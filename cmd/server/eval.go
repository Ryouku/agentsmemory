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
			&cli.StringFlag{Name: "cases", Usage: "read cases from this JSONL file if it exists, otherwise write the generated ones there"},
			&cli.StringFlag{Name: "gen-model", Value: "qwen2.5-coder:7b", Usage: "Ollama model that writes the questions"},
			&cli.IntFlag{Name: "pool", Value: 50, Usage: "candidates fetched per query; every arm re-orders this same pool"},
			&cli.IntFlag{Name: "seed", Value: 1, Usage: "sampling seed, so a re-run picks the same drawers"},
			&cli.StringFlag{Name: "style", Value: "paraphrase", Usage: "question style: paraphrase (no shared vocabulary — the hard case) or literal (keeps identifiers, like a real developer search)"},
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
	report, err := svc.drawers.Evaluate(ctx, t.TeamID, cases, c.Int("pool"),
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
		if cases, err := readCases(path); err == nil && len(cases) > 0 {
			return cases, "from " + path, nil
		}
	}

	drawers, err := svc.drawers.List(ctx, t.TeamID, c.String("wing"), "", 1000, 0)
	if err != nil {
		return nil, "", fmt.Errorf("list drawers: %w", err)
	}
	if len(drawers) == 0 {
		return nil, "", nil
	}
	// Deterministic sample: a ranking change must be judged on the same drawers
	// as the run before it.
	rng := rand.New(rand.NewSource(int64(c.Int("seed"))))
	rng.Shuffle(len(drawers), func(i, j int) { drawers[i], drawers[j] = drawers[j], drawers[i] })
	if n := c.Int("n"); n < len(drawers) {
		drawers = drawers[:n]
	}

	prompt, style := evalPromptParaphrase, c.String("style")
	if style == "literal" {
		prompt = evalPromptLiteral
	}
	gen := &questionGen{
		url:     cfg2URL(c.String("ollama-url")),
		model:   c.String("gen-model"),
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
			fmt.Fprintf(out, "  [%2d/%2d] failed: %v\n", i+1, len(drawers), err)
			continue
		}
		if q == "" {
			fmt.Fprintf(out, "  [%2d/%2d] empty answer, skipped\n", i+1, len(drawers))
			continue
		}
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s\n", i+1, len(drawers), time.Since(started).Seconds(), firstLineOf(q, 62))
		cases = append(cases, palace.EvalCase{Query: q, Expect: d.ID, Wing: c.String("wing")})
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
	prompt  string
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
