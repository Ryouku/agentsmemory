package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// knob is one operator setting and the values the sweep tries for it.
type knob struct {
	name   string
	values []string
	apply  func(config.Config, string) config.Config
}

var sweptKnobs = []knob{
	{"--fusion", []string{"linear", "rrf"}, func(c config.Config, v string) config.Config { c.Fusion = v; return c }},
	{"--bm25-weight", []string{"auto", "auto-idf", "0.0", "0.6"}, func(c config.Config, v string) config.Config { c.BM25Weight = v; return c }},
	{"--closet-boost", []string{"1", "0"}, func(c config.Config, v string) config.Config {
		if v == "0" {
			c.ClosetBoost = 0
		} else {
			c.ClosetBoost = 1
		}
		return c
	}},
	{"--rerank-weight", []string{"0.5", "0", "1"}, func(c config.Config, v string) config.Config {
		switch v {
		case "0":
			c.RerankWeight = 0
		case "1":
			c.RerankWeight = 1
		default:
			c.RerankWeight = 0.5
		}
		return c
	}},
	{"--rerank-pool", []string{"50", "3"}, func(c config.Config, v string) config.Config {
		if v == "3" {
			c.RerankPool = 3
		} else {
			c.RerankPool = 50
		}
		return c
	}},
}

// TestModeScopedKnobsAreDiscovered sweeps the ranking knobs over the real wiring
// and reports which are inert under which mode — computed, never declared.
//
// The predicate is TWO-PART, and that is the whole correctness of it:
//
//  1. K is LIVE AT BASELINE — varying K alone from config.Default() changes the
//     result ordering; and
//  2. K is INERT WHEN D IS SET — with D at a non-default value, varying K over
//     the same range changes nothing.
//
// Only both together mean "D scopes K". The one-part version — cell varies D, K
// does not move the output, therefore D scopes K — was the design an independent
// judge selected, and two adversarial reviewers rejected it for the same reason:
// config.Default() leaves RerankURL empty, so the rerank knobs are inert in
// EVERY cell, and a one-part rule charges that to whichever knob the cell
// happened to vary. Thirteen misattributed cells, one of them the shipped stack.
func TestModeScopedKnobsAreDiscovered(t *testing.T) {
	pairs, inertAtBaseline := sweep(t)
	t.Logf("inert at baseline (attributed to NO other knob): %v", inertAtBaseline)
	t.Logf("mode-scoped pairs discovered (%d):\n  %s", len(pairs), strings.Join(pairs, "\n  "))

	// The known case, discovered rather than asserted into existence.
	want := "--bm25-weight is inert when --fusion=rrf"
	found := false
	for _, p := range pairs {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the sweep did not discover %q. rankRRF takes no weight parameter, so this pair "+
			"exists in the code; a sweep that cannot see it is measuring nothing.\n  found: %v", want, pairs)
	}
}

// TestBaselineInertKnobsAreNotAttributed is the ADR's pre-registered
// falsification, and it asserts on the sweep's OUTPUT rather than on its input.
//
// The first version checked that the rerank knobs do not move the ordering at
// baseline — true, and useless: dropping part 1 of the predicate left that check
// green while the pair list went from 1 to 21. It was testing the fact the rule
// is built on instead of the rule. A knob that cannot move anything at baseline
// must appear in NO pair, because every mode would otherwise look like the thing
// that disabled it.
func TestBaselineInertKnobsAreNotAttributed(t *testing.T) {
	pairs, inertAtBaseline := sweep(t)

	if len(inertAtBaseline) == 0 {
		t.Fatal("no knob is inert at baseline, so this check cannot detect a misattribution — " +
			"config.Default() leaves RerankURL empty and at least the rerank knobs should be here")
	}
	for _, name := range inertAtBaseline {
		for _, p := range pairs {
			if strings.HasPrefix(p, name+" ") {
				t.Errorf("%s is inert at BASELINE and was attributed to another knob anyway: %q\n"+
					"That is the one-part predicate two reviewers rejected: it charges a knob's "+
					"own disabled state to whichever mode the cell happened to vary.", name, p)
			}
		}
	}
}

// sweep computes the mode-scoped pairs and the knobs that cannot move anything
// at baseline. Both tests read it, so neither can pass on a fact the other
// disproves.
func sweep(t *testing.T) (pairs, inertAtBaseline []string) {
	t.Helper()
	base, queries := sweepFixture(t)

	live := map[string]bool{}
	for _, k := range sweptKnobs {
		live[k.name] = knobMoves(t, base, queries, config.Default(), k)
	}

	for _, k := range sweptKnobs {
		if !live[k.name] {
			inertAtBaseline = append(inertAtBaseline, k.name)
			continue
		}
		for _, d := range sweptKnobs {
			if d.name == k.name {
				continue
			}
			for _, dv := range d.values[1:] { // values[0] is the default
				cfg := d.apply(config.Default(), dv)
				if !knobMoves(t, base, queries, cfg, k) {
					pairs = append(pairs, fmt.Sprintf("%s is inert when %s=%s", k.name, d.name, dv))
				}
			}
		}
	}
	sort.Strings(pairs)
	sort.Strings(inertAtBaseline)
	return pairs, inertAtBaseline
}

// knobMoves reports whether varying one knob over its range changes the result
// ordering, holding everything else at cfg.
func knobMoves(t *testing.T, base *palace.Service, queries []string, cfg config.Config, k knob) bool {
	t.Helper()
	var first []string
	for i, v := range k.values {
		// Clone per cell. With* mutates, so reusing one Service would carry each
		// cell's settings into the next and make every knob look inert.
		svc, _ := configureRanking(base.Clone(), k.apply(cfg, v), func(string, time.Duration) palace.Reranker {
			return nil
		})
		got := orderingFor(t, svc, queries)
		if i == 0 {
			first = got
			continue
		}
		if strings.Join(got, "|") != strings.Join(first, "|") {
			return true
		}
	}
	return false
}

func orderingFor(t *testing.T, svc *palace.Service, queries []string) []string {
	t.Helper()
	var out []string
	for _, q := range queries {
		hits, err := svc.Search(context.Background(), "team-sweep", palace.SearchQuery{
			Query: q, Limit: 8, SkipTelemetry: true,
		})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		for _, h := range hits {
			out = append(out, h.Drawer.ID)
		}
		out = append(out, "|")
	}
	return out
}

// sweepFixture seeds a corpus large enough that ranking can differ between
// configurations. Below limit*hybridCandidateMultiplier every cell sees the same
// candidates and no knob can move anything — a sweep over such a corpus reports
// every knob inert and looks like a finding.
func sweepFixture(t *testing.T) (*palace.Service, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sweep.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := palace.NewService(palace.NewRepo(gdb), sweepEmbedder{}, sqlitevec.New(gdb), sweepDim)

	// Deliberate lexical overlap between documents, so BM25 and vector disagree
	// and a lexical weight has something to change.
	texts := []string{
		"the retry budget is three attempts because a fourth exceeds the upstream timeout",
		"the upstream timeout is thirty seconds and is owned by another team",
		"retry backoff is exponential and capped at the retry budget",
		"the queue worker drains in batches and retries with backoff",
		"batch size is tuned to the upstream timeout rather than to throughput",
		"a fourth retry attempt was measured to exceed the timeout in production",
		"the timeout budget is split between connect and read",
		"connect timeouts are retried, read timeouts are not",
		"the worker retries on connect failure and gives up on read failure",
		"throughput is bounded by batch size and by the retry budget together",
		"deploys roll one node at a time to keep the queue drained",
		"a rolling deploy overlaps with the retry window and can double-count",
		"double counting is prevented by an idempotency key on each batch",
		"the idempotency key is derived from the batch contents",
		"batch contents are hashed before the key is derived",
		"hashing is stable across processes so the key is reproducible",
		"reproducible keys let a retried batch be recognised downstream",
		"downstream recognises a duplicate batch and drops it",
		"dropped duplicates are counted but not logged individually",
		"individual logging was removed because it dominated the log volume",
		"log volume is bounded by sampling rather than by level",
		"sampling keeps one in a hundred duplicate events",
		"one in a hundred was chosen to keep the signal visible at low cost",
		"cost here is log storage rather than compute",
	}
	for i, txt := range texts {
		if _, err := svc.Add(context.Background(), "team-sweep", palace.AddInput{
			Wing: "wing_sweep", Room: "decisions", Content: fmt.Sprintf("%s (note %d)", txt, i),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	return svc, []string{
		"why is the retry capped",
		"what bounds throughput",
		"how are duplicate batches handled",
	}
}

const sweepDim = 8

// sweepEmbedder is deterministic and content-derived, so an ordering difference
// between cells comes from the ranking configuration and from nothing else.
type sweepEmbedder struct{}

func (sweepEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, sweepDim)
		for j, r := range s {
			v[j%sweepDim] += float32(r%13) / 12
		}
		out[i] = v
	}
	return out, nil
}

func (e sweepEmbedder) EmbedOne(ctx context.Context, in string) ([]float32, error) {
	v, err := e.Embed(ctx, []string{in})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
