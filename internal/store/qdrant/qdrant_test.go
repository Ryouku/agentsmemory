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

// TestEnsureCollectionIndexesFilterKeys: a filtered search without a payload
// index is answered by SCANNING, which is invisible on a small palace and
// becomes the whole latency budget on a large one. Per-project wings make every
// search filtered, so the index is not an optimisation, it is the feature
// working.
func TestEnsureCollectionIndexesFilterKeys(t *testing.T) {
	var indexed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if schema, _ := body["field_schema"].(string); schema != "keyword" {
				t.Errorf("field_schema = %v, want keyword", body["field_schema"])
			}
			name, _ := body["field_name"].(string)
			indexed = append(indexed, name)
		case r.Method == http.MethodGet:
			// collectionExists: report absent so the create path runs too.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":true,"status":"ok"}`))
	}))
	defer srv.Close()

	if err := New(srv.URL, "", time.Second).EnsureCollection(context.Background(), "team-1", 1024); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(indexed) != 2 || indexed[0] != "wing" || indexed[1] != "room" {
		t.Errorf("indexed %v, want [wing room]", indexed)
	}
}

// TestEnsureCollectionIndexesAnExistingCollection: a palace created before the
// index existed must get one on the next boot, not stay slow forever with
// nothing to say why.
func TestEnsureCollectionIndexesAnExistingCollection(t *testing.T) {
	var indexed int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/index") {
			indexed++
		}
		if r.Method == http.MethodPut && !strings.HasSuffix(r.URL.Path, "/index") {
			t.Error("an existing collection must not be re-created")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"status":"green"},"status":"ok"}`))
	}))
	defer srv.Close()

	if err := New(srv.URL, "", time.Second).EnsureCollection(context.Background(), "team-1", 1024); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if indexed != 2 {
		t.Errorf("indexed %d key(s) on an existing collection, want 2", indexed)
	}
}
