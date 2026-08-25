package mcpserver

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestSearchToolDescriptionSaysBlendedIsPoolRelative: the caveat reaches the
// caller, not just the source.
//
// blended_score is normalised over the POOL — `BlendRerank` min-max scales the
// fused and rerank components across the candidates it scored — so two pages
// cannot be compared on it and an average across pages is meaningless. That is
// exactly the kind of fact a reader assumes the other way round by default.
//
// It is asserted on the TOOL DESCRIPTION rather than on a comment beside the
// field, and the difference is the whole point. A response field carries a Go
// struct tag and a source comment; neither is transmitted, so a caveat written
// there is a caveat the agent never receives. The tool description is a runtime
// string in tools/list, which is what an agent actually reads before deciding
// what a number means. This repository has already been bitten by the other
// shape: an assertion that matched a config file's COMMENTS survived deletion of
// the real key and had been declared mutation-checked.
func TestSearchToolDescriptionSaysBlendedIsPoolRelative(t *testing.T) {
	_, tools := liveSurface(t, false)

	for _, tool := range tools {
		if tool.Name != "am_search" {
			continue
		}
		desc := strings.ToLower(tool.Description)
		if !strings.Contains(desc, "blended_score") {
			t.Fatalf("am_search does not mention blended_score, so the field arrives on every hit with "+
				"nothing telling the agent what it is:\n%s", tool.Description)
		}
		// "pool" is the load-bearing word: it is what makes the number
		// page-local. Accepting a description that merely names the field would
		// let the caveat be dropped while this test stayed green.
		if !strings.Contains(desc, "pool") {
			t.Errorf("am_search names blended_score without saying it is pool-relative; a reader who "+
				"averages it across pages is averaging numbers with different denominators:\n%s",
				tool.Description)
		}
		return
	}
	t.Fatal("am_search is not registered — this test is asserting against a catalogue that cannot " +
		"contain its subject")
}

// TestRenderedHitCarriesTheBlendedValueNotTheRerankScore closes the hole a
// survived mutant found.
//
// blended_score and rerank_score are different numbers with different jobs, and
// on most real pages they agree closely enough that a wrong assignment between
// them is invisible. The domain test pins BlendRerank's ordering; the reflect
// gate pins that the field reaches the wire; neither can see the render site
// putting the wrong value in the right field. Populating Blended from
// RerankScore passed the whole fence until this existed.
func TestRenderedHitCarriesTheBlendedValueNotTheRerankScore(t *testing.T) {
	// Deliberately distinct, and distinct from every other field, so no
	// substitution can pass by coincidence.
	hit := palace.SearchHit{
		MemoryID:    "m-1",
		Score:       0.11,
		BM25:        0.22,
		ClosetBoost: 0.33,
		Distance:    0.44,
		RerankScore: 0.55,
		Reranked:    true,
		Blended:     0.66,
	}

	view := newSearchHitView(hit)

	if view.Blended != hit.Blended {
		t.Errorf("blended_score renders as %v, want %v — the value that decided the order is not the "+
			"value on the wire", view.Blended, hit.Blended)
	}
	if view.Blended == hit.RerankScore {
		t.Error("blended_score renders the cross-encoder score under a different name, which is the " +
			"confusion this field exists to end")
	}
	if view.RerankScore != hit.RerankScore {
		t.Errorf("rerank_score renders as %v, want %v", view.RerankScore, hit.RerankScore)
	}
}
