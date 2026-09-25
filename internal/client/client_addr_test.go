package client

import (
	"net/url"
	"testing"
)

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

// Regression: hostPortAddr treated every IPv6 literal as "already has a
// port" because the address contains ':', so ws://[::1]/ws dialled an
// address with no port and failed with "missing port in address".
func TestHostPortAddrIPv6DefaultPort(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{"ipv6 ws default", "ws://[::1]/ws", "[::1]:80"},
		{"ipv6 wss default", "wss://[2001:db8::1]/ws", "[2001:db8::1]:443"},
		{"ipv6 explicit port", "ws://[::1]:8080/ws", "[::1]:8080"},
		{"ipv4 default", "ws://127.0.0.1/ws", "127.0.0.1:80"},
		{"hostname default", "ws://localhost/ws", "localhost:80"},
		{"explicit port", "ws://127.0.0.1:9000/ws", "127.0.0.1:9000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := parseURL(t, tc.raw)
			if got := hostPortAddr(u); got != tc.want {
				t.Fatalf("hostPortAddr(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
