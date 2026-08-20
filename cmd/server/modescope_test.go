package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// TestDiscoveredPairsAdmitTheirCondition closes the loop T2 opens: a pair the
// sweep discovers is a knob that silently does nothing under some mode, and the
// only fix an operator can act on is being told so where they read.
//
// The admission must name the gating knob GREPPABLY. That is deliberate
// strictness: prose counts here only when it is mechanically checkable, which is
// the same line this repository already holds for environment variables. An
// honest sentence phrased without the gating knob's name fails, and that is the
// trade — the alternative is a check that accepts text it cannot verify.
func TestDiscoveredPairsAdmitTheirCondition(t *testing.T) {
	pairs, _ := sweep(t)
	if len(pairs) == 0 {
		t.Fatal("the sweep discovered no pairs, so this check has nothing to enforce — " +
			"--bm25-weight under --fusion=rrf exists in the code and should have been found")
	}

	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	cfgSrc, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}

	for _, p := range pairs {
		// "--bm25-weight is inert when --fusion=rrf"
		knobName, gate := parsePair(t, p)
		usage := flagUsage(t, string(src), knobName)
		if usage == "" {
			t.Errorf("no Usage string found for %s", knobName)
			continue
		}
		if !mentions(usage, gate) && !mentions(string(cfgSrc), gate+" "+knobName) {
			t.Errorf("%s does nothing when %s is set, and its --help says nothing about it:\n  %q\n"+
				"  An operator who sets it gets no behaviour change and no explanation. Naming the "+
				"gating knob is the whole remedy.", knobName, gate, usage)
		}
	}
}

// TestStartupDoesNotContradictItself: under rrf the wiring announces that the
// bm25 weight does not apply, and then announced the bm25 weight anyway. Two
// lines, adjacent, disagreeing — a reader believes whichever they read second.
func TestStartupDoesNotContradictItself(t *testing.T) {
	cfg := config.Default()
	cfg.Fusion = "rrf"
	cfg.BM25Weight = "auto-idf"

	_, lines := configureRanking(bareService(), cfg, noReranker)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "does not apply") {
		t.Fatalf("rrf did not announce that the bm25 weight is inert:\n%s", joined)
	}
	if strings.Contains(joined, "bm25 weight: auto (IDF-weighted coverage)") {
		t.Errorf("startup says the bm25 weight does not apply and then reports one:\n%s\n"+
			"A reader believes whichever line they read second.", joined)
	}
}

// parsePair splits "--knob is inert when --gate=value" into its two flag names.
func parsePair(t *testing.T, pair string) (knob, gate string) {
	t.Helper()
	parts := strings.SplitN(pair, " is inert when ", 2)
	if len(parts) != 2 {
		t.Fatalf("unparseable pair %q", pair)
	}
	gate = parts[1]
	if i := strings.Index(gate, "="); i >= 0 {
		gate = gate[:i]
	}
	return parts[0], gate
}

// flagUsage pulls a flag's Usage text out of the command wiring.
func flagUsage(t *testing.T, src, flag string) string {
	t.Helper()
	name := strings.TrimPrefix(flag, "--")
	i := strings.Index(src, `Name: "`+name+`"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(src[i:], "Usage: ")
	if j < 0 {
		return ""
	}
	rest := src[i+j:]
	if k := strings.Index(rest, "},\n"); k >= 0 {
		rest = rest[:k]
	}
	return rest
}

// mentions accepts the flag spelling or the environment spelling, since an
// operator may know a knob by either.
func mentions(text, flag string) bool {
	name := strings.TrimPrefix(flag, "--")
	env := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return strings.Contains(text, "--"+name) || strings.Contains(text, env)
}

// unobservableKnobs are the ranking settings the sweep cannot move, each with
// the reason it is silent. The list exists so that silence is DECLARED: a knob
// absent from both the sweep and this map fails TestEveryKnobIsSweptOrNamed
// rather than passing unnoticed, which is how a setting joins the wiring and
// nobody ever measures whether it does anything.
var unobservableKnobs = map[string]string{
	"RerankURL":     "selects a live cross-encoder service; with none reachable the sweep measures the absence, not the setting",
	"RerankTimeout": "bounds a call the sweep never makes, so every value produces the same ordering in-process",
	"HTTPTimeout":   "bounds outbound calls to the vector and embedding backends, which the fixture serves locally",
}

// TestEveryKnobIsSweptOrNamed: every config field the ranking wiring READS is
// either swept for mode-scoping or listed as unobservable with a reason.
//
// The universe is read out of configureRanking's own body rather than declared
// beside it, because a list kept beside the truth is a thing somebody has to
// remember. A field that joins the wiring joins this check on the same commit.
//
// The mapping from knob to field is EXECUTED, not asserted: each knob's apply
// runs against a zero Config and the fields it moves are the fields it owns. A
// knob whose Usage claims one setting while its apply moves another cannot pass
// by naming itself correctly.
func TestEveryKnobIsSweptOrNamed(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	universe := fieldsReadBy(t, string(src), "configureRanking")
	if len(universe) == 0 {
		t.Fatal("no cfg.* reads found in configureRanking — the extraction that this " +
			"check reads has moved, so it is measuring nothing")
	}

	covered := map[string]string{}
	for _, k := range sweptKnobs {
		for _, f := range fieldsMovedBy(k) {
			covered[f] = "swept as " + k.name
		}
	}
	for f, why := range unobservableKnobs {
		if why == "" {
			t.Errorf("%s is listed as unobservable with no reason; the list is a declaration, not a mute button", f)
		}
		if prior, dup := covered[f]; dup {
			t.Errorf("%s is both %s and listed as unobservable — one of the two is wrong", f, prior)
		}
		covered[f] = "declared unobservable"
	}

	for _, f := range universe {
		if _, ok := covered[f]; !ok {
			t.Errorf("configureRanking reads cfg.%s, but no knob sweeps it and nothing declares it "+
				"unobservable.\n  An operator can set it and nobody has measured whether it changes "+
				"anything. Add it to sweptKnobs, or to unobservableKnobs with the reason the sweep "+
				"is silent.", f)
		}
	}

	inUniverse := map[string]bool{}
	for _, f := range universe {
		inUniverse[f] = true
	}
	for f := range unobservableKnobs {
		if !inUniverse[f] {
			t.Errorf("%s is declared unobservable but the ranking wiring no longer reads it; "+
				"a stale excuse outlives the thing it excused", f)
		}
	}
}

// fieldsReadBy returns the config.Config field names a named function reads,
// scanned out of its source body.
func fieldsReadBy(t *testing.T, src, fn string) []string {
	t.Helper()
	i := strings.Index(src, "\nfunc "+fn+"(")
	if i < 0 {
		return nil
	}
	body := src[i:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j]
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(body, "cfg.")[1:] {
		end := 0
		for end < len(part) && (isIdentRune(part[end])) {
			end++
		}
		name := part[:end]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func isIdentRune(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// fieldsMovedBy runs a knob's apply over every value it sweeps and reports which
// config fields actually changed. Running it is the point: a knob is bound to the
// field it MOVES, not to the field its name suggests.
func fieldsMovedBy(k knob) []string {
	var zero config.Config
	seen := map[string]bool{}
	var out []string
	rz := reflect.ValueOf(zero)
	for _, v := range k.values {
		rv := reflect.ValueOf(k.apply(zero, v))
		for i := 0; i < rv.NumField(); i++ {
			name := rv.Type().Field(i).Name
			if seen[name] {
				continue
			}
			if !reflect.DeepEqual(rv.Field(i).Interface(), rz.Field(i).Interface()) {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}
