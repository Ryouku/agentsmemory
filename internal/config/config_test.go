package config

import "testing"

// TestIsLoopback pins the classification that decides whether local mode warns
// about exposing its unauthenticated endpoint. The case that matters most is
// ":8080" — the multi-tenant default, which binds every interface and must NOT
// be read as safe just because it names no host.
func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{LocalAddr, true},
		{"127.0.0.1:9000", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"127.5.5.5:8080", true}, // the whole 127/8 block is loopback
		{":8080", false},         // every interface — the dangerous default
		{"0.0.0.0:8080", false},
		{"192.168.1.10:8080", false},
		{"8080", false}, // unparseable: unknown must not be treated as safe
		{"", false},
	}
	for _, tc := range tests {
		if got := IsLoopback(tc.addr); got != tc.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
