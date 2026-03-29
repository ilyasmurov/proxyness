package mux

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestIsProxyProtocol(t *testing.T) {
	if !IsProxyProtocol(0x01) {
		t.Fatal("0x01 should be proxy")
	}
	if IsProxyProtocol('G') {
		t.Fatal("G should not be proxy")
	}
	if IsProxyProtocol('P') {
		t.Fatal("P should not be proxy")
	}
}

func TestPeekConn(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() { c1.Write([]byte("hello world")) }()
	pc := NewPeekConn(c2)
	first, err := pc.PeekByte()
	if err != nil {
		t.Fatal(err)
	}
	if first != 'h' {
		t.Fatalf("expected 'h', got %c", first)
	}
	buf := make([]byte, 11)
	n, _ := pc.Read(buf)
	if string(buf[:n]) != "hello world" {
		t.Fatalf("got %q", string(buf[:n]))
	}
}

func TestListenerMux_HTTP(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	var gotHTTP bool
	m := NewListenerMux(ln,
		func(conn net.Conn) { conn.Close() },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHTTP = true
			w.WriteHeader(200)
		}),
	)
	go m.Serve()
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get("http://" + ln.Addr().String() + "/test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(50 * time.Millisecond)
	if !gotHTTP {
		t.Fatal("HTTP handler not called")
	}
}

func TestListenerMux_Proxy(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	var gotProxy bool
	m := NewListenerMux(ln,
		func(conn net.Conn) { gotProxy = true; conn.Close() },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
	go m.Serve()
	time.Sleep(50 * time.Millisecond)
	c, _ := net.Dial("tcp", ln.Addr().String())
	c.Write([]byte{0x01})
	c.Close()
	time.Sleep(50 * time.Millisecond)
	if !gotProxy {
		t.Fatal("proxy handler not called")
	}
}
