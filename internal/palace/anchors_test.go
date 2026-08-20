package palace

import (
	"context"
	"testing"
)

// TestAnchorsDieWithTheirDrawer: an anchor that outlives its memory makes verify
// report drift on a sentence nobody can read any more — a warning about nothing,
// which is the fastest way to teach people to ignore warnings.
func TestAnchorsDieWithTheirDrawer(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-anchors"

	drawers := mustAdd(t, svc, team, AddInput{Wing: "wing_x", Room: "decisions", Content: "why the installer pins the config dir"})
	id := drawers[0].ID
	if _, err := svc.AddAnchors(ctx, team, id, []AnchorInput{{Path: "installer.go", Snippet: "func pinConfigDir() bool {"}}); err != nil {
		t.Fatalf("add anchors: %v", err)
	}
	if got, err := svc.ListAnchors(ctx, team, AnchorFilter{}); err != nil || len(got) != 1 {
		t.Fatalf("listed %d anchor(s), err %v; want 1", len(got), err)
	}

	if _, err := svc.Delete(ctx, team, id); err != nil {
		t.Fatalf("delete drawer: %v", err)
	}
	got, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("list anchors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("anchor outlived its drawer: %+v", got)
	}
}

// TestAddAnchorsKeepsExistingVerdicts: re-filing a memory teaches the system
// nothing new about the code, so it must not erase what verification already
// found. Resetting to unchecked would silently clear a drift flag.
func TestAddAnchorsKeepsExistingVerdicts(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-verdicts"

	drawers := mustAdd(t, svc, team, AddInput{Wing: "wing_x", Room: "decisions", Content: "pinned memory"})
	in := []AnchorInput{{Path: "x.go", Snippet: "func target() {}"}}
	if _, err := svc.AddAnchors(ctx, team, drawers[0].ID, in); err != nil {
		t.Fatalf("add anchors: %v", err)
	}
	anchors, _ := svc.ListAnchors(ctx, team, AnchorFilter{})
	if _, err := svc.MarkAnchors(ctx, team, []AnchorVerdict{{ID: anchors[0].ID, Status: AnchorDrifted, Line: 0}}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Same anchor filed again — the verdict must survive.
	if _, err := svc.AddAnchors(ctx, team, drawers[0].ID, in); err != nil {
		t.Fatalf("re-add anchors: %v", err)
	}
	after, _ := svc.ListAnchors(ctx, team, AnchorFilter{})
	if len(after) != 1 {
		t.Fatalf("re-adding duplicated the anchor: %d rows", len(after))
	}
	if after[0].Status != AnchorDrifted || !after[0].Stale() {
		t.Errorf("verdict reset by re-filing: %+v", after[0])
	}
}

// TestMarkAnchorsRejectsUnknownStatus keeps the column a closed set: a typo from
// a client must not become a status nothing knows how to read.
func TestMarkAnchorsRejectsUnknownStatus(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.MarkAnchors(context.Background(), "t", []AnchorVerdict{{ID: "x", Status: "probably-fine"}}); err == nil {
		t.Fatal("want an error for an unknown status")
	}
}

// TestReplaceAnchorsSwapsRatherThanAppends pins the semantics a correction
// needs.
//
// A memory that is corrected keeps its old anchor unless something replaces it —
// and the old anchor pins the OLD text, so the staleness check that exists to
// protect the memory is what marks the correction out of date. Appending would
// leave both live and the dead one still checked. Reported from a live session
// that rewrote a memory and watched it stay flagged STALE.
func TestReplaceAnchorsSwapsRatherThanAppends(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	d := mustAddOne(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about some code"})
	if _, err := svc.AddAnchors(ctx, team, d.ID, []AnchorInput{
		{Path: "internal/old.go", Snippet: "func Old() {}"},
	}); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	n, err := svc.ReplaceAnchors(ctx, team, d.ID, []AnchorInput{
		{Path: "internal/new.go", Snippet: "func New() {}"},
	})
	if err != nil {
		t.Fatalf("ReplaceAnchors: %v", err)
	}
	if n != 1 {
		t.Errorf("replaced %d anchor(s), want 1", n)
	}
	got, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("ListAnchors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the drawer has %d anchor(s) after a replace, want 1 — the old one survived and "+
			"will keep marking the corrected memory stale", len(got))
	}
	if got[0].Path != "internal/new.go" {
		t.Errorf("the surviving anchor is %s, want internal/new.go", got[0].Path)
	}

	// An empty set clears them, which is the honest option when a rewrite no
	// longer points at any particular code.
	if n, err := svc.ReplaceAnchors(ctx, team, d.ID, nil); err != nil || n != 0 {
		t.Fatalf("clearing: n=%d err=%v", n, err)
	}
	if got, err := svc.ListAnchors(ctx, team, AnchorFilter{}); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Errorf("%d anchor(s) survived an empty replace", len(got))
	}
}
