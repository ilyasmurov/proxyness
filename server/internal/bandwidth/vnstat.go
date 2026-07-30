// Package bandwidth exposes host-level interface statistics collected by vnstat.
//
// A timer on the host writes `vnstat --json` into the proxyness-data volume,
// which the container sees as /data/vnstat.json. Nothing here shells out to
// vnstat itself — the container has no access to the host's network counters.
package bandwidth

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// DefaultPath is where the host timer drops its export inside the container.
const DefaultPath = "/data/vnstat.json"

// StaleAfter marks a snapshot as stale once the export stops being refreshed.
// The timer runs every 5 minutes, so 15 covers two missed runs.
const StaleAfter = 15 * time.Minute

// Retention windows per resolution.
const (
	fiveMinuteWindow = 24 * time.Hour
	hourWindow       = 48 * time.Hour
	dayWindow        = 30 * 24 * time.Hour
)

// Interval length of each sample, used to turn byte counts into rates.
const (
	fiveMinuteSecs = 300
	hourSecs       = 3600
	daySecs        = 86400
)

// Snapshot is the normalized view handed to the admin dashboard.
type Snapshot struct {
	UpdatedAt  int64       `json:"updated_at"`
	Stale      bool        `json:"stale"`
	Interfaces []Interface `json:"interfaces"`
}

// Interface holds one network interface's history at three resolutions.
type Interface struct {
	Name       string  `json:"name"`
	TotalRx    uint64  `json:"total_rx"`
	TotalTx    uint64  `json:"total_tx"`
	FiveMinute []Point `json:"fiveminute"`
	Hour       []Point `json:"hour"`
	Day        []Point `json:"day"`
}

// Point is one sample: raw bytes plus the average rate across the interval.
type Point struct {
	T      int64   `json:"t"`
	Rx     uint64  `json:"rx"`
	Tx     uint64  `json:"tx"`
	RxMbps float64 `json:"rx_mbps"`
	TxMbps float64 `json:"tx_mbps"`
}

// vnstat's own JSON shape (jsonversion 2), only the fields we consume.
type vnstatFile struct {
	Interfaces []struct {
		Name    string `json:"name"`
		Updated struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"updated"`
		Traffic struct {
			Total struct {
				Rx uint64 `json:"rx"`
				Tx uint64 `json:"tx"`
			} `json:"total"`
			FiveMinute []vnstatSample `json:"fiveminute"`
			Hour       []vnstatSample `json:"hour"`
			Day        []vnstatSample `json:"day"`
		} `json:"traffic"`
	} `json:"interfaces"`
}

type vnstatSample struct {
	Timestamp int64  `json:"timestamp"`
	Rx        uint64 `json:"rx"`
	Tx        uint64 `json:"tx"`
}

// Load reads and parses the export written by the host timer.
func Load(path string, now time.Time) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vnstat export: %w", err)
	}
	return Parse(data, now)
}

// Parse turns a vnstat JSON export into a Snapshot.
//
// Virtual interfaces are dropped: docker bridges and veth pairs carry
// container-internal traffic, and ifb0 is our own shaper device, which would
// double-count everything already counted as ingress on the physical NIC.
func Parse(data []byte, now time.Time) (*Snapshot, error) {
	var raw vnstatFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse vnstat export: %w", err)
	}

	snap := &Snapshot{Interfaces: []Interface{}}
	for _, iface := range raw.Interfaces {
		if isVirtual(iface.Name) {
			continue
		}
		if iface.Updated.Timestamp > snap.UpdatedAt {
			snap.UpdatedAt = iface.Updated.Timestamp
		}
		snap.Interfaces = append(snap.Interfaces, Interface{
			Name:       iface.Name,
			TotalRx:    iface.Traffic.Total.Rx,
			TotalTx:    iface.Traffic.Total.Tx,
			FiveMinute: convert(iface.Traffic.FiveMinute, fiveMinuteSecs, now.Add(-fiveMinuteWindow)),
			Hour:       convert(iface.Traffic.Hour, hourSecs, now.Add(-hourWindow)),
			Day:        convert(iface.Traffic.Day, daySecs, now.Add(-dayWindow)),
		})
	}

	if snap.UpdatedAt > 0 {
		snap.Stale = now.Sub(time.Unix(snap.UpdatedAt, 0)) > StaleAfter
	}
	return snap, nil
}

// isVirtual reports whether an interface carries traffic we would double-count
// or that never leaves the host.
func isVirtual(name string) bool {
	switch {
	case name == "lo", name == "ifb0", name == "docker0":
		return true
	case strings.HasPrefix(name, "veth"), strings.HasPrefix(name, "br-"):
		return true
	}
	return false
}

// convert drops samples older than cutoff, sorts them oldest-first and derives
// the average rate over each interval. vnstat returns samples in ring-buffer
// order, not chronological order, so sorting is not optional.
func convert(samples []vnstatSample, intervalSecs int64, cutoff time.Time) []Point {
	points := make([]Point, 0, len(samples))
	for _, s := range samples {
		if s.Timestamp < cutoff.Unix() {
			continue
		}
		points = append(points, Point{
			T:      s.Timestamp,
			Rx:     s.Rx,
			Tx:     s.Tx,
			RxMbps: mbps(s.Rx, intervalSecs),
			TxMbps: mbps(s.Tx, intervalSecs),
		})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].T < points[j].T })
	return points
}

// mbps converts a byte count over an interval into megabits per second.
func mbps(bytes uint64, secs int64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(bytes) * 8 / float64(secs) / 1e6
}
