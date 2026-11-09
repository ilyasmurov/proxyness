package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
)

// yamux settings for TLS-mode sessions (MsgTypeMux, PRXNS-33).
// Keep in pair with daemon/internal/transport/tls_mux.go — both ends ping
// each other, so a mismatched keepalive or window only shows up as one side
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
// failed) with the number of streams it accepted and the reason; all stream
// handlers have returned by then.
func serveMux(conn net.Conn, dispatch func(stream net.Conn, msgType byte)) (int, error) {
	sess, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return 0, err
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
