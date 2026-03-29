package mux

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

const protoVersion = 0x01

func IsProxyProtocol(b byte) bool {
	return b == protoVersion
}

// PeekConn wraps net.Conn, allows peeking first byte without consuming it.
type PeekConn struct {
	net.Conn
	reader *bufio.Reader
}

func NewPeekConn(c net.Conn) *PeekConn {
	return &PeekConn{Conn: c, reader: bufio.NewReader(c)}
}

func (pc *PeekConn) PeekByte() (byte, error) {
	b, err := pc.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (pc *PeekConn) Read(p []byte) (int, error) {
	return pc.reader.Read(p)
}

// ListenerMux routes connections based on first byte.
type ListenerMux struct {
	ln           net.Listener
	proxyHandler func(net.Conn)
	httpHandler  http.Handler
	httpConns    chan net.Conn
}

func NewListenerMux(ln net.Listener, proxyHandler func(net.Conn), httpHandler http.Handler) *ListenerMux {
	return &ListenerMux{
		ln: ln, proxyHandler: proxyHandler,
		httpHandler: httpHandler, httpConns: make(chan net.Conn, 64),
	}
}

func (m *ListenerMux) Serve() error {
	httpLn := &chanListener{ch: m.httpConns, addr: m.ln.Addr()}
	go http.Serve(httpLn, m.httpHandler)
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			close(m.httpConns)
			return err
		}
		go m.route(conn)
	}
}

func (m *ListenerMux) Close() error { return m.ln.Close() }

func (m *ListenerMux) route(conn net.Conn) {
	pc := NewPeekConn(conn)
	b, err := pc.PeekByte()
	if err != nil {
		conn.Close()
		return
	}
	if IsProxyProtocol(b) {
		m.proxyHandler(pc)
	} else {
		m.httpConns <- pc
	}
}

// chanListener implements net.Listener using a channel.
type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func (l *chanListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, io.EOF
	}
	return conn, nil
}

func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }
