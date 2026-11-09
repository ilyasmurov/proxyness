package transport

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
)

// yamux settings for TLS-mode sessions (PRXNS-33).
// Keep in pair with server/internal/proxy/session.go — both ends ping each
// other, so a mismatched keepalive or window only shows up as one side
// dropping the session early or capping throughput.
const (
	// The nginx SNI router in front of :443 drops a stream that stays silent
	// for proxy_timeout (12h); a ping every 10s keeps the session visible and
	// finds a dead link in ~20s (interval + write timeout).
	muxKeepAliveInterval      = 10 * time.Second
	muxConnectionWriteTimeout = 10 * time.Second
	muxStreamOpenTimeout      = 10 * time.Second
	// The default 256 KiB window caps a single stream at ~8 MB/s at 30 ms RTT.
	muxMaxStreamWindowSize = 4 << 20
)

// Pool shape.
const (
	// Sessions kept up at all times.
	muxBaseSessions = 2
	// Sessions the pool may grow to, one at a time, while every live session
	// carries at least muxGrowAtStreams open streams.
	muxMaxSessions   = 4
	muxGrowAtStreams = 64
	// Pause between failed background dials: 1s, 2s, 4s … 30s; reset by a
	// successful one.
	muxRedialBackoffMin = 1 * time.Second
	muxRedialBackoffMax = 30 * time.Second
	// How long a TCP stream waits for the server's connect result. The server
	// gives the target up to 10s itself.
	muxConnectResultTimeout = 15 * time.Second
)

// The pool knobs as vars so tests can shrink them, like tlsSetupTimeout.
// Connect snapshots them into the transport.
var (
	tlsMuxBaseSessions         = muxBaseSessions
	tlsMuxMaxSessions          = muxMaxSessions
	tlsMuxGrowAtStreams        = muxGrowAtStreams
	tlsMuxBackoffMin           = muxRedialBackoffMin
	tlsMuxBackoffMax           = muxRedialBackoffMax
	tlsMuxConnectResultTimeout = muxConnectResultTimeout
)

type muxLimits struct {
	base, max, growAt      int
	backoffMin, backoffMax time.Duration
	connectResult          time.Duration
}

func currentMuxLimits() muxLimits {
	return muxLimits{
		base:          tlsMuxBaseSessions,
		max:           tlsMuxMaxSessions,
		growAt:        tlsMuxGrowAtStreams,
		backoffMin:    tlsMuxBackoffMin,
		backoffMax:    tlsMuxBackoffMax,
		connectResult: tlsMuxConnectResultTimeout,
	}
}

// errNoMux: the server passed auth and machine-id, then closed the connection
// (or said no) on MsgTypeMux — a pre-PRXNS-33 server. Never reaches the
// engine: whoever gets it switches the transport to per-stream and dials the
// old way within the same call.
var errNoMux = errors.New("server has no mux")

// muxSessionSeq numbers sessions across the daemon's life, so "up" and
// "closed" lines pair up in daemon.log even across transport rebuilds.
var muxSessionSeq atomic.Int64

type muxSession struct {
	n       int64
	sess    *yamux.Session
	conn    *errConn
	log     *muxLogger
	started time.Time

	// Guarded by muxPool.poolMu.
	open   int    // streams handed out and not yet Close()d
	served int    // streams opened over the session's life
	gone   bool   // no longer in the pool
	reason string // why we closed it ourselves, if we did
}

// muxDial is one in-flight session dial; everyone who needs it waits on done.
type muxDial struct {
	done chan struct{}
	err  error
}

// muxPool is the TLSTransport's set of yamux sessions. All fields are guarded
// by poolMu; yamux calls that can block (Close, OpenStream) run outside it.
type muxPool struct {
	poolMu   sync.Mutex
	sessions []*muxSession
	inflight *muxDial
	growing  bool // growLoop is running
	backoff  time.Duration
	retryAt  time.Time
	closed   bool
}

// dialSession dials a fresh TLS connection, runs auth and machine-id, asks
// for MsgTypeMux and starts a yamux client on it. Handshake errors come back
// exactly as handshake() words them; a server that closes the connection or
// says no on MsgTypeMux gives errNoMux; a server that says nothing within
// setupTimeout is a network failure, not an old server.
func (t *TLSTransport) dialSession() (*muxSession, error) {
	conn, rawConn, err := t.handshake()
	if err != nil {
		return nil, err
	}
	rawConn.SetDeadline(time.Now().Add(t.setupTimeout))
	if err := proto.WriteMsgType(conn, proto.MsgTypeMux); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mux: %w", err)
	}
	ok, err := proto.ReadResult(conn)
	if err != nil {
		conn.Close()
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("mux: %w", err)
		}
		return nil, errNoMux
	}
	if !ok {
		conn.Close()
		return nil, errNoMux
	}
	rawConn.SetDeadline(time.Time{})

	ec := &errConn{Conn: conn}
	lg := &muxLogger{}
	sess, err := yamux.Client(ec, muxClientConfig(lg))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mux: %w", err)
	}
	return &muxSession{sess: sess, conn: ec, log: lg}, nil
}

func muxClientConfig(lg *muxLogger) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = muxKeepAliveInterval
	cfg.ConnectionWriteTimeout = muxConnectionWriteTimeout
	cfg.StreamOpenTimeout = muxStreamOpenTimeout
	cfg.MaxStreamWindowSize = muxMaxStreamWindowSize
	cfg.LogOutput = nil // yamux defaults to stderr; route it through our log
	cfg.Logger = lg
	return cfg
}

// adoptSession puts a dialed session into the pool and starts watching it.
// A session dialed while Close() ran is closed on the spot.
func (t *TLSTransport) adoptSession(s *muxSession) {
	t.poolMu.Lock()
	ok := t.addLocked(s)
	t.poolMu.Unlock()
	if !ok {
		s.sess.Close()
		return
	}
	t.sessionUp(s)
}

func (t *TLSTransport) addLocked(s *muxSession) bool {
	if t.closed {
		return false
	}
	s.n = muxSessionSeq.Add(1)
	s.started = time.Now()
	t.sessions = append(t.sessions, s)
	return true
}

// sessionUp runs once a session is in the pool: log, watch, top up the pool.
func (t *TLSTransport) sessionUp(s *muxSession) {
	log.Printf("[tls] mux session #%d up (server %s)", s.n, t.server)
	go t.watchSession(s)
	t.maybeGrow()
}

// watchSession drops a session from the pool once yamux shuts it down
// (connection lost, keepalive failed, or we closed it) and tops the pool up.
// Its streams see read/write errors, same as a dropped per-stream
// connection. It never touches done: D1 fires on Close() only.
func (t *TLSTransport) watchSession(s *muxSession) {
	<-s.sess.CloseChan()
	t.poolMu.Lock()
	t.removeLocked(s)
	reason, served := s.reason, s.served
	t.poolMu.Unlock()
	if reason == "" {
		reason = s.log.lastError()
	}
	if reason == "" {
		reason = s.conn.firstError()
	}
	if reason == "" {
		reason = "session shut down"
	}
	log.Printf("[tls] mux session #%d closed after %s, streams served %d: %s",
		s.n, time.Since(s.started).Round(time.Second), served, reason)
	t.maybeGrow()
}

func (t *TLSTransport) removeLocked(s *muxSession) {
	if s.gone {
		return
	}
	s.gone = true
	for i, x := range t.sessions {
		if x == s {
			t.sessions = append(t.sessions[:i], t.sessions[i+1:]...)
			break
		}
	}
}

// liveLocked counts sessions yamux still runs and whether each of them
// carries at least growAt open streams.
func (t *TLSTransport) liveLocked() (live int, allBusy bool) {
	allBusy = true
	for _, s := range t.sessions {
		if s.sess.IsClosed() {
			continue
		}
		live++
		if s.open < t.limits.growAt {
			allBusy = false
		}
	}
	return live, allBusy
}

// needDialLocked: fewer than base sessions, or all of them busy and room to
// grow.
func (t *TLSTransport) needDialLocked() bool {
	if t.closed || t.perStream.Load() {
		return false
	}
	live, allBusy := t.liveLocked()
	if live < t.limits.base {
		return true
	}
	return live < t.limits.max && allBusy
}

// startDialLocked joins the in-flight dial or starts one. One dial at a time,
// whoever asked for it.
func (t *TLSTransport) startDialLocked() *muxDial {
	if t.inflight != nil {
		return t.inflight
	}
	d := &muxDial{done: make(chan struct{})}
	t.inflight = d
	go t.runDial(d)
	return d
}

func (t *TLSTransport) runDial(d *muxDial) {
	s, err := t.dialSession()

	t.poolMu.Lock()
	t.inflight = nil
	adopted := false
	switch {
	case err == nil:
		t.backoff = 0
		t.retryAt = time.Time{}
		adopted = t.addLocked(s)
	case errors.Is(err, errNoMux):
	default:
		if t.backoff == 0 {
			t.backoff = t.limits.backoffMin
		} else if t.backoff *= 2; t.backoff > t.limits.backoffMax {
			t.backoff = t.limits.backoffMax
		}
		t.retryAt = time.Now().Add(t.backoff)
	}
	backoff := t.backoff
	t.poolMu.Unlock()

	d.err = err
	if errors.Is(err, errNoMux) {
		t.switchToPerStream()
	}
	close(d.done)

	switch {
	case err == nil && adopted:
		t.sessionUp(s)
	case err == nil:
		s.sess.Close() // transport closed while we dialed
	case !errors.Is(err, errNoMux):
		log.Printf("[tls] mux dial failed: %v (next background try in %s)", err, backoff)
	}
}

// maybeGrow starts the background dialer if the pool needs a session and
// the dialer is not already running.
func (t *TLSTransport) maybeGrow() {
	t.poolMu.Lock()
	defer t.poolMu.Unlock()
	if t.growing || !t.needDialLocked() {
		return
	}
	t.growing = true
	go t.growLoop()
}

// growLoop dials sessions one at a time until the pool has what it needs,
// pausing after failures. Exits when nothing is needed or the transport is
// closed.
func (t *TLSTransport) growLoop() {
	for {
		t.poolMu.Lock()
		if !t.needDialLocked() {
			t.growing = false
			t.poolMu.Unlock()
			return
		}
		d := t.inflight
		wait := time.Until(t.retryAt)
		if d == nil && wait <= 0 {
			d = t.startDialLocked()
		}
		t.poolMu.Unlock()

		if d != nil {
			<-d.done
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-t.done:
			timer.Stop()
		}
	}
}

// reserveSession picks the live session with the fewest open streams and
// counts a stream on it. With no live session it waits for a dial — the
// in-flight one or a new one, ignoring the background pause, since a caller
// is waiting — and returns that dial's error unchanged.
func (t *TLSTransport) reserveSession() (*muxSession, error) {
	for attempt := 0; ; attempt++ {
		t.poolMu.Lock()
		if t.closed {
			t.poolMu.Unlock()
			return nil, fmt.Errorf("not connected")
		}
		var best *muxSession
		for _, s := range t.sessions {
			if s.sess.IsClosed() {
				continue
			}
			if best == nil || s.open < best.open {
				best = s
			}
		}
		if best != nil {
			best.open++
			grow := best.open >= t.limits.growAt
			t.poolMu.Unlock()
			if grow {
				t.maybeGrow()
			}
			return best, nil
		}
		if attempt >= 2 {
			t.poolMu.Unlock()
			return nil, fmt.Errorf("mux: no live session")
		}
		d := t.startDialLocked()
		t.poolMu.Unlock()
		<-d.done
		if d.err != nil {
			return nil, d.err
		}
	}
}

// releaseStream un-counts a stream. After Close() the session that just lost
// its last stream is closed: it was only kept for that stream.
func (t *TLSTransport) releaseStream(s *muxSession) {
	t.poolMu.Lock()
	s.open--
	drain := t.closed && s.open == 0 && !s.gone
	if drain {
		s.reason = "transport closed, last stream done"
	}
	t.poolMu.Unlock()
	if drain {
		s.sess.Close()
	}
}

// dropSession closes a session that failed us before its watcher noticed.
func (t *TLSTransport) dropSession(s *muxSession, why error) {
	t.poolMu.Lock()
	if s.reason == "" {
		s.reason = why.Error()
	}
	t.poolMu.Unlock()
	s.sess.Close()
}

// openMuxStream opens a stream inside the pool. Errors are worded as on the
// per-stream path: dial errors as is, a refused or unanswered TCP connect as
// "connect rejected". A stream lost because its session died during setup
// is retried on another session.
func (t *TLSTransport) openMuxStream(streamType byte, addr string, port uint16) (Stream, error) {
	var lastErr error
	for attempt := 0; attempt <= t.limits.max; attempt++ {
		s, err := t.reserveSession()
		if err != nil {
			return nil, err
		}
		st, err := s.sess.OpenStream()
		if err != nil {
			t.releaseStream(s)
			t.dropSession(s, fmt.Errorf("open stream: %w", err))
			lastErr = err
			continue
		}
		stream, err := t.setupMuxStream(s, st, streamType, addr, port)
		if err != nil && s.sess.IsClosed() {
			lastErr = err
			continue
		}
		return stream, err
	}
	return nil, fmt.Errorf("mux open stream: %w", lastErr)
}

// setupMuxStream sends what a per-stream connection sends after machine-id.
// On failure the stream is closed and un-counted; the session is left to
// keepalive.
func (t *TLSTransport) setupMuxStream(s *muxSession, st *yamux.Stream, streamType byte, addr string, port uint16) (Stream, error) {
	fail := func(err error) (Stream, error) {
		st.Close()
		t.releaseStream(s)
		return nil, err
	}

	msgType := byte(proto.MsgTypeTCP)
	if streamType == 0x02 {
		msgType = proto.MsgTypeUDP
	}
	if err := proto.WriteMsgType(st, msgType); err != nil {
		return fail(err)
	}
	if streamType == 0x01 {
		if err := proto.WriteConnect(st, addr, port); err != nil {
			return fail(err)
		}
		st.SetReadDeadline(time.Now().Add(t.limits.connectResult))
		ok, err := proto.ReadResult(st)
		st.SetReadDeadline(time.Time{})
		if err != nil || !ok {
			return fail(fmt.Errorf("connect rejected: %s:%d", addr, port))
		}
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	t.poolMu.Lock()
	s.served++
	t.poolMu.Unlock()
	return &muxStream{st: st, id: id, t: t, s: s}, nil
}

// closePool runs once, from the first Close(): idle sessions close now,
// sessions with streams close after their last stream (releaseStream) or
// when yamux gives up on them.
func (t *TLSTransport) closePool() {
	t.poolMu.Lock()
	if t.closed {
		t.poolMu.Unlock()
		return
	}
	t.closed = true
	var idle []*muxSession
	for _, s := range t.sessions {
		if s.open == 0 {
			s.reason = "transport closed"
			idle = append(idle, s)
		}
	}
	t.poolMu.Unlock()
	for _, s := range idle {
		s.sess.Close()
	}
}

func (t *TLSTransport) switchToPerStream() {
	if t.perStream.CompareAndSwap(false, true) {
		log.Printf("[tls] server has no mux, per-stream mode")
	}
}

type muxStream struct {
	st   *yamux.Stream
	id   uint32
	t    *TLSTransport
	s    *muxSession
	once sync.Once
}

func (m *muxStream) Read(p []byte) (int, error)  { return m.st.Read(p) }
func (m *muxStream) Write(p []byte) (int, error) { return m.st.Write(p) }
func (m *muxStream) ID() uint32                  { return m.id }

func (m *muxStream) Close() error {
	var err error
	m.once.Do(func() {
		err = m.st.Close()
		m.t.releaseStream(m.s)
	})
	return err
}

// errConn remembers the first read/write error on a session's connection:
// the reason a session died when yamux itself stays quiet (EOF, reset).
type errConn struct {
	net.Conn
	mu  sync.Mutex
	err error
}

func (c *errConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.note(err)
	return n, err
}

func (c *errConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.note(err)
	return n, err
}

func (c *errConn) note(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *errConn) firstError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return ""
	}
	return c.err.Error()
}

// muxLogger sends yamux's own messages to the daemon log with a prefix and
// keeps the last error, which names the reason when yamux kills a session
// (keepalive failed, write timeout).
type muxLogger struct {
	mu   sync.Mutex
	last string
}

func (l *muxLogger) Print(v ...interface{}) { l.note(fmt.Sprint(v...)) }
func (l *muxLogger) Printf(format string, v ...interface{}) {
	l.note(fmt.Sprintf(format, v...))
}
func (l *muxLogger) Println(v ...interface{}) { l.note(fmt.Sprintln(v...)) }

func (l *muxLogger) note(msg string) {
	msg = strings.TrimSpace(msg)
	if strings.Contains(msg, "[ERR]") {
		l.mu.Lock()
		l.last = msg
		l.mu.Unlock()
	}
	log.Printf("[tls/mux] %s", msg)
}

func (l *muxLogger) lastError() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}
