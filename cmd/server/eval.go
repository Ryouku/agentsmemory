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
			"  agentsmemory eval --cases /data/eval.jsonl        # re-run the same questions",
		Flags: append(serveFlags(def),
			&cli.StringFlag{Name: "wing", Usage: "sample drawers from this wing only"},
			&cli.IntFlag{Name: "n", Value: 30, Usage: "how many drawers to sample when generating cases"},
			&cli.StringFlag{Name: "cases", Usage: "read cases from this JSONL file if it exists, otherwise write the generated ones there. Several comma-separated files are merged, which is how answerable and unanswerable questions get scored in one run — the only way the distance separation can be computed"},
			&cli.StringFlag{Name: "gen-model", Sources: cli.EnvVars("EVAL_GEN_MODEL"), Value: "qwen2.5-coder:7b", Usage: "model that writes the questions — any model your generator endpoint serves"},
			&cli.StringFlag{Name: "gen-url", Sources: cli.EnvVars("EVAL_GEN_URL"), Usage: "generator endpoint (default: the configured Ollama). A URL containing /v1 is called as an OpenAI-compatible chat API, so a hosted model works here too"},
			&cli.StringFlag{Name: "gen-api-key", Sources: cli.EnvVars("EVAL_GEN_API_KEY"), Usage: "bearer token for the generator endpoint, when it needs one"},
			&cli.IntFlag{Name: "pool", Value: 50, Usage: "candidates fetched per query; every arm re-orders this same pool"},
			&cli.BoolFlag{Name: "contextual", Usage: "also score a contextual-chunk index: each chunk re-embedded with a little of its parent's context, built into a scratch namespace"},
			&cli.IntFlag{Name: "contextual-limit", Value: palace.DefaultContextualLimit, Usage: "how many chunks the contextual experiment covers — it costs an embedding pass and a second copy of those vectors, so it is capped rather than corpus-wide"},
			&cli.BoolFlag{Name: "drop-contextual", Usage: "delete the contextual experiment's vectors and exit"},
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
	t, err := svc.tenants.EnsureLocalWorkspace(ctx)
	if err != nil {
		return fmt.Errorf("resolve the local workspace (eval runs against --local): %w", err)
	}

	cases, from, err := loadOrGenerateCases(ctx, c, svc, t, out)
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
		n, err := svc.drawers.DropContextualIndex(ctx, t.TeamID, c.Int("contextual-limit"))
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
		n, err := svc.drawers.BuildContextualIndex(ctx, t.TeamID, 32, c.Int("contextual-limit"))
		if err != nil {
			return fmt.Errorf("build contextual index: %w", err)
		}
		fmt.Fprintf(out, "  embedded %d chunk(s) with context in %s\n", n, time.Since(started).Round(time.Second))
	}

	report, err := svc.drawers.EvaluateWith(ctx, t.TeamID, cases, c.Int("pool"),
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
func loadOrGenerateCases(ctx context.Context, c *cli.Command, svc *services, t tenant.Tenant, out io.Writer) ([]palace.EvalCase, string, error) {
	path := c.String("cases")
	if path != "" {
		var merged []palace.EvalCase
		files := strings.Split(path, ",")
		for _, f := range files {
			if cases, err := readCases(strings.TrimSpace(f)); err == nil {
				merged = append(merged, cases...)
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

	// Sample across the whole corpus rather than its newest slice: on a palace
	// that holds years, the newest thousand memories are one week of work, and an
	// eval built from them measures recall on recent memory only.
	//
	// Reproducibility comes from the saved case file rather than from a seed: the
	// questions are what a re-run must hold constant, and they are on disk.
	drawers, err := svc.drawers.SampleDrawers(ctx, t.TeamID, c.String("wing"), c.Int("n"))
	if err != nil {
		return nil, "", fmt.Errorf("sample drawers: %w", err)
	}
	if len(drawers) == 0 {
		return nil, "", nil
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
	genURL := c.String("gen-url")
	if strings.TrimSpace(genURL) == "" {
		genURL = cfg2URL(c.String("ollama-url"))
	}
	gen := &questionGen{
		url:     genURL,
		model:   c.String("gen-model"),
		apiKey:  c.String("gen-api-key"),
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

// questionGen asks a local model for a question a given memory answers.
type questionGen struct {
	url     string
	model   string
	apiKey  string
	prompt  string
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
	worst := 0
	for _, m := range report.Arms {
		if m.NotFound > worst {
			worst = m.NotFound
		}
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
	if len(s) > max {
		s = s[:max]
	}
	return s
}
