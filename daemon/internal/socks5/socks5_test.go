package socks5

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHandshake_IPv4(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x05, 0x01, 0x00})
		reply := make([]byte, 2)
		io.ReadFull(c1, reply)
		req := []byte{0x05, 0x01, 0x00, 0x01, 93, 184, 216, 34, 0x01, 0xBB}
		c1.Write(req)
	}()

	cr, err := Handshake(c2)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if cr.Addr != "93.184.216.34" {
		t.Fatalf("addr: got %s, want 93.184.216.34", cr.Addr)
	}
	if cr.Port != 443 {
		t.Fatalf("port: got %d, want 443", cr.Port)
	}
}

func TestHandshake_Domain(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x05, 0x01, 0x00})
		reply := make([]byte, 2)
		io.ReadFull(c1, reply)
		domain := "example.com"
		req := make([]byte, 4+1+len(domain)+2)
		req[0] = 0x05
		req[1] = 0x01
		req[2] = 0x00
		req[3] = 0x03
		req[4] = byte(len(domain))
		copy(req[5:], domain)
		binary.BigEndian.PutUint16(req[5+len(domain):], 80)
		c1.Write(req)
	}()

	cr, err := Handshake(c2)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if cr.Addr != "example.com" {
		t.Fatalf("addr: got %s, want example.com", cr.Addr)
	}
	if cr.Port != 80 {
		t.Fatalf("port: got %d, want 80", cr.Port)
	}
}

func TestHandshake_BadVersion(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x04, 0x01, 0x00})
	}()

	if _, err := Handshake(c2); err == nil {
		t.Fatal("expected error for SOCKS4")
	}
}

func TestSendSuccess(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		SendSuccess(c1)
		c1.Close()
	}()

	reply := make([]byte, 10)
	io.ReadFull(c2, reply)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("expected success reply, got %v", reply)
	}
}

func TestSendFailure(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		SendFailure(c1)
		c1.Close()
	}()

	reply := make([]byte, 10)
	io.ReadFull(c2, reply)
	if reply[0] != 0x05 || reply[1] != 0x01 {
		t.Fatalf("expected failure reply, got %v", reply)
	}
}
