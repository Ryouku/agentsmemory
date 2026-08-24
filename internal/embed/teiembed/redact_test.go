package teiembed

import (
	"strings"
	"testing"
	"time"
)

// TestConfiguredCredentialsNeverReachALogLine is the gate for F8.
//
// EMBED_URL is operator-supplied and `http://user:pass@host` is a valid value,
// so any log line or error that echoes the configured address writes the
// password into whatever collects those lines — outliving the process, readable
// by anyone with log access, and invisible to the operator who set it.
//
// The check is on the FIELD rather than on each call site, because the field is
// what a future log line will reach for. Every diagnostic path formats
// safeEndpoint; only the actual HTTP request uses infoEndpoint, which has to
// carry the credentials to authenticate.
func TestConfiguredCredentialsNeverReachALogLine(t *testing.T) {
	const secret = "hunter2"
	e := New("http://tei-user:"+secret+"@embed.internal:8080/", time.Second)

	if strings.Contains(e.safeEndpoint, secret) {
		t.Errorf("safeEndpoint carries the password: %q", e.safeEndpoint)
	}
	if strings.Contains(e.safeEndpoint, "tei-user") {
		t.Errorf("safeEndpoint carries the username: %q", e.safeEndpoint)
	}
	// It must stay diagnosable — a redaction that also removes the host turns a
	// connectivity bug into an unreadable log line, so people stop redacting.
	for _, want := range []string{"embed.internal", "8080", "/info"} {
		if !strings.Contains(e.safeEndpoint, want) {
			t.Errorf("safeEndpoint dropped %q, which an operator needs to diagnose: %q", want, e.safeEndpoint)
		}
	}
	// The request path still authenticates.
	if !strings.Contains(e.infoEndpoint, secret) {
		t.Errorf("infoEndpoint lost its credentials, so the probe cannot authenticate: %q", e.infoEndpoint)
	}
}

// TestRedactURLFailsClosedOnGarbage: an unparseable URL is exactly the case
// where assuming it holds no secret is unjustified, so it is replaced rather
// than echoed.
func TestRedactURLFailsClosedOnGarbage(t *testing.T) {
	got := redactURL("://not a url\x7f:pass@host")
	if strings.Contains(got, "pass") {
		t.Errorf("an unparseable URL was echoed, secrets and all: %q", got)
	}
}
