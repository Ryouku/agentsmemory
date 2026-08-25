package mcptest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// pageOf decodes a search response envelope, including the page-level id.
//
// Deliberately separate from regions_test.go's hitsOf: that helper fails when a
// page has no hits, which is correct for assertions ABOUT hits and wrong here.
// A recall that found nothing is still a recall, it still wrote a row, and
// whether it names itself is exactly what this file is about.
func pageOf(t *testing.T, out string) (string, []map[string]any) {
	t.Helper()
	var page struct {
		Count    int              `json:"count"`
		SearchID string           `json:"search_id"`
		Hits     []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("search response is not the JSON an agent parses: %v\n%s", err, out)
	}
	return page.SearchID, page.Hits
}

// TestSearchResponseCarriesItsSearchID: a page names the recall it came from.
//
// The id is not new. `Search` has minted one per call for as long as the spans
// have existed, puts it on every stage span, and stores it as the PRIMARY KEY of
// the `search_events` row — so the join from a page to its durable record has
// been one field away the whole time. What was missing is the only half that
// leaves the process: the string appeared nowhere in internal/mcpserver, so no
// caller could name the recall it had just run.
//
// The empty-page case is asserted alongside the populated one because it is the
// one that would have been lost by the cheap implementation. Hanging the id off
// each hit would satisfy every ordinary test and return nothing at all for a
// search that found nothing — which is precisely the recall an operator most
// wants to trace.
func TestSearchResponseCarriesItsSearchID(t *testing.T) {
	h := mcptest.New(t)

	h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_searchid", "room": "decisions",
		"content": "the recall identifier is minted once per search and stored as the event row's key",
	})

	id, hits := pageOf(t, h.MustCall(t, "am_search", map[string]any{
		"query": "recall identifier minted once per search", "wing": "wing_searchid", "limit": 5,
	}))
	if len(hits) == 0 {
		t.Fatal("the populated search returned no hits, so this case proves nothing about a page that found something")
	}
	if strings.TrimSpace(id) == "" {
		t.Errorf("a page that found %d memories does not name the recall that produced it; "+
			"without it no fetch can say which search sent it", len(hits))
	}

	emptyID, emptyHits := pageOf(t, h.MustCall(t, "am_search", map[string]any{
		"query": "nothing in this palace resembles this query at all", "wing": "wing_searchid_absent", "limit": 5,
	}))
	if len(emptyHits) != 0 {
		t.Fatalf("expected an empty page for an absent wing, got %d hits — the assertion below is about the empty case", len(emptyHits))
	}
	if strings.TrimSpace(emptyID) == "" {
		t.Error("a recall that found nothing does not name itself, so the one page an operator " +
			"most wants to trace is the one that cannot be traced")
	}
	if emptyID == id {
		t.Error("two searches share one id — the identifier is not per-recall, and every future join would " +
			"merge unrelated pages")
	}
}

// TestGetDrawerSchemaAdvertisesSearchID: the argument is DESCRIBED, not merely
// accepted.
//
// TestEveryArgumentAHandlerReadsIsDeclared already fails when a handler reads an
// argument the tool never advertises, which is the harder half of rung 3 and was
// in place before this task. This is the narrower half it does not check: an
// argument declared with no description is discoverable and unexplained, and an
// agent that cannot tell what to put in a field does not fill it.
func TestGetDrawerSchemaAdvertisesSearchID(t *testing.T) {
	h := mcptest.New(t)

	tools, err := h.ListToolDefinitions(t)
	if err != nil {
		t.Fatalf("list tool definitions: %v", err)
	}
	for _, tool := range tools {
		if tool.Name != "am_get_drawer" {
			continue
		}
		prop, ok := tool.InputSchema.Properties["search_id"]
		if !ok {
			t.Fatalf("am_get_drawer does not advertise search_id, so an agent reading the schema "+
				"never sends it — the capability would work only for a caller who guessed. properties: %v",
				keysOf(tool.InputSchema.Properties))
		}
		desc, _ := prop.(map[string]any)["description"].(string)
		if strings.TrimSpace(desc) == "" {
			t.Error("search_id is advertised with no description; an argument nobody can interpret " +
				"is advertised and still unusable")
		}
		return
	}
	t.Fatal("am_get_drawer is not registered at all — this test is asserting against a catalogue that " +
		"cannot contain its subject")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestGetDrawerIgnoresAnUnknownSearchID: until the recording task lands, the
// argument changes nothing.
//
// Written now rather than with that task because the invariant is this task's:
// an id that does not resolve must not fail the fetch. A memory an agent can
// read only when it quotes a valid recall id would be a regression introduced by
// a field that does nothing yet.
func TestGetDrawerIgnoresAnUnknownSearchID(t *testing.T) {
	h := mcptest.New(t)

	h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_searchid_fetch", "room": "decisions",
		"content": "a fetch quoting an unknown recall identifier still returns the memory it asked for",
	})
	_, hits := pageOf(t, h.MustCall(t, "am_search", map[string]any{
		"query": "fetch quoting an unknown recall identifier", "wing": "wing_searchid_fetch", "limit": 1,
	}))
	if len(hits) == 0 {
		t.Fatal("no hit to fetch, so the assertion below would be vacuous")
	}
	id, _ := hits[0]["id"].(string)
	if id == "" {
		t.Fatalf("hit carries no id to fetch: %v", keysOf(hits[0]))
	}

	got := h.MustCall(t, "am_get_drawer", map[string]any{
		"id": id, "search_id": "cafebabe-not-a-recall-that-ever-ran",
	})
	if !strings.Contains(got, "unknown recall identifier") {
		t.Errorf("a fetch quoting an unrecognised search_id did not return the memory; the argument is "+
			"supposed to be inert until it is recorded, not a precondition:\n%s", got)
	}
}
