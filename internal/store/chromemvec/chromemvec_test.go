package chromemvec

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// newTestIndex opens an index in a throwaway directory, so every test exercises
// the real persistent path (files on disk) rather than an in-memory shortcut the
// server never uses.
func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := New(filepath.Join(t.TempDir(), "chromem"))
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	return idx
}

// TestSearchFilterNarrowsToPayload proves the wing/room scope is answered by the
// index rather than by the caller: the nearest vector is in the wrong wing, and a
// filtered search must skip past it instead of returning it for the caller to
// discard.
func TestSearchFilterNarrowsToPayload(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_one", "room": "decisions"}},
		{ID: "b", Vector: []float32{0.9, 0.1, 0}, Payload: map[string]any{"wing": "wing_two", "room": "decisions"}},
		{ID: "c", Vector: []float32{0.8, 0.2, 0}, Payload: map[string]any{"wing": "wing_two", "room": "diary"}},
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hits, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 3, store.Filter{"wing": "wing_two"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 || hits[0].ID != "b" || hits[1].ID != "c" {
		t.Fatalf("wing filter: want [b c], got %v", ids(hits))
	}

	// Two keys must both hold, and the payload still round-trips verbatim.
	hits, err = idx.Search(ctx, ns, []float32{1, 0, 0}, 3, store.Filter{"wing": "wing_two", "room": "diary"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "c" {
		t.Fatalf("wing+room filter: want [c], got %v", ids(hits))
	}
	if hits[0].Payload["room"] != "diary" {
		t.Errorf("payload not round-tripped: %v", hits[0].Payload)
	}
}

// ids renders hit ids for a failure message.
func ids(hits []store.Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestUpsertSearchRanking(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"label": "x-axis"}},
		{ID: "b", Vector: []float32{0, 1, 0}},
		{ID: "c", Vector: []float32{0.9, 0.1, 0}}, // close to a
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hits, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 2, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].ID != "a" || hits[1].ID != "c" {
		t.Fatalf("want closest-first [a c], got [%s %s]", hits[0].ID, hits[1].ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores must descend, got %v then %v", hits[0].Score, hits[1].Score)
	}
	// The payload must survive the JSON round-trip through chromem's
	// string-only metadata, and the reserved key must not leak into it.
	if got := hits[0].Payload["label"]; got != "x-axis" {
		t.Fatalf("payload label = %v, want x-axis", got)
	}
	if _, leaked := hits[0].Payload[payloadKey]; leaked {
		t.Fatalf("reserved key %q leaked into the caller payload", payloadKey)
	}
	if hits[1].Payload != nil {
		t.Fatalf("point stored without a payload came back with %v", hits[1].Payload)
	}
}

func TestUpsertReplacesByID(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"v": "old"}}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{0, 1, 0}, Payload: map[string]any{"v": "new"}}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	n, err := idx.Count(ns)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-upserting an ID must replace, not duplicate: count = %d", n)
	}
	hits, err := idx.Search(ctx, ns, []float32{0, 1, 0}, 1, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := hits[0].Payload["v"]; got != "new" {
		t.Fatalf("payload = %v, want the replacement", got)
	}
}

// TestSearchClampsK guards the one place chromem's contract differs from the
// seam's: chromem errors when asked for more results than it holds, while
// store.VectorStore promises a short result slice instead.
func TestSearchClampsK(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	if err := idx.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	hits, err := idx.Search(ctx, ns, []float32{1, 0, 0}, 10, nil)
	if err != nil {
		t.Fatalf("search with k above the point count: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want the 1 stored point, got %d hits", len(hits))
	}
}

func TestSearchEdgeCases(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	// An empty namespace is a legitimate state (a workspace before its first
	// drawer), not an error.
	hits, err := idx.Search(ctx, "empty", []float32{1, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search on an empty namespace: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("want no hits, got %d", len(hits))
	}

	if err := idx.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if hits, err = idx.Search(ctx, "team1", []float32{1, 0, 0}, 0, nil); err != nil || len(hits) != 0 {
		t.Fatalf("k <= 0 must return no hits and no error, got %d hits, err %v", len(hits), err)
	}
}

func TestNamespacesAreIsolated(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	if err := idx.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert team1: %v", err)
	}
	if err := idx.Upsert(ctx, "team2", []store.Point{{ID: "b", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert team2: %v", err)
	}

	hits, err := idx.Search(ctx, "team1", []float32{1, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("tenant isolation broken: %+v", hits)
	}
}

func TestDelete(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}},
		{ID: "b", Vector: []float32{0, 1, 0}},
	}
	if err := idx.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// "missing" is not stored: the seam says unknown IDs are ignored, not an error.
	if err := idx.Delete(ctx, ns, []string{"a", "missing"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := idx.Delete(ctx, ns, nil); err != nil {
		t.Fatalf("delete with no ids must be a no-op: %v", err)
	}

	n, err := idx.Count(ns)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 point left, got %d", n)
	}
}

// TestPersistsAcrossReopen is the property that makes chromem a usable local
// backend: a restart must find the index where it left it, not rebuild from
// scratch.
func TestPersistsAcrossReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chromem")
	ctx := context.Background()
	const ns = "team1"

	first, err := New(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := first.Upsert(ctx, ns, []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"label": "x-axis"}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	second, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	hits, err := second.Search(ctx, ns, []float32{1, 0, 0}, 1, nil)
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("index did not survive the reopen: %+v", hits)
	}
	if got := hits[0].Payload["label"]; got != "x-axis" {
		t.Fatalf("payload after reopen = %v, want x-axis", got)
	}
}
