package proto

import (
	"net"
	"testing"

	"smurov-proxy/pkg/auth"
)

func TestWriteReadAuth_Valid(t *testing.T) {
	key := auth.GenerateKey()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		if err := WriteAuth(c1, key); err != nil {
			t.Errorf("WriteAuth: %v", err)
		}
	}()

	if err := ReadAuth(c2, key); err != nil {
		t.Fatalf("ReadAuth: %v", err)
	}
}

func TestWriteReadAuth_WrongKey(t *testing.T) {
	key1 := auth.GenerateKey()
	key2 := auth.GenerateKey()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteAuth(c1, key1)
	}()

	if err := ReadAuth(c2, key2); err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestWriteReadResult(t *testing.T) {
	for _, ok := range []bool{true, false} {
		c1, c2 := net.Pipe()

		go func() {
			WriteResult(c1, ok)
			c1.Close()
		}()

		got, err := ReadResult(c2)
		c2.Close()
		if err != nil {
			t.Fatalf("ReadResult: %v", err)
		}
		if got != ok {
			t.Fatalf("expected %v, got %v", ok, got)
		}
	}
}

func TestWriteReadConnect_IPv4(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "93.184.216.34", 443)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "93.184.216.34" || port != 443 {
		t.Fatalf("got %s:%d, want 93.184.216.34:443", addr, port)
	}
}

func TestWriteReadConnect_Domain(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "example.com", 80)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "example.com" || port != 80 {
		t.Fatalf("got %s:%d, want example.com:80", addr, port)
	}
}

func TestWriteReadConnect_IPv6(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "2001:db8::1", 8080)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "2001:db8::1" || port != 8080 {
		t.Fatalf("got %s:%d, want 2001:db8::1:8080", addr, port)
	}
}

func TestRelay(t *testing.T) {
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()

	go Relay(c2, c3)

	// Write from c1, read from c4
	go func() {
		c1.Write([]byte("hello"))
		c1.Close()
	}()

	buf := make([]byte, 5)
	n, _ := c4.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q, want %q", string(buf[:n]), "hello")
	}
	c2.Close()
	c3.Close()
	c4.Close()
}
