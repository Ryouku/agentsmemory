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
	"math/rand"
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
			&cli.StringFlag{Name: "cases", Usage: "read cases from this JSONL file if it exists, otherwise write the generated ones there"},
			&cli.StringFlag{Name: "gen-model", Sources: cli.EnvVars("EVAL_GEN_MODEL"), Value: "qwen2.5-coder:7b", Usage: "model that writes the questions (must be GENERATIVE — an embedder like bge-m3 cannot answer /api/generate)"},
			&cli.StringFlag{Name: "gen-url", Sources: cli.EnvVars("EVAL_GEN_URL"), Usage: "where the question generator runs; defaults to --ollama-url, so set it only to generate somewhere other than the embedder (e.g. Ollama Cloud)"},
			&cli.StringFlag{Name: "gen-api-key", Sources: cli.EnvVars("EVAL_GEN_API_KEY"), Usage: "bearer token for --gen-url; required by hosted Ollama, ignored by a local one"},
			&cli.IntFlag{Name: "pool", Value: 50, Usage: "candidates fetched per query; every arm re-orders this same pool"},
			&cli.IntFlag{Name: "seed", Value: 1, Usage: "sampling seed, so a re-run picks the same drawers"},
			&cli.BoolFlag{Name: "contextual", Usage: "also score a contextual-chunk index: each chunk re-embedded with a little of its parent's context, built into a scratch namespace (costs one embedding pass over the corpus)"},
			&cli.StringFlag{Name: "style", Value: "paraphrase", Usage: "question style: paraphrase (no shared vocabulary), literal (keeps identifiers, like a real developer search), crosslingual (asks in the other language), or absent (questions the palace should NOT answer)"},
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
	if c.Bool("contextual") {
		// Built here rather than lazily inside the arm: it is an embedding pass
		// over the whole corpus and the operator should see it happen, and pay
		// for it once rather than per case.
		fmt.Fprintf(out, "building the contextual index (one embedding pass over the corpus)…\n")
		started := time.Now()
		n, err := svc.drawers.BuildContextualIndex(ctx, team.ID, 32)
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
	return nil
}

// loadOrGenerateCases reads a case file when one exists, and otherwise samples
// drawers and generates questions — writing them out so the next run compares
// like with like.
func loadOrGenerateCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, error) {
	path := c.String("cases")
	if path != "" {
		if cases, err := readCases(path); err == nil && len(cases) > 0 {
			return cases, "from " + path, nil
		}
	}

	drawers, err := svc.drawers.List(ctx, team.ID, c.String("wing"), "", 1000, 0)
	if err != nil {
		return nil, "", fmt.Errorf("list drawers: %w", err)
	}
	if len(drawers) == 0 {
		// Named distinctly rather than folded into runEval's "no eval cases": an
		// empty corpus and a broken generator are different faults with different
		// fixes, and reporting them with one sentence sent the reader to inspect a
		// wing that was never the problem.
		return nil, "", fmt.Errorf("no drawers to sample in %s of workspace %q — file some memories first, or widen --wing",
			corpusLabel(c.String("wing")), team.Slug)
	}
	// Deterministic sample: a ranking change must be judged on the same drawers
	// as the run before it.
	rng := rand.New(rand.NewSource(int64(c.Int("seed"))))
	rng.Shuffle(len(drawers), func(i, j int) { drawers[i], drawers[j] = drawers[j], drawers[i] })
	if n := c.Int("n"); n < len(drawers) {
		drawers = drawers[:n]
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
		}
		cases = append(cases, palace.EvalCase{Query: q, Expect: expect, Wing: c.String("wing"), Category: category})
	}
	fmt.Fprintf(out, "generated %d case(s) in %s\n", len(cases), time.Since(genStart).Round(time.Second))
	if path != "" && len(cases) > 0 {
		if err := writeCases(path, cases); err != nil {
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
	apiKey string // sent as Authorization: Bearer when set; hosted Ollama needs it
	prompt string

	http    *http.Client
	verbose io.Writer
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
		var c palace.EvalCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cases = append(cases, c)
	}
	return cases, sc.Err()
}

// writeCases saves cases as JSONL.
func writeCases(path string, cases []palace.EvalCase) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, c := range cases {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// printEvalTable renders the arms and the cases every arm missed.
func printEvalTable(out io.Writer, report palace.EvalReport) {
	fmt.Fprintf(out, "%-22s %8s %8s %8s %10s\n", "arm", "R@1", "R@5", "MRR", "not found")
	fmt.Fprintf(out, "%s\n", strings.Repeat("-", 60))
	for _, m := range report.Arms {
		fmt.Fprintf(out, "%-22s %7.0f%% %7.0f%% %8.3f %10d\n",
			m.Arm, m.Recall1Pct(), m.Recall5Pct(), m.MRR, m.NotFound)
	}

	printCategories(out, report)
	printSeparation(out, report)

	// Questions no arm could answer are usually a bad question rather than bad
	// ranking — the generator drifted off the note. Printing them keeps the table
	// honest, because they drag every arm down equally.
	var lost []string
	for _, d := range report.Details {
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

// printCategories breaks the leading arm out by question kind. An average over
// categories hides the failure that matters: a system can be perfect on
// single-hop questions and blind on the ones that need the CURRENT version of a
// corrected fact, and the mean looks fine.
func printCategories(out io.Writer, report palace.EvalReport) {
	if len(report.Arms) == 0 {
		return
	}
	// The production arm is the last one that is not a swept variant.
	best := report.Arms[len(report.Arms)-1]
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
	if len(s) > max {
		s = s[:max]
	}
	return s
}
