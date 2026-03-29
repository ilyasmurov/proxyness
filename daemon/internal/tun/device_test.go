package tun

import (
	"testing"
)

func TestNewStack(t *testing.T) {
	s, ep, err := newStack(1500)
	if err != nil {
		t.Fatalf("newStack: %v", err)
	}
	defer s.Close()

	if ep == nil {
		t.Fatal("endpoint is nil")
	}

	nics := s.NICInfo()
	if len(nics) == 0 {
		t.Fatal("no NICs in stack")
	}
}
