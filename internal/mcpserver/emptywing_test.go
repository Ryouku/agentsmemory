package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestEmptyWingIsDistinguishableFromAMiss: the failure this closes is that both
// look identical — count 0, empty hits, sub-second reply.
func TestEmptyWingIsDistinguishableFromAMiss(t *testing.T) {
	// populated lists the wings that DO hold memories; the one being searched is
	// absent from it, which is the case this closes.
	w := fakeWings{
		populated: map[string]bool{"wing_depozitas": true, "wing_craft": true, "wing_zeus": true},
		names:     []string{"wing_depozitas", "wing_craft", "wing_zeus"},
	}
	note, _ := emptyWingNote(context.Background(), w, "t", "wing_depozitas_laravel")
	if note == "" {
		t.Fatal("a wing holding nothing produced no note — an agent cannot tell a typo from an absence")
	}
	if !strings.Contains(note, "wing_depozitas") {
		t.Errorf("the note does not suggest the near neighbour that DOES hold memories: %q", note)
	}
	if !strings.Contains(note, `wing:"*"`) {
		t.Errorf("the note does not offer the escape hatch that would have answered the query: %q", note)
	}
}

// TestAGenuineMissStaysSilent: a wing that holds memories and simply did not
// match must read exactly as it did before. A warning on every empty page is a
// warning nobody reads.
func TestAGenuineMissStaysSilent(t *testing.T) {
	w := fakeWings{populated: map[string]bool{"wing_craft": true}, names: []string{"wing_craft"}}
	if note, _ := emptyWingNote(context.Background(), w, "t", "wing_craft"); note != "" {
		t.Errorf("a real miss produced a note: %q", note)
	}
}

// TestEmptyWingNoteFailsOpen: a lookup failure must not turn a working page into
// a warning about the palace's own health.
func TestEmptyWingNoteFailsOpen(t *testing.T) {
	w := fakeWings{err: errors.New("db down")}
	if note, _ := emptyWingNote(context.Background(), w, "t", "wing_anything"); note != "" {
		t.Errorf("a failed lookup produced a note: %q", note)
	}
}

// TestNoSuggestionWhenNothingIsClose: a wrong suggestion is worse than none — it
// sends an agent to a wing unrelated to its question.
func TestNoSuggestionWhenNothingIsClose(t *testing.T) {
	if got := nearestWing("wing_zzzzzz", []string{"wing_craft", "wing_infrastructure"}); got != "" {
		t.Errorf("suggested %q for a name sharing nothing with it", got)
	}
	if got := nearestWing("wing_craf", []string{"wing_craft", "wing_zeus"}); got != "wing_craft" {
		t.Errorf("a one-character typo was not matched: got %q", got)
	}
}

// TestStarAndEmptyAreNotWings: "*" means every wing and "" means the
// registration's own; neither is a name that can be empty.
func TestStarAndEmptyAreNotWings(t *testing.T) {
	w := fakeWings{populated: map[string]bool{"wing_craft": true}, names: []string{"wing_craft"}}
	for _, wing := range []string{"*", "", "  "} {
		if note, _ := emptyWingNote(context.Background(), w, "t", wing); note != "" {
			t.Errorf("wing %q produced a note: %q", wing, note)
		}
	}
}
