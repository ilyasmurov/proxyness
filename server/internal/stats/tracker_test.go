package stats

import "testing"

func TestAddRemoveConn(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice", "")
	conns := tr.Active()
	if len(conns) != 1 { t.Fatalf("expected 1, got %d", len(conns)) }
	if conns[0].DeviceName != "MacBook" || conns[0].UserName != "Alice" { t.Fatalf("unexpected: %+v", conns[0]) }
	info := tr.Remove(id)
	if info == nil { t.Fatal("expected conn info") }
	if len(tr.Active()) != 0 { t.Fatal("expected 0 active") }
}

func TestUpdateBytes(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice", "")
	tr.AddBytes(id, 100, 200)
	tr.AddBytes(id, 50, 30)
	conns := tr.Active()
	if conns[0].BytesIn != 150 || conns[0].BytesOut != 230 { t.Fatalf("bytes: in=%d out=%d", conns[0].BytesIn, conns[0].BytesOut) }
	info := tr.Remove(id)
	if info.BytesIn != 150 || info.BytesOut != 230 { t.Fatalf("removed: in=%d out=%d", info.BytesIn, info.BytesOut) }
}

func TestActiveCount(t *testing.T) {
	tr := New()
	tr.Add(1, "A", "U", "")
	tr.Add(2, "B", "U", "")
	if tr.ActiveCount() != 2 { t.Fatalf("expected 2, got %d", tr.ActiveCount()) }
}
