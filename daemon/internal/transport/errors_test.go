package transport

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestIsNetworkUnreachable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "direct ENETUNREACH syscall errno",
			err:  syscall.ENETUNREACH,
			want: true,
		},
		{
			name: "ENETUNREACH wrapped with fmt.Errorf %w",
			err:  fmt.Errorf("tcp dial: %w", syscall.ENETUNREACH),
			want: true,
		},
		{
			name: "ENETUNREACH double-wrapped (auto.go both-transports chain)",
			err:  fmt.Errorf("both transports failed: %w", fmt.Errorf("tcp dial: %w", syscall.ENETUNREACH)),
			want: true,
		},
		{
			name: "plain string match 'network is unreachable' without errno",
			err:  errors.New("connect: network is unreachable"),
			want: true,
		},
		{
			name: "unrelated error (connection refused)",
			err:  syscall.ECONNREFUSED,
			want: false,
		},
		{
			name: "unrelated error (timeout)",
			err:  errors.New("dial tcp: i/o timeout"),
			want: false,
		},
		{
			name: "host unreachable is NOT network unreachable",
			err:  syscall.EHOSTUNREACH,
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsNetworkUnreachable(tc.err)
			if got != tc.want {
				t.Errorf("IsNetworkUnreachable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsSystemOffline(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "helper refresh_routes reply, verbatim",
			err:  errors.New("helper: no default gateway (system offline)"),
			want: true,
		},
		{
			name: "wrapped by a caller",
			err:  fmt.Errorf("refresh routes: %w", errors.New("helper: no default gateway (system offline)")),
			want: true,
		},
		{
			name: "helper up but TUN gone is NOT system offline",
			err:  errors.New("helper: no TUN device"),
			want: false,
		},
		{
			name: "ENETUNREACH is a dial failure, not a helper verdict",
			err:  syscall.ENETUNREACH,
			want: false,
		},
		{
			name: "unrelated helper error",
			err:  errors.New("helper: dial: connection refused"),
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSystemOffline(tc.err)
			if got != tc.want {
				t.Errorf("IsSystemOffline(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
