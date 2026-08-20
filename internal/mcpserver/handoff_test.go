package mcpserver

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// fakeWings answers the emptiness question without a database, so these tests
// drive the DECISION rather than the storage underneath it.
type fakeWings struct {
	populated map[string]bool
	names     []string
	err       error
}

func (f fakeWings) WingIsEmpty(_ context.Context, _, wing string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return !f.populated[wing], nil
}

func (f fakeWings) WingNames(_ context.Context, _ string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.names, nil
}

func palaceWith(wings ...string) fakeWings {
	f := fakeWings{populated: map[string]bool{}, names: wings}
	for _, w := range wings {
		f.populated[w] = true
	}
	return f
}

// TestHandoffIntoAnUnresolvableWingIsRefused.
//
// Measured 2026-08-20 on a 217-drawer palace: two sessions filed six drawers of
// real findings into wings named for the DIRECTION of travel (wing_to-<project>)
// rather than the project. Step 0c's rungs can only ever produce wing_<project>,
// so no session will look there and the handoffs were undeliverable.
//
// The discriminator is "the wing holds nothing AND the room is inbox". Nobody's
// first act in a palace is filing an inbox item to themselves: across all 217
// drawers, every legitimate wing's first write was decisions or diary, and the
// only two whose first write was inbox are the two malformed ones.
func TestHandoffIntoAnUnresolvableWingIsRefused(t *testing.T) {
	p := palaceWith("wing_craft", "wing_agentmemories")

	refusal := handoffRefusal(context.Background(), p, "team", "wing_to-someproject", "inbox", false)
	if refusal == "" {
		t.Fatal("filed into an empty wing from the inbox room without a word — that is exactly " +
			"how six drawers became unreachable")
	}
	for _, want := range []string{"confirm_new_wing", "wing_craft", "wing_agentmemories"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal does not mention %q — the filer needs the way forward and the "+
				"wings that exist, or the next attempt is another guess:\n%s", want, refusal)
		}
	}
}

// TestHandoffRefusalCanBeOverridden pins the three ways a write must still go
// through. Handing off to a project that genuinely has no wing yet is legitimate;
// so is any first write that is not an inbox item, which is how every wing in a
// fresh palace comes into existence.
func TestHandoffRefusalCanBeOverridden(t *testing.T) {
	p := palaceWith("wing_craft")

	if r := handoffRefusal(context.Background(), p, "team", "wing_new", "inbox", true); r != "" {
		t.Errorf("confirm_new_wing did not proceed: %s", r)
	}
	if r := handoffRefusal(context.Background(), p, "team", "wing_new", "decisions", false); r != "" {
		t.Errorf("a first write to a new wing in any other room must be free — a wing comes into "+
			"existence when something is written to it, and a fresh install has none: %s", r)
	}
	if r := handoffRefusal(context.Background(), p, "team", "wing_craft", "inbox", false); r != "" {
		t.Errorf("an inbox item into a wing that already holds drawers is the convention working: %s", r)
	}
}

// TestHandoffCheckFailsOpen: the check is a guard on a mistake, not a gate on
// storage. If the palace cannot answer whether the wing is empty, the write must
// still land — refusing would turn a database hiccup into lost work, and the
// convention this protects is not worth that.
func TestHandoffCheckFailsOpen(t *testing.T) {
	broken := fakeWings{err: errors.New("db down")}
	if r := handoffRefusal(context.Background(), broken, "team", "wing_new", "inbox", false); r != "" {
		t.Errorf("a failed emptiness check blocked a write: %s", r)
	}
}

// TestAddPathConsultsTheHandoffCheck pins the SELECTION. handoffRefusal can be
// correct and unreached: this package has already shipped a refusal that the
// destructive path did not call, and a source-grep check that only looked for
// the guard's text passed against that guard disarmed with "&& false". So the
// behaviour above lives in a function a test can drive, and this answers only
// the one question reading can: is it called, and is its answer honoured.
func TestAddPathConsultsTheHandoffCheck(t *testing.T) {
	src, err := os.ReadFile("drawers.go")
	if err != nil {
		t.Fatalf("read drawers.go: %v", err)
	}
	body := string(src)

	i := strings.Index(body, "drawers.Add(")
	if i < 0 {
		t.Fatal("no Add call in drawers.go — this check has stopped checking anything")
	}
	window := body[:i]
	if !strings.Contains(window, "handoffRefusal(") {
		t.Error("the add path never consults handoffRefusal — the refusal is tested and unreached, " +
			"and an undeliverable handoff files silently")
	}
	if !strings.Contains(window, "confirm_new_wing") {
		t.Error("the add_drawer tool does not declare confirm_new_wing, so the refusal names an " +
			"argument callers cannot pass")
	}
}
