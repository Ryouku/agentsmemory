package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestEmptyWingIsDistinguishableFromAMiss: the failure this closes is that both
// look identical — count 0, empty hits, sub-second reply.
func TestEmptyWingIsDistinguishableFromAMiss(t *testing.T) {
	// populated lists the wings that DO hold memories; the one being searched is
	// absent from it, which is the case this closes.
	w := fakeWings{
		populated: map[string]bool{"wing_acme": true, "wing_craft": true, "wing_atlas": true},
		names:     []string{"wing_acme", "wing_craft", "wing_atlas"},
	}
	note, _ := emptyWingNote(context.Background(), w, "t", "wing_acme_laravel")
	if note == "" {
		t.Fatal("a wing holding nothing produced no note — an agent cannot tell a typo from an absence")
	}
	if !strings.Contains(note, "wing_acme") {
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
	if got := nearestWing("wing_zzzzzz", []string{"wing_craft", "wing_billing"}); got != "" {
		t.Errorf("suggested %q for a name sharing nothing with it", got)
	}
	if got := nearestWing("wing_craf", []string{"wing_craft", "wing_atlas"}); got != "wing_craft" {
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

// TestEmptyWingNoteIsBoundedAndCountsCharacters covers three defects a review
// found in the diagnostic, all of which fail in the direction of making the
// note worse than no note.
func TestEmptyWingNoteIsBoundedAndCountsCharacters(t *testing.T) {
	t.Run("the note does not spend the page listing wings", func(t *testing.T) {
		many := make([]string, 400)
		for i := range many {
			many[i] = fmt.Sprintf("wing_project%03d", i)
		}
		note, names := emptyWingNote(context.Background(),
			fakeWings{names: many}, "team", "wing_missing")
		if note == "" {
			t.Fatal("an empty wing produced no note")
		}
		if len(names) > maxWingsInNote {
			t.Errorf("the note carried %d wing names on the wire; the cap is %d", len(names), maxWingsInNote)
		}
		if !strings.Contains(note, "+380 more") {
			t.Errorf("the note truncated the list without saying so: %q", note)
		}
		if len(note) > 2000 {
			t.Errorf("the note is %d bytes — a diagnostic that crowds out what it diagnoses", len(note))
		}
	})

	t.Run("one multibyte character is not three characters", func(t *testing.T) {
		// "wing_猫x" vs "wing_猫y": one shared rune past the prefix, three shared
		// BYTES. The byte count cleared the three-character floor and offered a
		// confident suggestion on nothing.
		if got := nearestWing("wing_猫x", []string{"wing_猫y"}); got != "" {
			t.Errorf("suggested %q on a single shared character — the floor is counted in bytes", got)
		}
		// Three real characters still suggest.
		if got := nearestWing("wing_acmee", []string{"wing_acme"}); got != "wing_acme" {
			t.Errorf("three shared characters must still suggest; got %q", got)
		}
	})

	t.Run("an emptied wing is not a wing nobody ever used", func(t *testing.T) {
		note, _ := emptyWingNote(context.Background(),
			fakeWings{names: []string{"wing_acme"}}, "team", "wing_gone")
		if strings.Contains(note, "ever been filed") {
			t.Errorf("the note claims nothing was ever filed there; a wing whose last memory was "+
				"deleted is also empty: %q", note)
		}
	})
}
