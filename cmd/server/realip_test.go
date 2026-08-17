package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRealIP pins the whole contract in one table: which peers are trusted,
// which header wins, and what happens to values we cannot use. The header cases
// all use a Docker-style peer because that is the deployment that motivated the
// middleware — cloudflared in a sidecar, every request arriving from 172.16/12.
func TestRealIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{{
		name:       "cloudflare header wins from a docker peer",
		remoteAddr: "172.17.0.5:41234",
		headers:    map[string]string{"CF-Connecting-IP": "203.0.113.7"},
		want:       "203.0.113.7",
	}, {
		// The precedence that matters in practice: Cloudflare sets both, and
		// CF-Connecting-IP is the one it guarantees.
		name:       "cf-connecting-ip beats x-forwarded-for",
		remoteAddr: "172.17.0.5:41234",
		headers: map[string]string{
			"CF-Connecting-IP": "203.0.113.7",
			"X-Forwarded-For":  "198.51.100.9",
		},
		want: "203.0.113.7",
	}, {
		name:       "true-client-ip is honoured",
		remoteAddr: "10.0.0.2:5000",
		headers:    map[string]string{"True-Client-IP": "203.0.113.8"},
		want:       "203.0.113.8",
	}, {
		name:       "x-real-ip is honoured",
		remoteAddr: "192.168.1.10:5000",
		headers:    map[string]string{"X-Real-IP": "203.0.113.9"},
		want:       "203.0.113.9",
	}, {
		name:       "x-forwarded-for takes the leftmost entry",
		remoteAddr: "172.20.0.3:5000",
		headers:    map[string]string{"X-Forwarded-For": "203.0.113.10, 198.51.100.1, 10.0.0.1"},
		want:       "203.0.113.10",
	}, {
		name:       "loopback peer is trusted",
		remoteAddr: "127.0.0.1:5000",
		headers:    map[string]string{"CF-Connecting-IP": "203.0.113.11"},
		want:       "203.0.113.11",
	}, {
		// The security case: a request straight off the internet may claim
		// anything it likes and must be ignored.
		name:       "public peer is not trusted",
		remoteAddr: "203.0.113.200:5000",
		headers:    map[string]string{"CF-Connecting-IP": "10.0.0.1"},
		want:       "203.0.113.200:5000",
	}, {
		name:       "public ipv6 peer is not trusted",
		remoteAddr: "[2001:db8::1]:5000",
		headers:    map[string]string{"CF-Connecting-IP": "203.0.113.12"},
		want:       "[2001:db8::1]:5000",
	}, {
		// A dual-stack listener reports a v4 peer in mapped form; it is still
		// the same private Docker address and must still be trusted.
		name:       "ipv4-mapped private peer is trusted",
		remoteAddr: "[::ffff:172.17.0.5]:41234",
		headers:    map[string]string{"CF-Connecting-IP": "203.0.113.13"},
		want:       "203.0.113.13",
	}, {
		// What --socket produces: net/http reports "@" as the peer, and only a
		// process on this host can have opened the 0600 socket.
		name:       "unix socket peer is trusted",
		remoteAddr: "@",
		headers:    map[string]string{"CF-Connecting-IP": "203.0.113.14"},
		want:       "203.0.113.14",
	}, {
		name:       "no headers leaves the peer untouched",
		remoteAddr: "172.17.0.5:41234",
		want:       "172.17.0.5:41234",
	}, {
		name:       "garbage header falls through to the next one",
		remoteAddr: "172.17.0.5:41234",
		headers: map[string]string{
			"CF-Connecting-IP": "not-an-ip",
			"X-Forwarded-For":  "203.0.113.15",
		},
		want: "203.0.113.15",
	}, {
		name:       "all-garbage headers leave the peer untouched",
		remoteAddr: "172.17.0.5:41234",
		headers:    map[string]string{"X-Forwarded-For": "unknown, 203.0.113.16"},
		want:       "172.17.0.5:41234",
	}, {
		// Some proxies append a source port; the port is not part of an address.
		name:       "header carrying a port is reduced to the ip",
		remoteAddr: "172.17.0.5:41234",
		headers:    map[string]string{"X-Forwarded-For": "203.0.113.17:5555"},
		want:       "203.0.113.17",
	}, {
		name:       "ipv6 client behind the tunnel",
		remoteAddr: "172.17.0.5:41234",
		headers:    map[string]string{"CF-Connecting-IP": "2001:db8::abcd"},
		want:       "2001:db8::abcd",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := realIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if got != tt.want {
				t.Fatalf("RemoteAddr = %q, want %q", got, tt.want)
			}
		})
	}
}
