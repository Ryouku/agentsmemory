package main

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIPHeaders lists the forwarded-for headers we honour, most authoritative
// first. CF-Connecting-IP is Cloudflare's own and is always the true client, so
// a cloudflared tunnel needs nothing else; True-Client-IP is its enterprise
// alias; X-Real-IP is the nginx/Traefik convention, kept so the middleware also
// works behind a plain reverse proxy. X-Forwarded-For comes last because it is a
// chain rather than a single address.
var clientIPHeaders = []string{
	"CF-Connecting-IP",
	"True-Client-IP",
	"X-Real-IP",
	"X-Forwarded-For",
}

// realIP replaces r.RemoteAddr with the client address a local reverse proxy
// forwarded, so access logs — and any future per-IP logic — see the actual user.
// Behind a cloudflared sidecar every request arrives from the container network
// (Docker's 172.16.0.0/12), which makes the raw RemoteAddr useless.
//
// The trust boundary is the PEER, not the header. Headers are honoured only when
// the connection itself came from a loopback, private, link-local or Unix-socket
// peer — something on this host, its container network, or its LAN. A request
// straight off the public internet keeps its real RemoteAddr, so an outside
// client cannot forge its own address into our logs. This is deliberately
// narrower than chi's middleware.RealIP, which trusts these headers from any
// peer at all and is therefore spoofable by anyone who can reach the port.
//
// It must be the first middleware in the chain so everything after it, Logger
// included, reads the corrected address.
func realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if peerIsLocal(r.RemoteAddr) {
			if ip := forwardedClientIP(r.Header); ip != "" {
				// Bare IP, no port — the same shape chi's RealIP writes. The
				// proxy's ephemeral source port says nothing about the client.
				r.RemoteAddr = ip
			}
		}
		next.ServeHTTP(w, r)
	})
}

// peerIsLocal reports whether the immediate peer of a connection is close enough
// to be trusted with forwarded-for headers: this machine, its container network,
// or its link.
//
// An address that does not parse as host:port is treated as local, because that
// is what a Unix-socket listener produces — net/http reports "@" for it — and a
// peer that reached our 0600 socket is by definition on this host. TCP always
// yields host:port, so there is no other way to land in that branch.
func peerIsLocal(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	// IsPrivate covers RFC1918 — including the 172.16/12 block Docker hands out
	// — and IPv6 unique-local fc00::/7. Unmap first so an IPv4-mapped v6 peer
	// (::ffff:172.17.0.1, what a dual-stack listener reports) is classified by
	// its v4 address rather than falling through as a plain global v6.
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// forwardedClientIP returns the first usable client address among the
// forwarded-for headers, or "" when none of them carries one.
func forwardedClientIP(h http.Header) string {
	for _, name := range clientIPHeaders {
		v := h.Get(name)
		if v == "" {
			continue
		}
		// X-Forwarded-For is a chain — "client, proxy1, proxy2" — whose leftmost
		// entry is the original client. Single-address headers contain no comma,
		// so this is a no-op for them.
		if i := strings.IndexByte(v, ','); i >= 0 {
			v = v[:i]
		}
		if ip, ok := parseClientIP(strings.TrimSpace(v)); ok {
			return ip
		}
		// A malformed value is skipped rather than fatal: try the next header
		// instead of stamping garbage onto RemoteAddr.
	}
	return ""
}

// parseClientIP accepts the two shapes proxies actually put in these headers — a
// bare address, and (from some proxies) an address with a port — and returns the
// canonical bare IP.
func parseClientIP(s string) (string, bool) {
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.Unmap().String(), true
	}
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return "", false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	return ip.Unmap().String(), true
}
