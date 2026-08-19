// eval.go implements `agentsmemory eval`: does recall actually return the memory
// that answers the question, and does each ranking stage earn its place?
//
// The corpus is the team's own memory, and the questions are generated from it —
// for each sampled drawer, a local model writes a question that drawer answers,
// and retrieval is scored on whether it comes back. That makes labelling free,
// which is the only reason an eval like this gets run more than once.
//
// Two honesty constraints shape it:
//
//   - The generator is told to PARAPHRASE and avoid the drawer's distinctive
//     terms. Without that, the questions inherit the drawer's vocabulary, BM25
//     scores its own homework, and every arm looks excellent.
//   - Absolute numbers from generated questions mean little. The DELTAS between
//     arms are the output worth acting on, which is why the table is the point
//     and a single headline number is not offered.
//
// Generated cases are written to (and re-read from) a file so a ranking change
// can be compared against the same questions rather than a fresh sample.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// evalCommand builds the `eval` subcommand.
func evalCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "eval",
		Usage: "measure retrieval quality and compare ranking arms on this palace",
		Description: "Samples drawers, has a local model write a question each one answers, then\n" +
			"scores every ranking arm on whether that drawer comes back:\n\n" +
			"  vector                 nearest neighbour only — the baseline to beat\n" +
			"  hybrid                 + Okapi-BM25 fusion\n" +
			"  hybrid+closet          + the closet boost\n" +
			"  hybrid+closet+rerank   + the cross-encoder (only when RERANK_URL is set)\n\n" +
			"Read the DELTAS, not the absolute numbers: questions generated from a drawer\n" +
			"share vocabulary with it, which flatters every arm equally.\n\n" +
			"  agentsmemory eval --wing wing_acme --n 40 --cases /data/eval.jsonl\n" +
			"  agentsmemory eval --cases /data/eval.jsonl        # re-run the same questions\n" +
			"  agentsmemory eval --project acme --wing wing_acme # a multi-tenant database",
		Flags: append(serveFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "wing", Usage: "sample drawers from this wing only"},
			&cli.IntFlag{Name: "n", Value: 30, Usage: "how many drawers to sample when generating cases"},
			&cli.StringFlag{Name: "cases", Usage: "read cases from this JSONL file if it exists, otherwise write the generated ones there. Several comma-separated files are merged, which is how answerable and unanswerable questions get scored in one run — the only way the distance separation can be computed"},
			&cli.StringFlag{Name: "gen-model", Sources: cli.EnvVars("EVAL_GEN_MODEL"), Value: "qwen2.5-coder:7b", Usage: "model that writes the questions (must be GENERATIVE — an embedder like bge-m3 cannot answer /api/generate)"},
			&cli.StringFlag{Name: "gen-url", Sources: cli.EnvVars("EVAL_GEN_URL"), Usage: "where the question generator runs; defaults to --ollama-url. A URL containing /v1 is called as an OpenAI-compatible chat API, so a hosted model works here too"},
			&cli.StringFlag{Name: "gen-api-key", Sources: cli.EnvVars("EVAL_GEN_API_KEY"), Usage: "bearer token for --gen-url; required by hosted providers, ignored by a local one"},
			&cli.IntFlag{Name: "pool", Value: 50, Usage: "candidates fetched per query; every arm re-orders this same pool"},
			&cli.BoolFlag{Name: "contextual", Usage: "also score a contextual-chunk index: each chunk re-embedded with a little of its parent's context, built into a scratch namespace"},
			&cli.IntFlag{Name: "contextual-limit", Value: palace.DefaultContextualLimit, Usage: "how many chunks the contextual experiment covers — it costs an embedding pass and a second copy of those vectors, so it is capped rather than corpus-wide"},
			&cli.BoolFlag{Name: "drop-contextual", Usage: "delete the contextual experiment's vectors and exit"},
			&cli.StringFlag{Name: "style", Value: "paraphrase", Usage: "question style: paraphrase (no shared vocabulary), literal (keeps identifiers, like a real developer search), crosslingual (asks in the other language), temporal (asks for the current state of a fact an older memory still contradicts), absent (questions the palace should NOT answer), or real (replay recorded searches, gold judged by the generator model)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runEval(ctx, c, def, os.Stdout)
		},
	}
}

// runEval is the whole flow: load or generate cases, score the arms, print.
func runEval(ctx context.Context, c *cli.Command, def config.Config, out io.Writer) error {
	cfg := configFromCmd(c, def)
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	// Named like every other database-level subcommand (wing, inspect, share,
	// plan) rather than resolved through EnsureLocalWorkspace: that helper's
	// refusal to touch a workspace it did not provision guards an UNAUTHENTICATED
	// /mcp, and applying it here made eval the one command that could not measure
	// a multi-tenant palace at all — a database holding any workspace not slugged
	// "local" (the seeded demo team is enough) failed before reading a drawer.
	// Possessing the file is the authorization here, exactly as it already is for
	// `wing export` and `inspect`.
	team, err := resolveProject(ctx, svc, c.String("project"))
	if err != nil {
		return err
	}

	cases, from, err := loadOrGenerateCases(ctx, c, svc, team, out)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return fmt.Errorf("no eval cases: the wing has no drawers, or the generator produced nothing usable")
	}

	fmt.Fprintf(out, "\n%d case(s) %s, style %s, pool %d, corpus %s\n\n",
		len(cases), from, c.String("style"), c.Int("pool"), corpusLabel(c.String("wing")))
	// Every arm runs per case, and a reranked arm is real inference, so a case can
	// take seconds. Report each one as it lands: silence for minutes reads as a
	// hang, and the first version of this command was exactly that.
	if c.Bool("drop-contextual") {
		n, err := svc.drawers.DropContextualIndex(ctx, team.ID, c.Int("contextual-limit"))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "dropped the contextual experiment's vectors (%d point(s))\n", n)
		return nil
	}

	if c.Bool("contextual") {
		// Built here rather than lazily inside the arm: it is an embedding pass
		// over the whole corpus and the operator should see it happen, and pay
		// for it once rather than per case.
		fmt.Fprintf(out, "building the contextual index — one embedding pass over up to %d chunk(s), written beside the real vectors; `eval --drop-contextual` gives the space back\n",
			c.Int("contextual-limit"))
		started := time.Now()
		n, err := svc.drawers.BuildContextualIndex(ctx, team.ID, 32, c.Int("contextual-limit"))
		if err != nil {
			return fmt.Errorf("build contextual index: %w", err)
		}
		fmt.Fprintf(out, "  embedded %d chunk(s) with context in %s\n", n, time.Since(started).Round(time.Second))
	}

	report, err := svc.drawers.EvaluateWith(ctx, team.ID, cases, c.Int("pool"),
		palace.EvalOptions{Contextual: c.Bool("contextual")},
		func(done, total int, query string, elapsed time.Duration) {
			fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s\n", done, total, elapsed.Seconds(), firstLineOf(query, 62))
		})
	if err != nil {
		return err
	}
	printEvalTable(out, report)

	// The full result goes to disk: per-case ranks per arm, warnings, config.
	// The printed table is a VIEW of this file, not the record — a run that only
	// exists in scrollback cannot be compared with the next one, and comparing
	// runs is the entire reason cases are saved.
	if resPath := resultsPath(c.String("cases")); resPath != "" {
		if err := writeResults(resPath, c, report, cases); err != nil {
			fmt.Fprintf(out, "  (could not save the results file: %v)\n", err)
		} else {
			fmt.Fprintf(out, "full results (per-case ranks, config, warnings): %s\n", resPath)
		}
	}
	return nil
}

// resultsPath derives where the results artifact lands: beside the first case
// file, named after it.
func resultsPath(casesFlag string) string {
	first := strings.TrimSpace(strings.Split(casesFlag, ",")[0])
	if first == "" {
		return ""
	}
	return strings.TrimSuffix(first, ".jsonl") + ".results.json"
}

// writeResults persists the run in full.
func writeResults(path string, c *cli.Command, report palace.EvalReport, cases []palace.EvalCase) error {
	type armOut struct {
		Arm      string  `json:"arm"`
		MRR      float64 `json:"mrr"`
		CILo     float64 `json:"ci_lo"`
		CIHi     float64 `json:"ci_hi"`
		Recall1  int     `json:"recall1"`
		Recall5  int     `json:"recall5"`
		NotFound int     `json:"not_found"`
		Ranks    []int   `json:"ranks"`
	}
	arms := make([]armOut, 0, len(report.Arms))
	for _, m := range report.Arms {
		ci := palace.BootstrapMRR(m.Ranks)
		arms = append(arms, armOut{Arm: string(m.Arm), MRR: m.MRR, CILo: ci.Lo, CIHi: ci.Hi,
			Recall1: m.Recall1, Recall5: m.Recall5, NotFound: m.NotFound, Ranks: m.Ranks})
	}
	payload := map[string]any{
		"created":  time.Now().UTC().Format(time.RFC3339),
		"pool":     c.Int("pool"),
		"wing":     c.String("wing"),
		"cases":    len(cases),
		"warnings": report.Warnings,
		"arms":     arms,
		"details":  report.Details,
	}
	raw, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// loadOrGenerateCases reads a case file when one exists, and otherwise samples
// drawers and generates questions — writing them out so the next run compares
// like with like.
// generateTemporalCases builds the CatTemporal case set. Each case pairs a dated
// drawer with its nearest semantic neighbour whose content date is strictly
// older (OlderNeighbor): the newer drawer is the expected answer, and the older
// one needs no field of its own — it stays in the corpus as the distractor that
// ranking must put below the correction. Pair discovery runs BEFORE question
// generation so no LLM round trip is spent on a drawer with nothing to supersede.
func generateTemporalCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, error) {
	wing := c.String("wing")
	drawers, err := svc.drawers.DatedDrawers(ctx, team.ID, wing, c.Int("n"))
	if err != nil {
		return nil, "", fmt.Errorf("list dated drawers: %w", err)
	}
	if len(drawers) == 0 {
		// The corpus may well have drawers — just none whose chronology is known,
		// and without a date "was later corrected" cannot be labelled. Name that
		// distinctly so the reader fixes the dates, not the wing.
		return nil, "", fmt.Errorf("no drawers with a content date in %s of workspace %q — temporal cases need dated memories (a date in the source name, frontmatter, or first lines), so file some or widen --wing",
			corpusLabel(wing), team.Slug)
	}

	gen := &questionGen{
		url:     genURL(c),
		model:   c.String("gen-model"),
		apiKey:  strings.TrimSpace(c.String("gen-api-key")),
		prompt:  evalPromptTemporal,
		http:    &http.Client{Timeout: 120 * time.Second},
		verbose: out,
	}
	fmt.Fprintf(out, "generating temporal questions with %s (%d dated drawers)…\n", gen.model, len(drawers))
	genStart := time.Now()
	var cases []palace.EvalCase
	// One successful generation proves the generator is configured; until then a
	// failure means misconfiguration, mirroring the first-drawer abort of the
	// main loop. It cannot key off the loop index here, because pair discovery
	// may legitimately skip any number of drawers before the first ask happens.
	proven := false
	for i, d := range drawers {
		started := time.Now()
		older, ok, err := svc.drawers.OlderNeighbor(ctx, team.ID, d, c.Int("pool"))
		if err != nil {
			// Pair discovery uses the embedder and the vector store — the same
			// dependencies the eval itself cannot run without — so a failure here
			// is a broken setup, not this drawer's fault. Abort rather than skip.
			return nil, "", fmt.Errorf("find older neighbour: %w", err)
		}
		if !ok {
			fmt.Fprintf(out, "  [%2d/%2d] skipped: no dated older neighbour for %q (%s) — nothing in the corpus it supersedes\n",
				i+1, len(drawers), firstLineOf(d.Content, 40), d.ContentDate)
			continue
		}
		q, err := gen.ask(ctx, d.Content)
		if err != nil {
			if !proven {
				return nil, "", fmt.Errorf("question generator failed on the first pair, so it is misconfigured rather than unlucky: %w\n"+
					"  check `ollama list` — the model must be a GENERATIVE one (an embedder such as bge-m3 cannot answer /api/generate),\n"+
					"  pull it with `ollama pull %s`, or name one you already have with --gen-model", err, gen.model)
			}
			fmt.Fprintf(out, "  [%2d/%2d] failed: %v\n", i+1, len(drawers), err)
			continue
		}
		if q == "" {
			fmt.Fprintf(out, "  [%2d/%2d] empty answer, skipped\n", i+1, len(drawers))
			continue
		}
		proven = true
		// The pair's dates ride the progress line so an operator can sanity-check
		// the labelling as it happens — a "supersedes" whose dates look wrong is a
		// bad pair, and this line is the only place it is visible.
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s  (%s supersedes %s)\n",
			i+1, len(drawers), time.Since(started).Seconds(), firstLineOf(q, 62), d.ContentDate, older.ContentDate)
		cases = append(cases, palace.EvalCase{Query: q, Expect: d.ID, Wing: wing, Category: palace.CatTemporal})
	}
	if len(cases) == 0 {
		// Distinct from the generic "no eval cases": every sampled drawer was
		// dated, yet none had a dated older neighbour — the corpus has chronology
		// but no supersession to measure, which no --wing change will fix.
		return nil, "", fmt.Errorf("sampled %d dated drawer(s) but none has a dated older semantic neighbour — temporal cases need at least two dated memories about the same fact; file corrections with dates, or run another --style", len(drawers))
	}
	fmt.Fprintf(out, "generated %d case(s) in %s\n", len(cases), time.Since(genStart).Round(time.Second))
	if path := c.String("cases"); path != "" {
		meta := caseFileMeta{
			Generator: gen.model, Style: "temporal", Wing: wing,
			Corpus: len(drawers), Created: time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s\n", len(cases), path)
		}
	}
	return cases, "generated", nil
}

// generateRealCases replays queries agents actually ran (from search_events)
// as eval cases. There is no seed note to serve as gold, so the gold is a
// judged SET: the broad candidate pool is scored by the generator model as a
// relevance judge, and every accepted memory counts as a correct answer. Two
// of the review's findings meet their fix here — real queries were never
// phrased to suit any arm's feature (the generated styles all are), and a
// multi-member gold stops a valid alternative answer being scored as an error.
//
// The judge is an LLM, not a human, and the pool comes from vector retrieval,
// so a memory no arm can retrieve is invisible to these qrels. Both limits are
// recorded in the case-file provenance rather than papered over.
func generateRealCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, error) {
	wing := c.String("wing")
	queries, err := svc.drawers.SampleSearchQueries(ctx, team.ID, wing, c.Int("n"))
	if err != nil {
		return nil, "", fmt.Errorf("sample real queries: %w", err)
	}
	if len(queries) == 0 {
		return nil, "", fmt.Errorf("no recorded searches to replay in %s of workspace %q — real-query cases need search telemetry; run some sessions against this palace first, or use a generated --style",
			corpusLabel(wing), team.Slug)
	}
	judge := &questionGen{
		url:    genURL(c),
		model:  c.String("gen-model"),
		apiKey: strings.TrimSpace(c.String("gen-api-key")),
		prompt: evalPromptRelevanceCheck,
		http:   &http.Client{Timeout: 120 * time.Second},
	}
	fmt.Fprintf(out, "judging %d real queries with %s…\n", len(queries), judge.model)
	var cases []palace.EvalCase
	for i, q := range queries {
		started := time.Now()
		hits, err := svc.drawers.Search(ctx, team.ID, palace.SearchQuery{
			// Ungated: the judge decides relevance, not the distance cutoff, and
			// a relevant memory the gate would drop still belongs in the qrels.
			// The judged pool is capped at 12 — each hit costs one judge call —
			// which means a memory below rank 12 here is invisible to the qrels.
			// That pooling bias is inherent to judged evals; it is recorded in
			// the provenance line rather than pretended away.
			Query: q, Wing: wing, Limit: 12, SkipTelemetry: true,
		})
		if err != nil {
			return nil, "", fmt.Errorf("pool real query %q: %w", q, err)
		}
		var relevant []string
		for _, h := range hits {
			excerpt := palace.Snippet(h.Drawer.Content, q, 900)
			reply, jerr := judge.ask(ctx, "QUERY: "+q+"\n\nNOTE:\n"+excerpt)
			if jerr != nil {
				// The FIRST failure aborts, matching the generator preflight
				// doctrine: a judge that cannot score one note is misconfigured,
				// and every later call would fail the same way.
				if i == 0 && len(relevant) == 0 {
					return nil, "", fmt.Errorf("the relevance judge is not usable: %w\n\n%s", jerr, judge.hint(ctx))
				}
				fmt.Fprintf(out, "  [%2d/%2d] judge failed on one note, skipped it: %v\n", i+1, len(queries), jerr)
				continue
			}
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(reply)), "YES") {
				relevant = append(relevant, h.Drawer.ID)
			}
		}
		if len(relevant) == 0 {
			fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  no relevant memory judged for: %s\n", i+1, len(queries), time.Since(started).Seconds(), firstLineOf(q, 50))
			continue
		}
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %d relevant  %s\n", i+1, len(queries), time.Since(started).Seconds(), len(relevant), firstLineOf(q, 50))
		cases = append(cases, palace.EvalCase{Query: q, ExpectAny: relevant, Wing: wing, Category: palace.CatReal})
	}
	if len(cases) == 0 {
		return nil, "", fmt.Errorf("none of the %d replayed queries had a judged-relevant memory — either the palace genuinely cannot answer its own traffic (check the recall stats) or the judge model is refusing everything", len(queries))
	}
	if path := c.String("cases"); path != "" {
		meta := caseFileMeta{
			Generator: judge.model, Style: "real", Wing: wing,
			Corpus: len(queries), Created: time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s\n", len(cases), path)
		}
	}
	return cases, "replayed from telemetry", nil
}

// verifyAbsent checks a generated "absent" question against the whole corpus,
// not just the note it was seeded from. The generator only promises the seed
// note cannot answer it; if any other memory can, the case would score a
// correct retrieval as a false positive, and every gate calibrated on such
// cases would be calibrated on invalid labels. Returns the id of a drawer that
// answers the question, or "" when the top hits all fail the answer check.
func verifyAbsent(ctx context.Context, svc *services, teamID, wing, question string, gen *questionGen) (string, error) {
	// No distance gate: absence must be checked broadly. A hit the production
	// gate would drop can still prove the knowledge exists in the palace.
	hits, err := svc.drawers.Search(ctx, teamID, palace.SearchQuery{
		Query: question, Wing: wing, Limit: 3, SkipTelemetry: true,
	})
	if err != nil {
		return "", err
	}
	checker := &questionGen{
		url: gen.url, model: gen.model, apiKey: gen.apiKey,
		prompt: evalPromptAnswerCheck, http: gen.http,
	}
	for _, h := range hits {
		// The query-centred snippet, not the head of the note: the answer to the
		// question sits near the query terms, and the head of a long mined part
		// is usually about something else entirely.
		excerpt := palace.Snippet(h.Drawer.Content, question, 900)
		reply, err := checker.ask(ctx, "QUESTION: "+question+"\n\nNOTE:\n"+excerpt)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(reply)), "YES") {
			return h.Drawer.ID, nil
		}
	}
	return "", nil
}

func loadOrGenerateCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, error) {
	path := c.String("cases")
	if path != "" {
		var merged []palace.EvalCase
		files := strings.Split(path, ",")
		for _, f := range files {
			f = strings.TrimSpace(f)
			cases, err := readCases(f)
			switch {
			case err == nil:
				merged = append(merged, cases...)
			case os.IsNotExist(err) && len(files) == 1:
				// The generate-then-save flow: a single named file that does not
				// exist yet is where the generated cases will land.
			default:
				// A CORRUPT file, or a missing one among several, silently shrank
				// the case set once; the label still said "from N files" and no
				// run was comparable with any other.
				return nil, "", fmt.Errorf("cases file %s: %w", f, err)
			}
		}
		if len(merged) > 0 {
			label := "from " + path
			if len(files) > 1 {
				label = fmt.Sprintf("from %d files", len(files))
			}
			return merged, label, nil
		}
	}

	if c.String("style") == "real" {
		return generateRealCases(ctx, c, svc, team, out)
	}
	// Temporal cases are shaped differently — a pair is discovered before a
	// question is written — so the style gets its own generation loop instead of
	// growing this one a second set of skip reasons.
	if c.String("style") == "temporal" {
		return generateTemporalCases(ctx, c, svc, team, out)
	}

	// Sample across the whole corpus rather than its newest slice: on a palace
	// that holds years, the newest thousand memories are one week of work, and an
	// eval built from them measures recall on recent memory only.
	//
	// Reproducibility comes from the saved case file rather than from a seed: the
	// questions are what a re-run must hold constant, and they are on disk.
	drawers, err := svc.drawers.SampleDrawers(ctx, team.ID, c.String("wing"), c.Int("n"))
	if err != nil {
		return nil, "", fmt.Errorf("sample drawers: %w", err)
	}
	if len(drawers) == 0 {
		// Named distinctly rather than folded into runEval's "no eval cases": an
		// empty corpus and a broken generator are different faults with different
		// fixes, and reporting them with one sentence sent the reader to inspect a
		// wing that was never the problem.
		return nil, "", fmt.Errorf("no drawers to sample in %s of workspace %q — file some memories first, or widen --wing",
			corpusLabel(c.String("wing")), team.Slug)
	}

	prompt, style := evalPromptParaphrase, c.String("style")
	category := palace.CatSingle
	switch style {
	case "literal":
		prompt = evalPromptLiteral
	case "crosslingual":
		prompt, category = evalPromptCrossLingual, palace.CatCrossLingual
	case "absent":
		prompt, category = evalPromptAbsent, palace.CatAbsent
	}
	gen := &questionGen{
		url:     genURL(c),
		model:   c.String("gen-model"),
		apiKey:  strings.TrimSpace(c.String("gen-api-key")),
		prompt:  prompt,
		http:    &http.Client{Timeout: 120 * time.Second},
		verbose: out,
	}

	// Ask for ONE question before asking for thirty. A missing model, a wrong URL
	// or a bad key fails identically on every case, and printing that failure n
	// times is thirty lines that say one thing — while the run still takes as long
	// as a working one.
	if _, err := gen.ask(ctx, drawers[0].Content); err != nil {
		return nil, "", fmt.Errorf("the question generator is not usable: %w\n\n%s", err, gen.hint(ctx))
	}
	fmt.Fprintf(out, "generating %s questions with %s (%d drawers)…\n", style, gen.model, len(drawers))
	genStart := time.Now()
	var cases []palace.EvalCase
	for i, d := range drawers {
		started := time.Now()
		q, err := gen.ask(ctx, d.Content)
		if err != nil {
			// The FIRST failure aborts. A generator that cannot answer the first
			// drawer is misconfigured rather than unlucky — a missing model, a wrong
			// --ollama-url, a stopped daemon — and every remaining drawer would fail
			// the same way, so continuing buys nothing and costs one round trip each.
			// It also buries the cause: the loop used to end with "no eval cases: the
			// wing has no drawers", blaming a corpus that was never the problem.
			// Later failures still skip, because by then the generator has proven it
			// works and the fault is that drawer's.
			if i == 0 {
				return nil, "", fmt.Errorf("question generator failed on the first drawer, so it is misconfigured rather than unlucky: %w\n"+
					"  check `ollama list` — the model must be a GENERATIVE one (an embedder such as bge-m3 cannot answer /api/generate),\n"+
					"  pull it with `ollama pull %s`, or name one you already have with --gen-model", err, gen.model)
			}
			fmt.Fprintf(out, "  [%2d/%2d] failed: %v\n", i+1, len(drawers), err)
			continue
		}
		if q == "" {
			fmt.Fprintf(out, "  [%2d/%2d] empty answer, skipped\n", i+1, len(drawers))
			continue
		}
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s\n", i+1, len(drawers), time.Since(started).Seconds(), firstLineOf(q, 62))
		expect := d.ID
		if category == palace.CatAbsent {
			// There is no gold for a question the palace should not answer; what
			// gets measured is whether it returns something confident anyway.
			expect = ""
			// The seed note not answering it is not enough — verify the rest of
			// the corpus cannot either, or drop the case.
			if answeredBy, verr := verifyAbsent(ctx, svc, team.ID, c.String("wing"), q, gen); verr != nil {
				fmt.Fprintf(out, "  [%2d/%2d] kept UNVERIFIED (absence check failed: %v)\n", i+1, len(drawers), verr)
			} else if answeredBy != "" {
				fmt.Fprintf(out, "  [%2d/%2d] rejected: memory %s answers it, so it is not absent\n", i+1, len(drawers), answeredBy)
				continue
			}
		}
		cases = append(cases, palace.EvalCase{Query: q, Expect: expect, Wing: c.String("wing"), Category: category})
	}
	fmt.Fprintf(out, "generated %d case(s) in %s\n", len(cases), time.Since(genStart).Round(time.Second))
	if path != "" && len(cases) > 0 {
		meta := caseFileMeta{
			Generator: gen.model, Style: style, Wing: c.String("wing"),
			Corpus: len(drawers), Created: time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s — pass --cases %s to re-run these exact questions\n", len(cases), path, path)
		}
	}
	return cases, "generated", nil
}

// genURL is where the question generator runs. It defaults to the embedder's
// Ollama so a single-machine setup configures nothing, and is separable because
// the two jobs have different shapes: embedding is a small, constant, local cost
// that belongs next to the data, while generating eval questions is a one-off
// burst of LLM work an operator may want to send to a bigger hosted model without
// moving their vectors off the box.
func genURL(c *cli.Command) string {
	if u := strings.TrimSpace(c.String("gen-url")); u != "" {
		return u
	}
	return cfg2URL(c.String("ollama-url"))
}

// questionGen asks a model for a question a given memory answers.
//
// The wire format is Ollama's own POST /api/generate ({model, prompt, stream,
// options} in, {response} out), so --gen-url accepts a local Ollama, a remote
// one, or hosted Ollama with a bearer token. It is NOT OpenAI-shaped: an
// OpenAI/Anthropic-compatible endpoint speaks /v1/chat/completions and would need
// a different request and reply, which is deliberately out of scope here — the
// generator is scaffolding for producing eval questions, not a model gateway.
type questionGen struct {
	url    string
	model  string
	apiKey string // sent as Authorization: Bearer when set; hosted providers need it
	prompt string

	http    *http.Client
	verbose io.Writer
}

// openAIShaped reports whether the endpoint should be called as an
// OpenAI-compatible chat API rather than Ollama's own. The /v1 convention is what
// every hosted provider and every local shim agrees on, so it is the honest
// discriminator — and it means a cloud model needs no new flag beyond its URL.
func (g *questionGen) openAIShaped() bool { return strings.Contains(g.url, "/v1") }

// hint turns a generator failure into something actionable: which models the
// endpoint actually serves, when it can be asked.
func (g *questionGen) hint(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  endpoint: %s\n  model:    %s\n", g.url, g.model)
	if g.openAIShaped() {
		b.WriteString("  Set EVAL_GEN_MODEL to a model this endpoint serves, and EVAL_GEN_API_KEY if it needs a key.\n")
		return b.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.url, "/")+"/api/tags", nil)
	if err == nil {
		if resp, err := g.http.Do(req); err == nil {
			defer resp.Body.Close()
			var tags struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			if json.NewDecoder(resp.Body).Decode(&tags) == nil && len(tags.Models) > 0 {
				names := make([]string, 0, len(tags.Models))
				for _, m := range tags.Models {
					names = append(names, m.Name)
				}
				fmt.Fprintf(&b, "  this endpoint serves: %s\n", strings.Join(names, ", "))
			}
		}
	}
	b.WriteString("  Set EVAL_GEN_MODEL to one of those, pull the one you want (ollama pull <model>),\n")
	b.WriteString("  or point EVAL_GEN_URL at another endpoint — a URL containing /v1 is called as an OpenAI-compatible API.\n")
	return b.String()
}

// Two prompts, because there are two regimes and they rank differently.
//
// paraphrase is the hard case: the question shares no distinctive vocabulary with
// the note, so lexical matching has nothing to work with and the vector half
// carries the query. literal is the common case: a developer searching months
// later usually DOES remember the identifier, the flag or the error string.
//
// Measuring only one of them produces a confident, wrong conclusion about BM25 —
// which is exactly what the first run of this eval did.
const evalPromptParaphrase = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question that this note answers — the
kind a colleague would type into a search box months later, when they remember the
problem but not the note.

Rules:
- Paraphrase. Do NOT reuse the note's distinctive nouns, identifiers, file names,
  flags, or error strings.
- Ask about the situation or the decision, not about the wording.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptCrossLingual tests the claim nobody has checked on this palace: that
// a multilingual embedder actually bridges the two languages the memories are
// written in. A bilingual team asks in whichever language it is thinking in, and
// the memory it needs is often in the other one.
const evalPromptCrossLingual = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question that this note answers — but
write it in the OTHER language: if the note is mostly English, ask in Lithuanian;
if it is mostly Lithuanian, ask in English.

Rules:
- Do not translate the note's distinctive identifiers, file names or flags — keep
  those as they are, and put the rest of the question in the other language.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptAbsent generates questions the palace should NOT answer. Recall has
// only ever been measured on questions with an answer, which means the gate that
// decides "we do not know this" has never been measured at all.
const evalPromptAbsent = `You are writing a NEGATIVE evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question about the same general area
of work that this note does NOT answer and could not answer — a neighbouring
topic, not this one.

Rules:
- Plausible for this team to ask, but genuinely unanswered by the note.
- Do not reuse the note's distinctive identifiers.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptAnswerCheck asks whether a note answers a question. It exists
// because "absent" used to mean absent from the one sampled note, while the
// evaluator scored it as absent from the whole palace — another memory could
// answer the question perfectly, and the case would count a correct retrieval
// as a false positive.
const evalPromptAnswerCheck = `Does the NOTE below contain the answer to the QUESTION?
Reply with exactly one word: YES or NO.

%s`

// evalPromptRelevanceCheck judges a real query against one retrieved note. Real
// queries are often fragments rather than questions, so this asks about intent
// ("what the searcher wanted") instead of literal answerhood.
const evalPromptRelevanceCheck = `An engineer searched their team memory. Below is their QUERY and one NOTE the
search returned. Is this note what they were looking for — does it contain the
information the query is after?
Reply with exactly one word: YES or NO.

%s`

// evalPromptTemporal generates questions for facts that were later corrected.
// The note the generator sees is the NEWER half of a discovered pair; the older
// version stays in the corpus as a distractor. The question must ask for the
// fact's current state WITHOUT carrying a date — the eval measures whether
// ranking prefers the newer memory on its own, and a date in the question would
// hand it the answer.
const evalPromptTemporal = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. It records the current state of a fact that
changed at some point. Write ONE question asking what the current state of that
fact is — the kind a colleague would type into a search box, wanting today's
answer rather than the history.

Rules:
- Ask about the fact's current state, value, or status — not about when or why
  it changed.
- Do NOT include any date, year, or month in the question.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

const evalPromptLiteral = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE search query that should find this
note — the kind a developer actually types: partly natural language, partly the
exact identifier, file name, flag or error string they remember.

Rules:
- KEEP one or two distinctive terms from the note verbatim (a symbol, file, flag).
- Keep it short, like something typed into a search box.
- One line, under 15 words, no quotes, no preamble.

NOTE:
%s

QUERY:`

// ask returns one generated question, or "" when the model produced nothing
// usable.
func (g *questionGen) ask(ctx context.Context, content string) (string, error) {
	if len([]rune(content)) > 1200 {
		content = string([]rune(content)[:1200])
	}
	body, err := json.Marshal(map[string]any{
		"model":  g.model,
		"prompt": fmt.Sprintf(g.prompt, content),
		"stream": false,
		"options": map[string]any{
			// Low temperature: the eval wants a representative question, not a
			// creative one, and reproducibility across runs matters more here than
			// variety.
			"temperature": 0.2,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.url, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("generate: %d: %s", resp.StatusCode, firstLineOf(string(raw), 120))
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return cleanQuestion(out.Response), nil
}

// cleanQuestion trims the shapes a small model wraps an answer in.
func cleanQuestion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
	s = strings.TrimPrefix(s, "QUESTION:")
	return strings.TrimSpace(s)
}

// caseFileMeta is the provenance record written as the FIRST line of a case
// file. Two runs of "the same" eval on different machines have already disagreed
// for reasons that were invisible afterwards — different generator models write
// questions of different difficulty, and nothing recorded which model wrote
// which file. A case file that does not say how it was made cannot be compared
// with anything.
type caseFileMeta struct {
	Meta      bool   `json:"meta"`
	Generator string `json:"generator"`
	Style     string `json:"style"`
	Wing      string `json:"wing"`
	Corpus    int    `json:"corpus_drawers"`
	Created   string `json:"created"`
}

// readCases loads a JSONL case file.
func readCases(path string) ([]palace.EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cases []palace.EvalCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// A provenance line is metadata, not a case.
		if strings.Contains(line, `"meta":true`) {
			continue
		}
		var c palace.EvalCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cases = append(cases, c)
	}
	return cases, sc.Err()
}

// writeCases saves cases as JSONL.
func writeCases(path string, cases []palace.EvalCase, meta caseFileMeta) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	meta.Meta = true
	if err := enc.Encode(meta); err != nil {
		return err
	}
	for _, c := range cases {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// printEvalTable renders the arms and the cases every arm missed.
func printEvalTable(out io.Writer, report palace.EvalReport) {
	for _, w := range report.Warnings {
		fmt.Fprintf(out, "⚠ %s\n", w)
	}

	// The baseline every arm is compared against is the best MRR in the table.
	// A PAIRED difference that includes zero is reported as INCONCLUSIVE, not as
	// equivalence: the winner was itself picked from this data (winner's curse),
	// and a CI spanning zero means the data cannot rule out a difference — it
	// never means one was ruled out. The table says exactly that and no more.
	best := 0
	for i, m := range report.Arms {
		if m.MRR > report.Arms[best].MRR {
			best = i
		}
	}

	fmt.Fprintf(out, "%-22s %8s %8s %8s %14s %10s   %s\n", "arm", "R@1", "R@5", "MRR", "95% CI", "not found", "vs best")
	fmt.Fprintf(out, "%s\n", strings.Repeat("-", 92))
	for i, m := range report.Arms {
		ci := palace.BootstrapMRR(m.Ranks)
		verdict := ""
		switch {
		case len(m.Ranks) == 0:
			verdict = "no scoreable cases"
		case i == best:
			verdict = "BEST"
		case len(m.Ranks) == len(report.Arms[best].Ranks):
			if delta := palace.PairedDelta(m.Ranks, report.Arms[best].Ranks); delta.Contains(0) {
				verdict = "inconclusive vs best (CI spans zero)"
			} else {
				verdict = fmt.Sprintf("worse by %.2f–%.2f", -delta.Hi, -delta.Lo)
			}
		}
		fmt.Fprintf(out, "%-22s %7.0f%% %7.0f%% %8.3f %14s %10d   %s\n",
			m.Arm, m.Recall1Pct(), m.Recall5Pct(), m.MRR, ci, m.NotFound, verdict)
	}
	fmt.Fprintf(out, "n=%d — CI column: single-arm bootstrap; 'vs best' verdicts: PAIRED bootstrap on per-case deltas (trust these, not CI overlap). The best arm was picked from this same table, so unadjusted comparisons against it flatter the winner; 'inconclusive' means exactly that, never equivalence\n",
		len(report.Arms[0].Ranks))

	printPoolDiagnosis(out, report)
	printCategories(out, report)
	printSeparation(out, report)

	// Questions no arm could answer are usually a bad question rather than bad
	// ranking — the generator drifted off the note. Printing them keeps the table
	// honest, because they drag every arm down equally.
	var lost []string
	for _, d := range report.Details {
		// An absent case retrieving nothing is the CORRECT outcome, not a lost
		// one: listing it as a failure inverts the meaning of the whole category.
		if d.Category == palace.CatAbsent {
			continue
		}
		missed := true
		for _, r := range d.Ranks {
			if r > 0 {
				missed = false
				break
			}
		}
		if missed {
			lost = append(lost, d.Query)
		}
	}
	if len(lost) > 0 {
		fmt.Fprintf(out, "\n%d question(s) no arm retrieved (check whether the question is about the note at all):\n", len(lost))
		for i, q := range lost {
			if i == 5 {
				fmt.Fprintf(out, "  … and %d more\n", len(lost)-5)
				break
			}
			fmt.Fprintf(out, "  - %s\n", q)
		}
	}
}

// printPoolDiagnosis separates the two failures a single score hides.
//
// A memory can be missed because ranking put it below the page (a RANKING
// failure, which reranking and fusion address) or because it never entered the
// candidate pool at all (a RETRIEVAL failure, which no amount of reranking can
// fix — the answer was never on the table). They call for opposite work, and on
// a large corpus the second becomes the common one while the score alone still
// just says "worse".
func printPoolDiagnosis(out io.Writer, report palace.EvalReport) {
	worst, contextualExtra := 0, 0
	for _, m := range report.Arms {
		// The contextual arm retrieves from its own CAPPED index, so its misses
		// mean "outside the experiment's sample", not "outside the shared pool" —
		// folding them in once steered an operator toward raising --pool when the
		// binding knob was --contextual-limit.
		if m.Arm == palace.ArmContextual {
			contextualExtra = m.NotFound
			continue
		}
		if m.NotFound > worst {
			worst = m.NotFound
		}
	}
	if contextualExtra > worst {
		fmt.Fprintf(out, "\nthe contextual arm missed %d question(s) beyond the shared pool's misses — those golds fall outside its capped sample; the knob is --contextual-limit, not --pool.\n", contextualExtra-worst)
	}
	if worst == 0 {
		return
	}
	cases := 0
	if len(report.Arms) > 0 {
		cases = report.Arms[0].Cases
	}
	fmt.Fprintf(out, "\n%d of %d question(s) had their answer OUTSIDE the candidate pool — a retrieval failure, not a ranking one.\n", worst, cases)
	fmt.Fprintf(out, "  No reranker can recover those. Raise --pool and re-run: if they come back, the ranking is fine and the pool was too small;\n")
	fmt.Fprintf(out, "  if they stay missing, the embedding is not placing those memories near their question.\n")
}

// printCategories breaks the leading arm out by question kind. An average over
// categories hides the failure that matters: a system can be perfect on
// single-hop questions and blind on the ones that need the CURRENT version of a
// corrected fact, and the mean looks fine.
func printCategories(out io.Writer, report palace.EvalReport) {
	if len(report.Arms) == 0 {
		return
	}
	// Break out the arm an operator will actually run: production first, then the
	// configured blend, then the plain fusion — never a sweep extreme, which is
	// what "last in the list" silently was once the sweeps existed.
	best := report.Arms[len(report.Arms)-1]
	for _, want := range []palace.EvalArm{palace.ArmProduction, palace.ArmReranked, palace.ArmHybridCloset} {
		for _, m := range report.Arms {
			if m.Arm == want && len(m.ByCategory) > 0 {
				best = m
				break
			}
		}
		if best.Arm == want {
			break
		}
	}
	if len(best.ByCategory) <= 1 {
		return // nothing to break out
	}
	fmt.Fprintf(out, "\n%s, by question kind\n", best.Arm)
	fmt.Fprintf(out, "%-16s %6s %8s %8s\n", "category", "cases", "R@1", "MRR")
	cats := make([]string, 0, len(best.ByCategory))
	for cat := range best.ByCategory {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		m := best.ByCategory[cat]
		if cat == palace.CatAbsent {
			fmt.Fprintf(out, "%-16s %6d %8s %8s   (scored by the separation below)\n", cat, m.Cases, "—", "—")
			continue
		}
		r1 := 0.0
		if m.Cases > 0 {
			r1 = float64(m.Recall1) / float64(m.Cases) * 100
		}
		fmt.Fprintf(out, "%-16s %6d %7.0f%% %8.3f\n", cat, m.Cases, r1, m.MRR)
	}
}

// printSeparation reports where answerable and unanswerable questions land in
// distance, which is the only honest basis for setting max_distance — the gate
// that decides when the palace should say it does not know. Overlapping
// distributions mean no threshold can separate them, and that is worth knowing
// before tuning a number.
func printSeparation(out io.Writer, report palace.EvalReport) {
	if len(report.AbsentDistances) == 0 || len(report.GoldDistances) == 0 {
		return
	}
	gold := append([]float64(nil), report.GoldDistances...)
	absent := append([]float64(nil), report.AbsentDistances...)
	sort.Float64s(gold)
	sort.Float64s(absent)
	fmt.Fprintf(out, "\ntop-1 distance: answerable %.3f–%.3f (median %.3f) | unanswerable %.3f–%.3f (median %.3f)\n",
		gold[0], gold[len(gold)-1], median(gold), absent[0], absent[len(absent)-1], median(absent))
	if gold[len(gold)-1] < absent[0] {
		fmt.Fprintf(out, "  clean separation — max_distance between %.3f and %.3f would answer only what it can\n",
			gold[len(gold)-1], absent[0])
		return
	}
	fmt.Fprintf(out, "  distributions OVERLAP — no max_distance separates them; a confidence gate needs a different signal\n")
	printRerankSeparation(out, report)
}

// printRerankSeparation asks the same question of the cross-encoder's score.
//
// Cosine distance answers "how similar", which is not the question — a note about
// the same system in the same vocabulary is similar to a question it cannot
// answer, which is exactly why the distributions overlap. A cross-encoder score
// answers "does this document answer this query", which IS the question, so it is
// the natural candidate for the gate that decides when to say nothing.
func printRerankSeparation(out io.Writer, report palace.EvalReport) {
	if len(report.GoldRerank) == 0 || len(report.AbsentRerank) == 0 {
		return
	}
	gold := append([]float64(nil), report.GoldRerank...)
	absent := append([]float64(nil), report.AbsentRerank...)
	sort.Float64s(gold)
	sort.Float64s(absent)
	fmt.Fprintf(out, "top-1 rerank score: answerable %.3f–%.3f (median %.3f) | unanswerable %.3f–%.3f (median %.3f)\n",
		gold[0], gold[len(gold)-1], median(gold), absent[0], absent[len(absent)-1], median(absent))
	if absent[len(absent)-1] < gold[0] {
		fmt.Fprintf(out, "  CLEAN SEPARATION — a cross-encoder threshold between %.3f and %.3f answers only what the palace holds\n",
			absent[len(absent)-1], gold[0])
		return
	}
	fmt.Fprintf(out, "  overlapping too — but compare the medians: %.3f against %.3f is the size of the signal available\n",
		median(gold), median(absent))
}

// median of a sorted slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// corpusLabel names the scope for the header line.
func corpusLabel(wing string) string {
	if wing == "" {
		return "all wings"
	}
	return wing
}

// cfg2URL defaults the generator endpoint to the configured Ollama.
func cfg2URL(ollamaURL string) string {
	if strings.TrimSpace(ollamaURL) == "" {
		return "http://localhost:11434"
	}
	return ollamaURL
}

// firstLineOf truncates an error body for a one-line message.
func firstLineOf(s string, max int) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	// Truncate by runes, not bytes: half this palace's content is Lithuanian,
	// and a byte cut can split a multibyte rune into mojibake mid-progress-line.
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}
