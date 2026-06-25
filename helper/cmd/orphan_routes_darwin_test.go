//go:build darwin

package main

import (
	"reflect"
	"testing"
)

// sample `netstat -rn -f inet` output with our split routes pointing at
// utun4, the ifscope bypass routes pointing at en0 via the gateway IP, and
// unrelated system routes.
const netstatSample = `Routing tables

Internet:
Destination        Gateway            Flags           Netif Expire
default            192.168.1.1        UGScg             en0
0/1                utun4              USc             utun4
0/1                192.168.1.1        UGSc              en0
10.0.85.1          10.0.85.1          UH              utun4
127                127.0.0.1          UCS               lo0
127.0.0.1          127.0.0.1          UH                lo0
128.0/1            utun4              USc             utun4
128.0/1            192.168.1.1        UGSc              en0
192.168.1          link#11            UCS               en0
`

func TestOrphanSplitRoutes(t *testing.T) {
	tests := []struct {
		name     string
		out      string
		exists   map[string]bool
		expected []string
	}{
		{
			name:     "utun gone — both split routes are orphans",
			out:      netstatSample,
			exists:   map[string]bool{"utun4": false},
			expected: []string{"0.0.0.0/1", "128.0.0.0/1"},
		},
		{
			name:     "utun still alive — nothing to clean (live foreign VPN)",
			out:      netstatSample,
			exists:   map[string]bool{"utun4": true},
			expected: nil,
		},
		{
			name:     "no split routes at all",
			out:      "default            192.168.1.1        UGScg             en0\n",
			exists:   map[string]bool{},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ifExists := func(name string) bool { return tc.exists[name] }
			got := orphanSplitRoutes(tc.out, ifExists)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("orphanSplitRoutes() = %v, want %v", got, tc.expected)
			}
		})
	}
}
