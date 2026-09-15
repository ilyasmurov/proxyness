package topology

import (
	"errors"
	"testing"
	"time"
)

func findEdge(s *Snapshot, id string) *Edge {
	for i := range s.Edges {
		if s.Edges[i].ID == id {
			return &s.Edges[i]
		}
	}
	return nil
}

func findNode(s *Snapshot, id string) *Node {
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return &s.Nodes[i]
		}
	}
	return nil
}

func TestBuildSplitsDevicesBetweenDirectAndBridge(t *testing.T) {
	now := time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC)
	in := BuildInput{
		Now: now,
		Entries: []Entry{
			{ID: "aeza", Label: "Aeza NL", Addr: "178.236.252.28:443", Kind: "exit"},
			{ID: "ru", Label: "Aeza via RU", Addr: "157.22.194.55:4443", Kind: "bridge", Via: "aeza"},
		},
		Results: map[string]probeResult{
			"aeza": {ok: true, latencyMS: 45, total: 2, conns: []activeConn{
				{DeviceID: 1, RemoteIP: "87.248.238.76"},
				{DeviceID: 1, RemoteIP: "87.248.238.76"}, // second stream, same device
				{DeviceID: 2, RemoteIP: "157.22.194.55"}, // through the bridge
			}},
			"ru": {ok: true, latencyMS: 47},
		},
		LocalDevices: 2, DNSHost: "proxyness.smurov.com", DNSIPs: []string{"157.22.194.55"},
	}
	s := Build(in)
	if s.Overall != OK {
		t.Fatalf("overall = %s, want ok (checks: %+v)", s.Overall, s.Checks)
	}
	if e := findEdge(s, "clients-aeza"); e == nil || e.Devices != 1 || e.State != OK {
		t.Fatalf("direct edge: %+v", e)
	}
	if e := findEdge(s, "clients-ru"); e == nil || e.Devices != 1 {
		t.Fatalf("bridge inbound edge: %+v", e)
	}
	if e := findEdge(s, "ru-aeza"); e == nil || e.State != OK || e.LatencyMS != 47 {
		t.Fatalf("bridge→exit edge: %+v", e)
	}
	if n := findNode(s, "clients"); n == nil || n.Devices != 2 {
		t.Fatalf("clients node: %+v", n)
	}
	if n := findNode(s, "aeza"); n == nil || n.Devices != 2 || n.TotalDevices != 2 {
		t.Fatalf("exit node: %+v", n)
	}
	if e := findEdge(s, "control-aeza"); e == nil || e.State != OK {
		t.Fatalf("replica edge: %+v", e)
	}
}

func TestBuildMarksBrokenBridgeAndStaleReplica(t *testing.T) {
	in := BuildInput{
		Now: time.Now(),
		Entries: []Entry{
			{ID: "aeza", Label: "Aeza NL", Addr: "1.2.3.4:443", Kind: "exit"},
			{ID: "ru", Label: "via RU", Addr: "5.6.7.8:4443", Kind: "bridge", Via: "aeza"},
		},
		Results: map[string]probeResult{
			"aeza": {ok: true, latencyMS: 40, total: 1},
			"ru":   {err: "dial tcp 5.6.7.8:4443: i/o timeout", latencyMS: 5000},
		},
		LocalDevices: 3,
	}
	s := Build(in)
	if s.Overall != Down {
		t.Fatalf("overall = %s, want down", s.Overall)
	}
	if e := findEdge(s, "ru-aeza"); e == nil || e.State != Down || e.Error == "" {
		t.Fatalf("broken bridge edge: %+v", e)
	}
	if e := findEdge(s, "control-aeza"); e == nil || e.State != Down || e.Label != "DB replica · 3 devices here, 1 on the exit" {
		t.Fatalf("stale replica edge: %+v", e)
	}
}

func TestBuildControlPlaneChecksAndEgress(t *testing.T) {
	in := BuildInput{
		Now:       time.Now(),
		Entries:   []Entry{{ID: "x", Label: "X", Addr: "1.1.1.1:443", Kind: "exit"}},
		Results:   map[string]probeResult{"x": {ok: true}},
		ConfigErr: errors.New("config service: HTTP 502"), LocalDevices: -1,
		DNSHost: "proxyness.smurov.com", DNSErr: errors.New("no such host"),
		Egress: "http://1.1.1.1:3128", EgressLatencyMS: 400,
	}
	s := Build(in)
	c := findNode(s, "control")
	if c == nil || c.State != Down || len(c.Checks) != 3 {
		t.Fatalf("control node: %+v", c)
	}
	if e := findEdge(s, "clients-control"); e == nil || e.State != Down {
		t.Fatalf("config poll edge: %+v", e)
	}
	if e := findEdge(s, "taskless-egress"); e == nil || e.State != OK || e.LatencyMS != 400 {
		t.Fatalf("egress edge: %+v", e)
	}
	if findNode(s, "egress") == nil || findNode(s, "taskless") == nil {
		t.Fatal("egress nodes missing")
	}
}

func TestBuildWithoutServersIsUnknownNotDown(t *testing.T) {
	s := Build(BuildInput{Now: time.Now(), LocalDevices: -1, DNSHost: "h", DNSIPs: []string{"1.1.1.1"}})
	if s.Overall != Unknown {
		t.Fatalf("overall = %s, want unknown", s.Overall)
	}
}
