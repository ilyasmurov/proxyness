package tunnel

import (
	"testing"
)

func TestNew(t *testing.T) {
	tun := New()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
	if tun.Uptime() != 0 {
		t.Fatalf("expected 0 uptime, got %d", tun.Uptime())
	}
}

func TestStartStop(t *testing.T) {
	tun := New()

	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tun.GetStatus() != Connected {
		t.Fatalf("expected connected, got %s", tun.GetStatus())
	}

	tun.Stop()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
}

func TestDoubleStart(t *testing.T) {
	tun := New()

	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	err = tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestUptime(t *testing.T) {
	tun := New()
	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	if tun.Uptime() < 0 {
		t.Fatal("uptime should be >= 0")
	}
}
