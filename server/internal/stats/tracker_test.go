package stats

import "testing"

func TestAddRemoveConn(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice", "", "1.2.3.4")
	conns := tr.Active()
	if len(conns) != 1 { t.Fatalf("expected 1, got %d", len(conns)) }
	if conns[0].DeviceName != "MacBook" || conns[0].UserName != "Alice" { t.Fatalf("unexpected: %+v", conns[0]) }
	info := tr.Remove(id)
	if info == nil { t.Fatal("expected conn info") }
	if len(tr.Active()) != 0 { t.Fatal("expected 0 active") }
}

func TestUpdateBytes(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice", "", "1.2.3.4")
	tr.AddBytes(id, 100, 200)
	tr.AddBytes(id, 50, 30)
	conns := tr.Active()
	if conns[0].BytesIn != 150 || conns[0].BytesOut != 230 { t.Fatalf("bytes: in=%d out=%d", conns[0].BytesIn, conns[0].BytesOut) }
	info := tr.Remove(id)
	if info.BytesIn != 150 || info.BytesOut != 230 { t.Fatalf("removed: in=%d out=%d", info.BytesIn, info.BytesOut) }
}

func TestDeviceAccessSameIP(t *testing.T) {
	tr := New()
	tr.Add(1, "MacBook", "Alice", "", "1.2.3.4")
	if !tr.CheckDeviceAccess(1, "1.2.3.4") {
		t.Fatal("expected access allowed for same IP")
	}
	if tr.CheckDeviceAccess(1, "5.6.7.8") {
		t.Fatal("expected access denied for different IP")
	}
}

func TestDeviceAccessAfterDisconnect(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice", "", "1.2.3.4")
	tr.Remove(id)
	if !tr.CheckDeviceAccess(1, "5.6.7.8") {
		t.Fatal("expected access allowed after disconnect")
	}
}

func TestActiveCount(t *testing.T) {
	tr := New()
	tr.Add(1, "A", "U", "", "1.2.3.4")
	tr.Add(2, "B", "U", "", "1.2.3.4")
	if tr.ActiveCount() != 2 { t.Fatalf("expected 2, got %d", tr.ActiveCount()) }
}
