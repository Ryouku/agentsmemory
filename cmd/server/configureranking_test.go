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
func TestConfigureRankingEmitsTheSameLines(t *testing.T) {
	cases := []struct {
		name string
		cfg  func(config.Config) config.Config
		want []string
	}{
		{"a default configuration announces nothing", func(c config.Config) config.Config { return c }, nil},
		{"rrf says that the bm25 weight no longer applies",
			func(c config.Config) config.Config { c.Fusion = "rrf"; return c },
			[]string{"fusion: reciprocal-rank"}},
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
