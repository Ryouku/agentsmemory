package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// TestCollectionNameIsDeterministicAndScoped verifies the tenancy invariant the
// collection-per-tenant design rests on: the same team always maps to the same
// collection, different teams never collide, and the name is a safe slug.
func TestCollectionNameIsDeterministicAndScoped(t *testing.T) {
	teamA := "11111111-1111-1111-1111-111111111111"
	teamB := "22222222-2222-2222-2222-222222222222"

	a1 := CollectionName(teamA)
	a2 := CollectionName(teamA)
	b1 := CollectionName(teamB)

	if a1 != a2 {
		t.Fatalf("non-deterministic: %q != %q", a1, a2)
	}
	if a1 == b1 {
		t.Fatalf("collision: teamA and teamB share collection %q", a1)
	}
	if !strings.HasPrefix(a1, "mempalace_") || !strings.HasSuffix(a1, "_drawers") {
		t.Fatalf("unexpected format: %q", a1)
	}
	// mempalace_(16 hex)_drawers
	if got := len(a1); got != len("mempalace_")+16+len("_drawers") {
		t.Fatalf("unexpected length %d for %q", got, a1)
	}
}

// TestDeleteCollection verifies the drop used by `sync --recreate`: it issues a
// DELETE to the team's collection path and treats both 200 (deleted) and 404
// (already absent) as success, but surfaces other failures.
func TestDeleteCollection(t *testing.T) {
	ctx := context.Background()
	want := "/collections/" + CollectionName("team-x")

	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(status)
		}))
		err := New(srv.URL, "", time.Second).DeleteCollection(ctx, "team-x")
		srv.Close()
		if err != nil {
			t.Fatalf("status %d: DeleteCollection err = %v, want nil (idempotent)", status, err)
		}
		if gotMethod != http.MethodDelete {
			t.Errorf("status %d: method = %s, want DELETE", status, gotMethod)
		}
		if gotPath != want {
			t.Errorf("status %d: path = %s, want %s", status, gotPath, want)
		}
	}

	// A real failure (5xx) must surface as an error, not be swallowed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := New(srv.URL, "", time.Second).DeleteCollection(ctx, "team-x"); err == nil {
		t.Error("status 500: want error, got nil")
	}
}

// TestMatchFilter pins the Qdrant request shape: a must-match clause per key, in
// sorted order so the body is stable, and nil when there is nothing to filter on
// (Qdrant rejects an empty filter object).
func TestMatchFilter(t *testing.T) {
	if got := matchFilter(nil); got != nil {
		t.Errorf("nil filter rendered %v, want nil", got)
	}
	if got := matchFilter(store.Filter{}); got != nil {
		t.Errorf("empty filter rendered %v, want nil", got)
	}

	got, err := json.Marshal(matchFilter(store.Filter{"room": "diary", "wing": "wing_two"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"must":[{"key":"room","match":{"value":"diary"}},{"key":"wing","match":{"value":"wing_two"}}]}`
	if string(got) != want {
		t.Errorf("filter body =\n %s\nwant\n %s", got, want)
	}
}
