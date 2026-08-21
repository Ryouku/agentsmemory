package palace

import (
	"context"
	"testing"
)

// TestSearchHonoursTheSelectedLexicalNormaliser: the three anchored normalisers
// were built, tested and measured in the eval, and production could not select
// any of them — Search's four-way ranker switch called the page-max wrappers and
// there was no config key, no flag and no setter. Three transforms, reachable
// from a table and from nothing an operator runs.
//
// This asserts the ORDERING changes, not that a field was assigned. A setter that
// stored a normaliser Search never consulted would satisfy any check that reads
// the field; only a different page proves the selection reached the ranker.
func TestSearchHonoursTheSelectedLexicalNormaliser(t *testing.T) {
	ctx := context.Background()
	const team = "team-lexnorm"

	// The fixture has to make the vector and lexical channels DISAGREE, because
	// all three normalisers are monotone in the raw BM25 scores — they cannot
	// reorder documents lexically, only change how loudly the lexical channel
	// speaks against the vector one. A corpus where lexical order and vector order
	// agree produces the same page under all three, and the first version of this
	// test used one: it reported "the selection is not reaching the ranker" when
	// the ranker was fine and the fixture could not exhibit the difference.
	//
	// The test embedder is a byte histogram (service_test.go:28), so:
	//   vectorTwin — the query's characters rearranged into words that share no
	//                TERM with it: close by vector, zero by BM25.
	//   lexStrong  — the query verbatim, padded with characters the query does not
	//                use: perfect by BM25, pushed away by vector.
	// page-max scales the lexical channel so its best candidate reads 1.0, which
	// puts lexStrong on top. Ceiling and saturating measure that same best against
	// the score the query COULD have attained, leaving it small, and the
	// vector-close twin wins.
	vectorTwin := "aaaa lllt ppmm hhee bbgg dddt aaee"
	lexStrong := "alpha beta gamma delta zqxjvwkyzqxjvwkyzqxjvwky zqxjvwky"
	corpus := []string{vectorTwin, lexStrong, "alpha filler", "beta filler", "unrelated zzz"}

	order := func(norm string) []string {
		svc := newTestService(t)
		if norm != "" {
			svc = svc.WithLexNorm(norm)
		}
		for _, c := range corpus {
			if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: c}); err != nil {
				t.Fatalf("add: %v", err)
			}
		}
		hits, err := svc.Search(ctx, team, SearchQuery{Query: "alpha beta gamma", Limit: 5})
		if err != nil {
			t.Fatalf("search(%s): %v", norm, err)
		}
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = h.Drawer.Content
		}
		return out
	}

	base := order("")
	if len(base) < 3 {
		t.Fatalf("only %d hits — the fixture cannot separate two normalisers", len(base))
	}
	// The fixture is only meaningful if page-max actually puts the lexical match
	// first; if it does not, the corpus has stopped exhibiting the disagreement
	// and a green result below would mean nothing.
	if base[0] != lexStrong {
		t.Fatalf("page-max ranked %.20q first, not the lexically strong document — the fixture no "+
			"longer separates the vector and lexical channels, so this test proves nothing", base[0])
	}

	sameAs := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	differs := 0
	for _, n := range []string{"ceiling", "saturating"} {
		if !sameAs(base, order(n)) {
			differs++
		}
	}
	if differs == 0 {
		t.Error("neither ceiling nor saturating changed the page against page-max — the selected " +
			"normaliser is not reaching the ranker, or the fixture cannot separate them (if the " +
			"latter, this test proves nothing and the fixture must change)")
	}
}

// TestUnknownLexNormKeepsTheDefault: an unrecognised value must not silently rank
// differently. It keeps page-max and says so, the way an unrecognised --fusion
// does.
func TestUnknownLexNormKeepsTheDefault(t *testing.T) {
	svc := newTestService(t).WithLexNorm("nonsense")
	if svc.lexNormName != "page-max" {
		t.Errorf("an unrecognised normaliser resolved to %q; it must keep the default so a typo "+
			"cannot silently change the ranking", svc.lexNormName)
	}
}
