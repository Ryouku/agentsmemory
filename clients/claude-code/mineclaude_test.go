package main

import (
	"strings"
	"testing"
)

// fixture builds a transcript with every kind of line the extractor must judge.
const mineFixture = `{"type":"user","cwd":"/home/u/proj","gitBranch":"main","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"why does the deploy fail on app2?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"let me think about this at great length"},{"type":"text","text":"The health check races the migration."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"kubectl get pods"}}]}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta housekeeping line"}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","text":"pod-1 Running"}]}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter that is not the conversation"}]}}
{"type":"user","message":{"role":"user","content":"Caveat: the messages below were generated while running local commands."}}
{"type":"user","message":{"role":"user","content":"<system-reminder>injected context</system-reminder>so fix the race?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Yes — gate the check on the migration job."}]}}
{"type":"file-history-snapshot","messageId":"x"}
`

// TestExtractSessionKeepsSpeechDropsMachinery is the miner's whole contract:
// ~90% of a transcript is tool traffic, and a palace mined without this filter
// recalls noise.
func TestExtractSessionKeepsSpeechDropsMachinery(t *testing.T) {
	doc := extractSession(strings.NewReader(mineFixture))

	if len(doc.Turns) != 4 {
		t.Fatalf("extracted %d turns, want 4: %q", len(doc.Turns), doc.Turns)
	}
	joined := strings.Join(doc.Turns, "\n")
	for _, want := range []string{"deploy fail on app2", "health check races", "fix the race", "gate the check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("conversation lost %q", want)
		}
	}
	for _, banned := range []string{"thinking about this", "kubectl", "pod-1", "subagent chatter", "Caveat:", "injected context", "meta housekeeping"} {
		if strings.Contains(joined, banned) {
			t.Errorf("machinery leaked into the corpus: %q", banned)
		}
	}
	if doc.Cwd != "/home/u/proj" || doc.Branch != "main" {
		t.Errorf("session origin lost: cwd=%q branch=%q", doc.Cwd, doc.Branch)
	}
}

// TestRenderSplitsMarathonSessions: one enormous session must become several
// parts with distinct source ids, or idempotent re-mining stops being true for
// exactly the sessions with the most in them.
func TestRenderSplitsMarathonSessions(t *testing.T) {
	doc := sessionDoc{Started: "2026-08-01"}
	turn := "A: " + strings.Repeat("insight ", 400) // ~3.2KB per turn
	for i := 0; i < 60; i++ {                       // ~190KB total, over the cap
		doc.Turns = append(doc.Turns, turn)
	}
	parts := doc.render("proj", "session-1")
	if len(parts) < 2 {
		t.Fatalf("a %dKB session rendered as %d part(s), want a split", 60*len(turn)/1000, len(parts))
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Source] {
			t.Errorf("duplicate source id %q — re-mining would clobber a sibling part", p.Source)
		}
		seen[p.Source] = true
		if len(p.Content) > mineDocCap+4000 {
			t.Errorf("part %q is %d runes, cap is %d", p.Source, len(p.Content), mineDocCap)
		}
	}
}

// TestShortSessionIDToleratesShortNames: the session id is a FILENAME under the
// transcripts root, so nothing guarantees it is a UUID. Abbreviating it for a
// progress line must not be able to end a mining run.
func TestShortSessionIDToleratesShortNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"01234567", "01234567"},
		{"0123456789abcdef", "01234567"},
		{"ąčęėįšųū-session", "ąčęėįšųū"}, // runes, not bytes: a byte slice would split one
	} {
		if got := shortSessionID(tc.in); got != tc.want {
			t.Errorf("shortSessionID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWingForSessionSurvivesDeletedProjects: an old project's directory may be
// gone; its sessions still deserve their own wing rather than one shared pile.
func TestWingForSessionSurvivesDeletedProjects(t *testing.T) {
	if w := wingForSession("/home/u/very-old-project", "-home-u-very-old-project"); w != "wing_very-old-project" {
		t.Errorf("wing from a vanished cwd = %q", w)
	}
	if w := wingForSession("", "-home-u-ancient"); !strings.HasPrefix(w, "wing_") {
		t.Errorf("wing without any cwd = %q", w)
	}
}
