package qdrant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
)

// fakeQdrant is a minimal Qdrant that stores what it is sent and returns it,
// faithful to the two request shapes this client uses: PUT .../points to write
// and POST .../points with an id list to read back.
//
// It is deliberately not a stub that returns a canned answer. What can be wrong
// in an HTTP driver is the mapping — the path, the body shape, the reserved id
// key, the direction of the payload — and a canned response tests none of that.
// This one round-trips through the real serialization in both directions.
func fakeQdrant(t *testing.T) *httptest.Server {
	t.Helper()
	stored := map[string]map[string]any{} // point uuid -> payload

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(strings.SplitN(r.URL.Path, "?", 2)[0], "/points"):
			var body struct {
				Points []struct {
					ID      string         `json:"id"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, p := range body.Points {
				stored[p.ID] = p.Payload
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points"):
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			type res struct {
				ID      string         `json:"id"`
				Payload map[string]any `json:"payload"`
			}
			out := struct {
				Result []res `json:"result"`
			}{}
			for _, id := range body.IDs {
				if pl, ok := stored[id]; ok { // an id it does not hold is simply absent
					out.Result = append(out.Result, res{ID: id, Payload: pl})
				}
			}
			_ = json.NewEncoder(w).Encode(out)

		default: // collection creation and anything else
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
}

// TestQdrantRunsTheConformanceSuite drives the real client, through real HTTP,
// against a store that keeps what it is given.
func TestQdrantRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "qdrant", func(t *testing.T) store.VectorStore {
		srv := fakeQdrant(t)
		t.Cleanup(srv.Close)
		return New(srv.URL, "", 10*time.Second)
	})
}
