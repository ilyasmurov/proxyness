package proxy

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

// yamux settings for TLS-mode sessions (MsgTypeMux, PRXNS-33).
// Keep in pair with daemon/internal/transport/tls_mux.go — both ends ping
// each other, so a mismatched keepalive or window only shows up as one side
// dropping the session early or capping throughput.
const (
	// The nginx SNI router in front of :443 drops a stream that stays silent
	// for proxy_timeout (12h); a ping every 10s keeps the session visible.
	// yamux v0.1.2 closes the WHOLE session when a ping's pong is later than
	// ConnectionWriteTimeout, and likewise when a stream's SYN stays unacked
	// for StreamOpenTimeout — so these two are 30s, not 10s: a 10–20s stall
	// (Wi-Fi roaming, LTE handover, a full uplink queue) must not cut every
	// stream at once; a TCP per stream used to ride out ~150s. A dead link is
	// found in ~40s (interval + write timeout).
	muxKeepAliveInterval      = 10 * time.Second
	muxConnectionWriteTimeout = 30 * time.Second
	muxStreamOpenTimeout      = 30 * time.Second
	// The default 256 KiB window caps a single stream at ~8 MB/s at 30 ms RTT.
	muxMaxStreamWindowSize = 4 << 20
)

// muxRecheckInterval: how often a mux session re-checks its device. A var so
// tests can shrink it.
var muxRecheckInterval = 60 * time.Second

// muxRevokeReason says why a live mux session must be cut after a device
// re-check, or "" to keep it. Only a device that is gone (db.ErrDeviceNotFound,
// however wrapped) or switched off ends the session. Any other error is a
// database hiccup — the device cache holds errors for up to a minute, and a
// short Postgres outage must not drop every client at once — so the session
// stays.
func muxRevokeReason(dev db.Device, err error) string {
	switch {
	case errors.Is(err, db.ErrDeviceNotFound):
		return "deleted"
	case err != nil:
		return ""
	case !dev.Active:
		return "disabled"
	}
	return ""
}

func muxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = muxKeepAliveInterval
	cfg.ConnectionWriteTimeout = muxConnectionWriteTimeout
	cfg.StreamOpenTimeout = muxStreamOpenTimeout
	cfg.MaxStreamWindowSize = muxMaxStreamWindowSize
	cfg.LogOutput = nil // yamux defaults to stderr; route it through our log
	cfg.Logger = muxLogger{}
	return cfg
}

// muxLogger sends yamux's own messages to the server log with a prefix.
type muxLogger struct{}

func (muxLogger) Print(v ...interface{}) { log.Printf("[proxy/mux] %s", fmt.Sprint(v...)) }
func (muxLogger) Printf(format string, v ...interface{}) {
	log.Printf("[proxy/mux] "+format, v...)
}
func (muxLogger) Println(v ...interface{}) { log.Printf("[proxy/mux] %s", fmt.Sprintln(v...)) }

// serveMux runs a yamux server session over conn (already past auth,
// machine-id and the MsgTypeMux reply). Every accepted stream gets its own
// goroutine that reads the stream's msg type and hands the stream to
// dispatch — exactly what Handle does with a whole connection on the
// per-connection path. Returns when the session ends (conn closed, keepalive
// failed, recheck said no) with the number of streams it accepted and the
// reason; all stream handlers and the recheck goroutine have returned by then.
//
// Key, machine-id and active flag are checked once per session at handshake,
// but a session can live for hours, so every muxRecheckInterval recheck is
// asked whether the device may stay (nil: never asked). The first false
// closes the session and with it every stream, like a disabled device used to
// be cut by its next TLS connection.
func serveMux(conn net.Conn, dispatch func(stream net.Conn, msgType byte), recheck func() bool) (int, error) {
	sess, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return 0, err
	}
	recheckDone := make(chan struct{})
	if recheck != nil {
		go func(every time.Duration) {
			defer close(recheckDone)
			watchDevice(sess, every, recheck)
		}(muxRecheckInterval)
	} else {
		close(recheckDone)
	}
	var wg sync.WaitGroup
	streams := 0
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			// Closing the session resets the streams still open, so their
			// handlers see an error and return.
			sess.Close()
			wg.Wait()
			<-recheckDone
			return streams, err
		}
		streams++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer st.Close()
			msgType, err := proto.ReadMsgType(st)
			if err != nil {
				return
			}
			dispatch(st, msgType)
		}()
	}
}

// watchDevice calls recheck every interval until the session ends; a false
// answer closes the session. It returns as soon as the session is closed, by
// anyone.
func watchDevice(sess *yamux.Session, every time.Duration, recheck func() bool) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-sess.CloseChan():
			return
		case <-tick.C:
			if !recheck() {
				sess.Close()
				return
			}
		}
	}
}
