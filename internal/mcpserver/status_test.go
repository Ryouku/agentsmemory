package mcpserver

import (
	"errors"
	"os"
	"strings"
	"testing"
)

var errCounting = errors.New("count failed")

// TestStatusNamesAWaitingInbox.
//
// An inbox item is only read if something makes the reader look. Measured
// 2026-08-20: every explicit tunnel in this palace reported access_count 0 since
// creation, and six handoff drawers had never been opened. The count existed the
// whole time — inside am_status's `wings` array, as one number among sixty.
//
// am_status is the site because it is the one call the protocol mandates first
// and it is server-side, so it cannot drift per-harness. The hint is the part an
// agent actually reads, so it must CHANGE when there is something to read: prose
// that always says "check your inbox" is prose that is always skipped.
func TestStatusNamesAWaitingInbox(t *testing.T) {
	waiting := inboxStatus("wing_agentmemories", 3, nil)
	if waiting.Count != 3 || waiting.Wing != "wing_agentmemories" {
		t.Fatalf("inbox block = %+v; want 3 in wing_agentmemories", waiting)
	}
	if !waiting.Known {
		t.Error("a counted inbox must report as known")
	}

	hint := statusHint(waiting)
	if !strings.Contains(hint, "3") || !strings.Contains(hint, "wing_agentmemories") {
		t.Errorf("the hint does not name the count and the wing, so nothing distinguishes it "+
			"from the hint on a quiet session:\n%s", hint)
	}

	quiet := statusHint(inboxStatus("wing_agentmemories", 0, nil))
	if quiet == hint {
		t.Error("the hint is identical with and without items waiting — an unconditional " +
			"reminder is one every session learns to skip")
	}
	if strings.Contains(quiet, "wing_agentmemories") {
		t.Errorf("the quiet hint still points at an empty inbox:\n%s", quiet)
	}
}

// TestStatusInboxWithoutADefaultWing: with no registration wing there is no "own
// wing" to count, and reporting 0 would be a claim. Zero and unknown are
// different answers and an agent cannot tell them apart from a bare number.
func TestStatusInboxWithoutADefaultWing(t *testing.T) {
	unknown := inboxStatus("", 0, nil)
	if unknown.Known {
		t.Error("reported a known inbox for a session with no wing to look in")
	}
	if unknown.Note == "" {
		t.Error("an unknown inbox must say why, or it reads as an empty one")
	}
	if h := statusHint(unknown); strings.Contains(h, "inbox") && !strings.Contains(h, "no wing") {
		t.Errorf("the hint sends the agent to an inbox it cannot name:\n%s", h)
	}
}

// TestStatusInboxSurvivesACountingFailure: am_status is the wake-up call. It
// already omits the taxonomy and the workspace block rather than failing when a
// lookup errors, and the inbox count is worth strictly less than either.
func TestStatusInboxSurvivesACountingFailure(t *testing.T) {
	failed := inboxStatus("wing_agentmemories", 0, errCounting)
	if failed.Known {
		t.Error("a failed count reported as a known zero — that is a false all-clear")
	}
	if failed.Note == "" {
		t.Error("a failed count must say so")
	}
}

// TestStatusResponseCarriesTheInbox pins the SELECTION. inboxStatus and
// statusHint can both be correct and never reach the wire: a count computed and
// not marshalled is invisible, and this repo's characteristic defect is exactly
// a component that works and nothing selects.
func TestStatusResponseCarriesTheInbox(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	body := string(src)

	i := strings.Index(body, `"total_drawers":`)
	if i < 0 {
		t.Fatal("the am_status response map has moved — this check has stopped checking anything")
	}
	end := strings.Index(body[i:], "})")
	if end < 0 {
		t.Fatal("could not find the end of the am_status response map")
	}
	resp := body[i : i+end]

	if !strings.Contains(resp, `"inbox"`) {
		t.Error("am_status does not marshal the inbox block — the count is computed and thrown away, " +
			"which is where it already sat for the six handoffs nobody read")
	}
	if !strings.Contains(resp, "statusHint(") {
		t.Error("am_status uses a fixed hint string, so it says the same thing whether or not " +
			"anything is waiting")
	}
	if !strings.Contains(body, "drawers.InboxCount(") {
		t.Error("nothing calls InboxCount, so the inbox block always reports zero")
	}
}
