package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// evalProject parses args through the real eval flag set and returns the
// workspace slug runEval would resolve against, so this pins the actual flag
// wiring rather than a re-implementation of it.
func evalProject(t *testing.T, args ...string) string {
	t.Helper()
	cmd := evalCommand(config.Default())
	var got string
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = c.String("project")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestEvalNamesItsWorkspace is a regression test for eval being the one
// subcommand that could not measure a multi-tenant palace. It resolved through
// EnsureLocalWorkspace, whose contract is "exactly one workspace exists and it is
// slugged local" — so a database holding any other workspace (the seeded demo
// team is enough) failed with ErrForeignWorkspace before a single drawer was
// read, and no flag or environment variable could reach the real corpus.
//
// That guard exists to stop an UNAUTHENTICATED /mcp serving a workspace it did
// not provision. eval is a read-only CLI against a database file the caller
// already possesses, which is the same trust model as `wing export` and
// `inspect` — both of which name their workspace by slug.
func TestEvalNamesItsWorkspace(t *testing.T) {
	if got := evalProject(t, "--project", "acme"); got != "acme" {
		t.Errorf("--project acme resolved to %q, want acme", got)
	}
}

// TestEvalDefaultsToTheLocalWorkspace keeps the self-hoster's zero-typing path:
// naming a workspace must be what a multi-tenant operator does, never a new step
// for someone running `--local`.
func TestEvalDefaultsToTheLocalWorkspace(t *testing.T) {
	if got := evalProject(t); got != tenant.LocalSlug {
		t.Errorf("default project = %q, want %q", got, tenant.LocalSlug)
	}
}

// evalGen parses args through the real eval flag set and reports where the
// question generator would point, plus the model it would ask for.
func evalGen(t *testing.T, args ...string) (url, model string) {
	t.Helper()
	cmd := evalCommand(config.Default())
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		url, model = genURL(c), c.String("gen-model")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return url, model
}

// TestEvalGeneratorFollowsTheEmbedderByDefault pins the single-machine path: one
// Ollama, nothing to configure. --gen-url exists to SEPARATE the two, so leaving
// it unset must not require setting it.
func TestEvalGeneratorFollowsTheEmbedderByDefault(t *testing.T) {
	url, model := evalGen(t, "--ollama-url", "http://box:11434")
	if url != "http://box:11434" {
		t.Errorf("gen url = %q, want it to follow --ollama-url", url)
	}
	if model != "qwen2.5-coder:7b" {
		t.Errorf("gen model = %q, want the documented default", model)
	}
}

// TestEvalGeneratorCanLeaveTheEmbedderBehind is the point of --gen-url: sending a
// one-off burst of question generation to a bigger or hosted model must not drag
// the embedder along with it, because the vectors stay where the data is.
func TestEvalGeneratorCanLeaveTheEmbedderBehind(t *testing.T) {
	url, model := evalGen(t,
		"--ollama-url", "http://localhost:11434",
		"--gen-url", "https://ollama.com",
		"--gen-model", "qwen3-coder:480b-cloud",
	)
	if url != "https://ollama.com" {
		t.Errorf("gen url = %q, want the override to win over --ollama-url", url)
	}
	if model != "qwen3-coder:480b-cloud" {
		t.Errorf("gen model = %q, want the override", model)
	}
}

// TestEvalGeneratorSendsItsBearerToken covers the reason --gen-api-key exists:
// hosted Ollama rejects an unauthenticated call, and a local one ignores the
// header, so sending it whenever it is set is both necessary and harmless.
func TestEvalGeneratorSendsItsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"response":"why did the deploy fail?"}`))
	}))
	defer srv.Close()

	gen := &questionGen{
		url:    srv.URL,
		model:  "m",
		apiKey: "secret-token",
		prompt: "%s",
		http:   srv.Client(),
	}
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}

	// And absent when unset: a local Ollama needs no credential, and inventing an
	// empty Bearer header is the kind of thing a strict proxy rejects.
	gen.apiKey = ""
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask without key: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q with no key set, want it absent", gotAuth)
	}
}

// closetReport is a two-category synthetic run: enough to render the block
// without standing up a palace, an embedder and a generator model.
func closetReport() palace.EvalReport {
	return palace.EvalReport{Details: []palace.EvalCaseResult{
		{Query: "paraphrase one", Category: palace.CatSingle, PoolRank: 3,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 4, palace.ArmHybridCloset: 1}},
		{Query: "paraphrase two", Category: palace.CatSingle, PoolRank: 2,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 1, palace.ArmHybridCloset: 3}},
		{Query: "paraphrase unreachable", Category: palace.CatSingle, PoolRank: 0,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 0, palace.ArmHybridCloset: 0}},
		{Query: "real one", Category: palace.CatReal, PoolRank: 1,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 1, palace.ArmHybridCloset: 4}},
	}}
}

// TestEvalPrintsPreselectedClosetDelta pins that the block says what it is.
//
// A reader who sees a delta next to an interval will assume it was chosen the
// same way the rest of the table's verdicts were — against a best arm picked
// from the same data. This one was named before the run, and the caption has to
// say so, or the number gets quoted with the wrong weight behind it. The
// exclusions are printed for the same reason: a delta over an unstated subset
// is a number nobody can check.
func TestEvalPrintsPreselectedClosetDelta(t *testing.T) {
	var buf strings.Builder
	printClosetBlock(&buf, closetReport())
	got := buf.String()

	for _, want := range []string{
		"hybrid+closet", "hybrid",
		string(palace.CatSingle), string(palace.CatReal),
		"preselected",
		"admitted", "unreachable", "moved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the closet block never mentions %q — a reader cannot tell what went into the number\n%s", want, got)
		}
	}
	// The counts live in columns, so check the row's fields rather than looking
	// for the word next to the number.
	var row []string
	for _, line := range strings.Split(got, "\n") {
		if f := strings.Fields(line); len(f) > 3 && f[0] == string(palace.CatSingle) {
			row = f
		}
	}
	if row == nil {
		t.Fatalf("no %s row in the block:\n%s", palace.CatSingle, got)
	}
	if row[1] != "2" {
		t.Errorf("%s row reports %s admitted, want 2:\n%s", palace.CatSingle, row[1], got)
	}
	if row[2] != "1" {
		t.Errorf("%s row reports %s unreachable, want 1 — the excluded case is invisible:\n%s", palace.CatSingle, row[2], got)
	}
}

// TestRunRecordCarriesProvenanceAndNoCaseText pins both halves of the cells
// file: it records what produced the numbers, and it records nothing that came
// out of the palace.
//
// The evidence directory is committed to a public repository while the palace it
// was measured on is private. A run record that carries queries or drawer ids
// cannot be committed at all, and a run record that does not say which commit,
// which closet scale and which ranking config produced it cannot be compared
// with the next one — which is how two runs of "the same" eval have already
// disagreed for reasons invisible afterwards.
func TestRunRecordCarriesProvenanceAndNoCaseText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.cells.json")
	meta := caseFileMeta{Generator: "qwen2.5-coder:7b", Style: "paraphrase", Wing: "wing_acme", Corpus: 4120}

	if err := writeCells(path, closetReport(), meta, cellsConfig{
		Pool: 50, Cases: 4, ClosetScale: 0, BM25Weight: "0.40",
		RerankConfigured: true, RerankWeight: 0.5, RerankPool: 50,
	}); err != nil {
		t.Fatalf("writeCells: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the cells file is not valid JSON: %v", err)
	}
	for _, key := range []string{"commit", "dirty", "style", "wing", "generator", "corpus_drawers", "cases", "pool", "closet_scale", "bm25_weight", "rerank_configured", "rerank_weight", "rerank_pool", "cells"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("the run record has no %q; two runs of this eval cannot be compared without it", key)
		}
	}
	if cells, ok := rec["cells"].([]any); !ok || len(cells) == 0 {
		t.Error("the run record carries no cells; the evidence directory would hold a file with no evidence in it")
	}

	// Nothing from the palace may appear anywhere in the file.
	text := string(raw)
	for _, leaked := range []string{"paraphrase one", "paraphrase two", "real one", "paraphrase unreachable"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the run record contains the case query %q; this file is committed and the palace is not", leaked)
		}
	}
}

// TestReadCasesKeepsProvenance pins that replaying a case file does not lose the
// record of how its questions were written.
//
// readCases skipped the meta line entirely, so a replayed run knew its own
// --style flag and nothing about the generator that actually produced the
// questions. Two machines running "the same" eval with different generators have
// already disagreed for exactly that reason.
func TestReadCasesKeepsProvenance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")
	want := caseFileMeta{Generator: "qwen2.5-coder:7b", Style: "temporal", Wing: "wing_acme", Corpus: 4120, Created: "2026-08-20T00:00:00Z"}
	if err := writeCases(path, []palace.EvalCase{{Query: "q", Expect: "d1"}}, want); err != nil {
		t.Fatalf("writeCases: %v", err)
	}

	cases, got, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("read %d cases, want 1 — the meta line must not be read as a case", len(cases))
	}
	if got.Generator != want.Generator || got.Style != want.Style || got.Corpus != want.Corpus {
		t.Errorf("provenance lost on replay: got %+v, want generator/style/corpus from %+v", got, want)
	}
}

// judgeSaying stands in for the local model: it replies with the same verdict to
// every prompt, or fails, so verifyPair can be exercised without one.
func judgeSaying(t *testing.T, reply string, fail bool) *questionGen {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "judge is down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":` + strconvQuote(reply) + `}`))
	}))
	t.Cleanup(srv.Close)
	return &questionGen{url: srv.URL, model: "test", prompt: evalPromptPairCheck, http: srv.Client()}
}

func strconvQuote(s string) string { return `"` + s + `"` }

// TestPairVerifiedRejectsDrift pins that a pair the judge does not confirm is
// DROPPED rather than kept.
//
// OlderNeighbor's filters say what a pair is not — not itself, not the same
// source, not newer — and the distance ceiling says the two are close. Neither
// says the older note records an EARLIER STATE of the same fact rather than a
// different fact that happens to be nearby. Only a judge can say that, and a
// pair it declines is not a temporal case: scoring it would measure the ranker
// against a supersession that never happened.
func TestPairVerifiedRejectsDrift(t *testing.T) {
	ctx := context.Background()
	newer := palace.Drawer{Content: "The retention window is ninety days.", ContentDate: "2026-01-01"}
	older := palace.Drawer{Content: "Kubernetes schedules pods using taints.", ContentDate: "2024-01-01"}

	ok, err := verifyPair(ctx, newer, older, judgeSaying(t, "NO — different subjects", false))
	if err != nil {
		t.Fatalf("verifyPair: %v", err)
	}
	if ok {
		t.Error("a pair the judge declined was accepted — the ranker would then be scored against " +
			"a supersession that never happened")
	}

	ok, err = verifyPair(ctx, newer, older, judgeSaying(t, "YES", false))
	if err != nil {
		t.Fatalf("verifyPair: %v", err)
	}
	if !ok {
		t.Error("a pair the judge confirmed was rejected")
	}
}

// TestPairVerifiedJudgeErrorDropsPair pins that a judge that cannot answer drops
// the pair rather than keeping it.
//
// The failure mode this avoids is the one verifyAbsent already learned: an
// unreachable checker that silently returns "fine" fills the case file with
// unverified cases that look identical to verified ones. Unknown is not
// confirmed.
func TestPairVerifiedJudgeErrorDropsPair(t *testing.T) {
	ctx := context.Background()
	newer := palace.Drawer{Content: "a", ContentDate: "2026-01-01"}
	older := palace.Drawer{Content: "b", ContentDate: "2024-01-01"}

	ok, err := verifyPair(ctx, newer, older, judgeSaying(t, "", true))
	if err == nil {
		t.Error("a judge that could not answer returned no error — the caller cannot tell an " +
			"unverified pair from a confirmed one")
	}
	if ok {
		t.Error("a pair was accepted while its check failed")
	}
}

// TestPairVerifiedMetaSurvivesRead pins that how a case file's pairs were made
// survives a replay: how many candidates were considered, how many the judge
// confirmed, and which judge it was.
//
// Without it a replayed temporal run cannot tell a file whose pairs were verified
// from one generated before verification existed, and the two produce different
// numbers from the same command.
func TestPairVerifiedMetaSurvivesRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairs.jsonl")
	want := caseFileMeta{
		Generator: "qwen2.5-coder:7b", Style: "temporal", Wing: "wing_acme",
		PairCandidates: 40, VerifiedPairs: 11, Judge: "qwen2.5-coder:7b",
	}
	if err := writeCases(path, []palace.EvalCase{{Query: "q", Expect: "d1", Distractor: "d0"}}, want); err != nil {
		t.Fatalf("writeCases: %v", err)
	}
	_, got, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if got.PairCandidates != want.PairCandidates || got.VerifiedPairs != want.VerifiedPairs || got.Judge != want.Judge {
		t.Errorf("pair provenance lost on replay: got %+v, want candidates=%d verified=%d judge=%q",
			got, want.PairCandidates, want.VerifiedPairs, want.Judge)
	}
}

// TestSupersessionGateRefusesUnhardenedCases pins that the gate refuses rather
// than answers when its evidence is not what it requires.
//
// Three refusals, each naming its own cause, because "the gate said no" is
// useless if the operator cannot tell a thin corpus from a broken setup. The
// floor is on pairs that are BOTH judge-verified and non-vacuous in THIS run —
// never the generation-time verified_pairs integer, which knows nothing about
// the pool this run used and so counts cases no arm could have ranked.
func TestSupersessionGateRefusesUnhardenedCases(t *testing.T) {
	cell := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 30}

	t.Run("too few usable pairs", func(t *testing.T) {
		thin := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 4, StaleAbove: 3}
		if err := supersessionGateReady(thin, caseFileMeta{VerifiedPairs: 40}); err == nil {
			t.Error("the gate answered on 4 usable pairs — the floor is on pairs verified AND " +
				"non-vacuous in this run, not on what the generator once wrote")
		} else if !strings.Contains(err.Error(), "--pool") && !strings.Contains(err.Error(), "corpus") {
			t.Errorf("the refusal must point at the corpus or the pool, not at the bar: %v", err)
		}
	})

	t.Run("case file was never hardened", func(t *testing.T) {
		if err := supersessionGateReady(cell, caseFileMeta{}); err == nil {
			t.Error("the gate answered on a case file with no verification record")
		} else if !strings.Contains(err.Error(), "--style temporal") {
			// The task said to name --verify-pairs. That flag does not exist:
			// T2 wired pair verification unconditionally into temporal
			// generation, so there is nothing to opt into and naming a flag that
			// is not there is the defect this branch spent itself closing. The
			// refusal names what actually fixes it — regenerating the file.
			t.Errorf("the refusal must name what actually fixes it: %v", err)
		}
	})

	t.Run("hardened and sufficient", func(t *testing.T) {
		if err := supersessionGateReady(cell, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}); err != nil {
			t.Errorf("a hardened file with enough usable pairs must be accepted: %v", err)
		}
	})
}

// TestSupersessionGateIgnoresPageScopedArms pins that the gate reads the arm it
// was pre-registered against and refuses when that arm is absent.
//
// Substituting the nearest available arm is exactly the selection this task
// exists to remove: a degraded run drops the reranked arms, and gating whatever
// is left answers a different question under the same name.
func TestSupersessionGateIgnoresPageScopedArms(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmProduction, Supersession: palace.SupersessionCell{Scope: palace.ScopePage, Cases: 40, StaleAbove: 0}},
		{Arm: palace.ArmHybrid, Supersession: palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 1}},
	}}
	if _, err := gatedArmCell(report); err == nil {
		t.Error("the gate accepted a report without its pre-registered arm — a page-scoped or " +
			"merely-available arm answers a different question under the same name")
	}
	report.Arms = append(report.Arms, palace.EvalMetrics{
		Arm: palace.SupersessionGatedArm(), Supersession: palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 30}})
	got, err := gatedArmCell(report)
	if err != nil {
		t.Fatalf("the gated arm is present and was refused: %v", err)
	}
	if got.StaleAbove != 30 {
		t.Errorf("the gate read the wrong arm: StaleAbove=%d, want 30 (the pre-registered arm's)", got.StaleAbove)
	}
}
