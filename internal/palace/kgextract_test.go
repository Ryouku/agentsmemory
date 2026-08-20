package palace

import (
	"context"
	"strings"
	"testing"
)

// mineSource files enough content under one source to produce at least one
// drawer, so the extraction queries have something real (the migrated schema,
// mine's chunker) to run against.
func mineSource(t *testing.T, svc *Service, team, wing, source, topic string) {
	t.Helper()
	content := "# " + topic + "\n\n" + strings.Repeat("The "+topic+" subsystem stores its state in the shared queue. ", 4)
	if _, err := svc.Mine(context.Background(), team, MineInput{Content: content, Wing: wing, Source: source}); err != nil {
		t.Fatalf("mine %s: %v", source, err)
	}
}

// TestKGReplaceSourceIdempotent is the replace-not-accumulate guarantee: running
// extraction twice over one source must leave only the second run's triples, and
// duplicates within one run's output must collapse to a single row.
func TestKGReplaceSourceIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	mineSource(t, svc, team, "wing_acme", "alpha.md", "cache")

	res, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: "redis"},
		{Subject: "cache", Predicate: "uses", Object: "redis"}, // window overlap repeats facts
		{Subject: "queue", Predicate: "feeds", Object: "worker"},
	})
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if res.Purged != 0 || res.Filed != 2 || res.Rejected != 0 {
		t.Fatalf("first replace should file 2 (duplicate collapsed), purge 0: %+v", res)
	}

	// The re-run supersedes the first wholesale: prior triples purged, only the
	// new set filed.
	res2, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: "memcached"},
	})
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if res2.Purged != 2 || res2.Filed != 1 {
		t.Fatalf("re-run should purge 2 and file 1: %+v", res2)
	}
	facts, _, err := svc.KGQuery(ctx, team, "cache", "", "outgoing")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if findFact(facts, "uses", "redis") != nil {
		t.Fatalf("the first run's triple should be gone after the re-run: %+v", facts)
	}
	if findFact(facts, "uses", "memcached") == nil {
		t.Fatalf("the re-run's triple should be present: %+v", facts)
	}
}

// TestKGReplaceSourceFiledCountsOwnRowsOnly pins the Filed semantics: KGAdd
// dedups across the whole team, so a fact already known from another source
// keeps its original provenance and must not be counted as this source's work.
func TestKGReplaceSourceFiledCountsOwnRowsOnly(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "gateway", Predicate: "routes to", Object: "billing"},
	}); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	res, err := svc.KGReplaceSource(ctx, team, "wing_acme", "beta.md", []ExtractedTriple{
		{Subject: "gateway", Predicate: "routes to", Object: "billing"}, // already alpha's
		{Subject: "beta", Predicate: "owns", Object: "exporter"},
	})
	if err != nil {
		t.Fatalf("replace beta: %v", err)
	}
	if res.Filed != 1 {
		t.Fatalf("only beta's own new row should count as filed, got %d", res.Filed)
	}
}

// TestKGReplaceSourceCountsRejected pins that a triple the sanitizers refuse is
// counted and skipped rather than either failing the whole source or vanishing
// silently — the model wrote it, and the operator needs to see how often.
func TestKGReplaceSourceCountsRejected(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	res, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: strings.Repeat("x", MaxKGValueLen+1)}, // over the value cap
		{Subject: "cache", Predicate: "uses/abuses", Object: "redis"},                       // predicate fails SanitizeName
		{Subject: "queue", Predicate: "feeds", Object: "worker"},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if res.Rejected != 2 || res.Filed != 1 {
		t.Fatalf("expected 2 rejected and 1 filed, got %+v", res)
	}
}

// TestKGExtractSourcesOrdersUnextractedFirst pins what makes --limit runs
// incremental: a source that already has triples sorts after the fresh ones, so
// each run advances into new material instead of re-doing the same batch.
func TestKGExtractSourcesOrdersUnextractedFirst(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	mineSource(t, svc, team, "wing_acme", "alpha.md", "cache")
	mineSource(t, svc, team, "wing_acme", "beta.md", "queue")
	// A third source in another wing must not leak into this wing's listing.
	mineSource(t, svc, team, "wing_beta", "gamma.md", "exporter")

	sources, err := svc.KGExtractSources(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected the wing's 2 sources, got %+v", sources)
	}
	for _, s := range sources {
		if s.Extracted {
			t.Fatalf("nothing extracted yet, but %q is flagged: %+v", s.Source, sources)
		}
		if s.Drawers < 1 {
			t.Fatalf("source %q should report its drawer count, got %d", s.Source, s.Drawers)
		}
	}

	if _, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: "redis"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	sources, err = svc.KGExtractSources(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if sources[0].Source != "beta.md" || sources[0].Extracted {
		t.Fatalf("the unextracted source should sort first: %+v", sources)
	}
	if sources[1].Source != "alpha.md" || !sources[1].Extracted {
		t.Fatalf("the extracted source should sort last and be flagged: %+v", sources)
	}
}

// TestKGSourceTextConcatenatesTheSourcesDrawers confirms the text handed to the
// extraction prompt is the source's own drawers and nobody else's.
func TestKGSourceTextConcatenatesTheSourcesDrawers(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	mineSource(t, svc, team, "wing_acme", "alpha.md", "cache")
	mineSource(t, svc, team, "wing_acme", "beta.md", "queue")

	text, err := svc.KGSourceText(ctx, team, "wing_acme", "alpha.md")
	if err != nil {
		t.Fatalf("source text: %v", err)
	}
	if !strings.Contains(text, "cache subsystem") {
		t.Fatalf("alpha's text should carry its own content, got %q", firstRunes(text, 120))
	}
	if strings.Contains(text, "queue subsystem") {
		t.Fatal("alpha's text must not carry beta's content")
	}
}

// firstRunes truncates for failure messages.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// TestKGReplaceSourceSparesHandFiledFacts pins the provenance firewall the
// review demanded: an agent can hand-file a fact whose source label equals the
// path the extractor uses, and a re-extraction must never purge it — only rows
// the extractor itself wrote (origin sentinel in source_closet) are its to
// replace.
func TestKGReplaceSourceSparesHandFiledFacts(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-handfiled"
	mineSource(t, svc, team, "wing_acme", "alpha.md", "cache")

	// Hand-filed with the SAME source label the extractor will use, plus a
	// temporal window the extractor never regenerates.
	if _, err := svc.KGAdd(ctx, team, "cache", "owned_by", "platform team", "2025-01-01", "", "human-note", "alpha.md", ""); err != nil {
		t.Fatalf("hand-file: %v", err)
	}
	if _, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: "redis"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	res, err := svc.KGReplaceSource(ctx, team, "wing_acme", "alpha.md", []ExtractedTriple{
		{Subject: "cache", Predicate: "uses", Object: "memcached"},
	})
	if err != nil {
		t.Fatalf("re-replace: %v", err)
	}
	if res.Purged != 1 {
		t.Fatalf("re-replace must purge only the extractor's 1 row, purged %d", res.Purged)
	}
	facts, _, err := svc.KGQuery(ctx, team, "cache", "", "")
	if err != nil {
		t.Fatalf("query hand-filed: %v", err)
	}
	survived := false
	for _, f := range facts {
		if f.Predicate == "owned_by" {
			survived = true
		}
	}
	if !survived {
		t.Fatalf("the hand-filed fact must survive extraction re-runs, got %+v", facts)
	}
}
