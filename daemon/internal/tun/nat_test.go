package tun

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestNATTable_HandlePacket(t *testing.T) {
	// Start a real UDP echo server
	echoAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	echoConn, err := net.ListenUDP("udp", echoAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer echoConn.Close()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := echoConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			echoConn.WriteToUDP(buf[:n], addr)
		}
	}()

	var replies int64
	nat := NewNATTable(func(pkt []byte) {
		atomic.AddInt64(&replies, 1)
	})
	// Use plain net.Dial for tests (protectedDial binds to physical iface,
	// which cannot reach 127.0.0.1 on macOS with IP_BOUND_IF)
	nat.dial = func(network, address string) (net.Conn, error) {
		return net.Dial(network, address)
	}
	defer nat.Close()

	echoPort := uint16(echoConn.LocalAddr().(*net.UDPAddr).Port)
	err = nat.HandlePacket(
		net.IP{10, 0, 0, 1}, net.IP{127, 0, 0, 1},
		12345, echoPort,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	if r := atomic.LoadInt64(&replies); r != 1 {
		t.Errorf("replies = %d, want 1", r)
	}
}

func TestNATTable_ReuseEntry(t *testing.T) {
	echoAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	echoConn, _ := net.ListenUDP("udp", echoAddr)
	defer echoConn.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := echoConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			echoConn.WriteToUDP(buf[:n], addr)
		}
	}()

	var replies int64
	nat := NewNATTable(func(pkt []byte) {
		atomic.AddInt64(&replies, 1)
	})
	nat.dial = func(network, address string) (net.Conn, error) {
		return net.Dial(network, address)
	}
	defer nat.Close()

	port := uint16(echoConn.LocalAddr().(*net.UDPAddr).Port)
	for i := 0; i < 5; i++ {
		nat.HandlePacket(net.IP{10, 0, 0, 1}, net.IP{127, 0, 0, 1}, 12345, port, []byte("hi"))
	}

	time.Sleep(200 * time.Millisecond)
	if r := atomic.LoadInt64(&replies); r != 5 {
		t.Errorf("replies = %d, want 5", r)
	}

	nat.mu.RLock()
	count := len(nat.entries)
	nat.mu.RUnlock()
	if count != 1 {
		t.Errorf("entries = %d, want 1", count)
	}
}
