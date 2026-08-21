// Package storetest holds the behaviour every VectorStore implementation must
// share, as one suite each backend runs against itself.
//
// It exists because this repository's characteristic defect is a capability that
// is finished and unreachable, and a multi-backend seam is where that hides
// best: a method added to an interface compiles for every implementation the
// moment each one has a body, whether or not any body does the thing. A suite
// that only ever runs against the convenient backend passes while another
// returns nil and changes nothing.
package storetest

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// Factory builds a fresh, empty store for one test. t is passed so a backend
// that needs a temp dir or a database can fail the test directly.
type Factory func(t *testing.T) store.VectorStore

// RunPointsConformance exercises PointsByIDs against one backend.
//
// The contract it pins: a payload written by Upsert comes back verbatim, an id
// the store does not hold is OMITTED rather than erroring (matching Delete, so a
// caller need not check existence first), and an empty id list is a no-op.
func RunPointsConformance(t *testing.T, name string, newStore Factory) {
	t.Helper()
	t.Run(name+"/PointsByIDs", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		const ns = "team-conformance"
		if err := s.EnsureNamespace(ctx, ns, 3); err != nil {
			t.Fatalf("EnsureNamespace: %v", err)
		}
		in := []store.Point{
			{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_acme", "room": "decisions"}},
			{ID: "b", Vector: []float32{0, 1, 0}, Payload: map[string]any{"wing": "wing_alpha", "room": "diary"}},
		}
		if err := s.Upsert(ctx, ns, in); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		got, err := s.PointsByIDs(ctx, ns, []string{"a", "b"})
		if err != nil {
			t.Fatalf("Points: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("PointsByIDs returned %d point(s), want 2 — a backend that cannot read a payload back "+
				"cannot be checked for drift, and a drift check that reads nothing reports clean", len(got))
		}
		byID := map[string]store.Point{}
		for _, p := range got {
			byID[p.ID] = p
		}
		for _, want := range in {
			p, ok := byID[want.ID]
			if !ok {
				t.Fatalf("PointsByIDs omitted %q, which was just written", want.ID)
			}
			for k, v := range want.Payload {
				if p.Payload[k] != v {
					t.Errorf("point %q payload[%q] = %v, want %v", want.ID, k, p.Payload[k], v)
				}
			}
			// EXACTLY the keys that were written. A driver that keeps its own
			// bookkeeping in the payload — a reserved id key, a JSON blob of the
			// whole payload beside a flattened copy of it — must hide that here
			// as it already does on Search, or the same point reads differently
			// depending on which method fetched it.
			if len(p.Payload) != len(want.Payload) {
				t.Errorf("point %q came back with payload %v; want exactly the keys written, %v — "+
					"a driver's internal keys must not reach the caller", want.ID, p.Payload, want.Payload)
			}
		}

		// An unknown id is omitted, not an error: the caller is asking what the
		// store holds, and "it holds nothing for this id" is an answer.
		mixed, err := s.PointsByIDs(ctx, ns, []string{"a", "no-such-id"})
		if err != nil {
			t.Fatalf("PointsByIDs with an unknown id: %v", err)
		}
		if len(mixed) != 1 || mixed[0].ID != "a" {
			t.Errorf("PointsByIDs with an unknown id returned %d point(s), want just %q", len(mixed), "a")
		}

		none, err := s.PointsByIDs(ctx, ns, nil)
		if err != nil || len(none) != 0 {
			t.Errorf("PointsByIDs(nil) = %d point(s), %v; want 0, nil", len(none), err)
		}
	})
}
