package main

import (
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestConfigureRankingEmitsTheSameLines pins the extraction as behaviour-
// preserving.
//
// The startup lines are the only observable of this wiring — each setter emits
// one — so comparing them is how a MOVE is distinguished from a CHANGE. The
// extraction exists so ADR-006 T2 can drive flag values through to behaviour
// without standing a server up; a block only reachable from newServices cannot
// be swept.
// TestConfigureRankingEmitsTheSameLines pins the delta lines AND the resolved
// profile. The "a default configuration announces nothing" case used to assert
// zero lines, which made a silent startup ambiguous: it meant both "everything is
// default" and "an operator set values that happen to equal the defaults". The
// profile line removes the ambiguity, so a deployment can be matched against the
// eval row that describes it.
func TestConfigureRankingEmitsTheSameLines(t *testing.T) {
	cases := []struct {
		name string
		cfg  func(config.Config) config.Config
		want []string
	}{
		{"a default configuration still announces the profile it resolved",
			func(c config.Config) config.Config { return c },
			[]string{"ranking: fusion=linear lex-weight=auto lex-norm=page-max closet-boost=1.00 rerank=off"}},
		{"rrf says that the bm25 weight no longer applies, and the profile agrees",
			func(c config.Config) config.Config { c.Fusion = "rrf"; return c },
			[]string{"fusion: reciprocal-rank", "ranking: fusion=rrf"}},
		{"the profile reports the normaliser actually in force",
			func(c config.Config) config.Config { c.LexNorm = "ceiling"; return c },
			[]string{"lex-norm=ceiling"}},
		{"the profile reports a fixed lexical weight as the number, not as auto",
			func(c config.Config) config.Config { c.BM25Weight = "0.25"; return c },
			[]string{"lex-weight=0.25"}},
		{"the profile reports a scaled closet prior",
			func(c config.Config) config.Config { c.ClosetBoost = 0; return c },
			[]string{"closet-boost=0.00"}},
		{"a fusion typo is reported, not silently ignored",
			func(c config.Config) config.Config { c.Fusion = "rff"; return c },
			[]string{"is not 'linear' or 'rrf'"}},
		{"a fixed bm25 weight is announced",
			func(c config.Config) config.Config { c.BM25Weight = "0.25"; return c },
			[]string{"bm25 weight: fixed"}},
		{"an unparseable bm25 weight is reported",
			func(c config.Config) config.Config { c.BM25Weight = "heavy"; return c },
			[]string{"is not 'auto', 'auto-idf' or a number"}},
		{"a scaled closet boost is announced",
			func(c config.Config) config.Config { c.ClosetBoost = 0; return c },
			[]string{"closet boost: scaled to 0.00"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, lines := configureRanking(bareService(), tc.cfg(config.Default()), noReranker)
			joined := strings.Join(lines, "\n")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("startup did not report %q:\n%s", w, joined)
				}
			}
			if tc.want == nil && len(lines) != 0 {
				t.Errorf("a default configuration announced %d line(s):\n%s", len(lines), joined)
			}
		})
	}
}

// TestConfigureRankingHonoursTheRerankURLGuard: with no rerank URL the factory
// must not be called at all. That guard is what makes three rerank knobs inert
// at baseline, and ADR-006 T2's two-part predicate depends on it being exactly
// here — an adversarial review found the one-part version misattributing 13
// cells because of it.
func TestConfigureRankingHonoursTheRerankURLGuard(t *testing.T) {
	called := 0
	factory := func(string, time.Duration) palace.Reranker { called++; return nil }

	if _, _ = configureRanking(bareService(), config.Default(), factory); called != 0 {
		t.Errorf("the reranker factory was called %d time(s) with no rerank URL configured", called)
	}

	cfg := config.Default()
	cfg.RerankURL = "http://reranker.invalid/v1"
	if _, lines := configureRanking(bareService(), cfg, factory); called != 1 {
		t.Errorf("the factory was called %d time(s) with a rerank URL set; lines=%v", called, lines)
	}
}

// bareService is a Service with no backends. The With* setters only assign
// fields, so this is enough to observe which ones ran — and it keeps the
// extraction test free of a database, which is the point of extracting it.
func bareService() *palace.Service { return palace.NewService(nil, nil, nil, 0) }

func noReranker(string, time.Duration) palace.Reranker { return nil }

// TestRerankSurvivesEveryFusionMode: the reranker is wired whatever the fusion
// mode is, because the two compose — Search runs fusion first and reranks the
// fused order, and the eval carries an explicit rrf+rerank arm an operator reads
// before choosing this configuration.
//
// This test exists because the fix for a startup contradiction created exactly
// the defect this repository is trying to stop shipping. Under --fusion=rrf the
// bm25 weight is inert, so the wiring returned early to stop announcing one —
// and took the reranker block with it. A reviewer found it; no gate did. The
// gate that discovered the inert pair could not: it varies knobs and watches the
// RESULT ORDER, and a fixture with no reranker to lose sees nothing go missing.
//
// What this observes is the factory CALL, plus the line the same block emits.
// It does NOT observe the pool and weight arriving on the Service: those are
// unexported and palace exposes no accessor, so a WithReranker that dropped its
// arguments would pass here. Said plainly rather than papered over — the
// reachability question ("did the wiring reach the reranker at all") is the one
// that shipped broken, and it is the one this answers.
func TestRerankSurvivesEveryFusionMode(t *testing.T) {
	for _, fusion := range []string{"linear", "rrf", "RRF", ""} {
		called := 0
		factory := func(string, time.Duration) palace.Reranker { called++; return nil }

		cfg := config.Default()
		cfg.Fusion = fusion
		cfg.RerankURL = "http://reranker.invalid/v1"
		cfg.RerankPool = 64
		cfg.RerankWeight = 0.25

		_, lines := configureRanking(bareService(), cfg, factory)

		if called != 1 {
			t.Errorf("fusion=%q: the reranker factory was called %d time(s) with a rerank URL set; lines=%v",
				fusion, called, lines)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "pool 64") || !strings.Contains(joined, "weight 0.25") {
			t.Errorf("fusion=%q: the reranker block did not report the configured pool and weight:\n%s",
				fusion, joined)
		}
	}
}

// TestConfigureRankingAppliesTheLexicalNormaliser: the composition root must
// carry the operator's choice into the service, and only driving it proves that.
//
// Unwiring the assignment survived every other test in this package: the AST
// check that says "configureRanking reads cfg.LexNorm" still sees the read when
// the branch is dead, and the palace tests drive WithLexNorm directly. A field
// that is read and discarded looks identical to one that is used, from the
// outside and from the source.
func TestConfigureRankingAppliesTheLexicalNormaliser(t *testing.T) {
	for _, tc := range []struct{ set, want string }{
		{"", palace.DefaultLexNorm},
		{"page-max", palace.DefaultLexNorm},
		{"ceiling", "ceiling"},
		{"saturating", "saturating"},
		{"nonsense", palace.DefaultLexNorm},
	} {
		cfg := config.Default()
		cfg.LexNorm = tc.set
		svc, lines := configureRanking(bareService(), cfg, noReranker)
		if got := svc.LexNormName(); got != tc.want {
			t.Errorf("--lex-norm=%q produced %q, want %q; lines=%v", tc.set, got, tc.want, lines)
		}
	}
}
