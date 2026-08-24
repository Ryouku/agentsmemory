package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// A setting is wired only when BOTH halves exist: something puts an operator's
// value into it, and something reads it back out. Either half alone is a knob
// that turns and does nothing, which is this project's most expensive recurring
// defect — an eval arm that was never registered, an IDF coverage function with
// no branch in Search, an embedding backend whose selector lived only in a
// comment, and a compose file advertising a rerank pool the server never read.
//
// Every one of those compiled, and every one had tests that passed, because the
// tests exercised the component instead of the wiring. These two check the wiring
// itself.

// operatorAssigns reports whether a Config field is populated from something an
// operator controls — `c.String("x")`, `c.Duration("x")`, an env lookup — rather
// than from `def.X`, which is the default it is supposed to override.
func operatorAssigns(text, field string) bool {
	for _, m := range regexp.MustCompile(`(?m)^\s*`+regexp.QuoteMeta(field)+`:\s*(.+?),?\s*$`).FindAllStringSubmatch(text, -1) {
		v := strings.TrimSpace(m[1])
		if strings.HasPrefix(v, "def.") || v == "" {
			continue // the default assigning itself is not an operator setting it
		}
		return true
	}
	return false
}

// TestEveryConfigFieldIsPopulatedAndRead fails when a field of config.Config is
// never assigned from the command line, or never read by anything outside the
// config package.
//
// A field nobody assigns is a setting an operator cannot set. A field nobody
// reads is a setting that changes nothing when they do. Both look identical from
// the outside — the program starts, accepts the flag, and behaves exactly as
// before — which is why this has to be mechanical.
func TestEveryConfigFieldIsPopulatedAndRead(t *testing.T) {
	root := repoRoot(t)
	fields := configFields(t, filepath.Join(root, "internal", "config", "config.go"))
	if len(fields) == 0 {
		t.Fatal("found no exported Config fields — this check has stopped checking anything")
	}

	// Assignment means an OPERATOR can set it — the field is populated from a flag
	// accessor, not from the defaults. The check used to be `strings.Contains(text,
	// f+":")`, which `HTTPTimeout: def.HTTPTimeout` satisfies, so a field nothing
	// exposed passed while the failure message below promised "an operator has no
	// way to set it". The message was right and the check was not.
	//
	// Reading: `.Field` appears anywhere outside the config package itself, where
	// the declaration and the defaults naturally mention every name.
	assigned := map[string]bool{}
	read := map[string]bool{}
	for _, path := range goFilesUnder(t, root) {
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("internal", "config")) {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(src)
		for _, f := range fields {
			if operatorAssigns(text, f) {
				assigned[f] = true
			}
			if strings.Contains(text, "."+f) {
				read[f] = true
			}
		}
	}

	var broken []string
	for _, f := range fields {
		switch {
		case !assigned[f] && !read[f]:
			broken = append(broken, f+" is neither populated nor read — it is a field, not a setting")
		case !assigned[f]:
			broken = append(broken, f+" is read but never populated from the command line — an operator has no way to set it")
		case !read[f]:
			broken = append(broken, f+" is populated but never read — setting it changes nothing")
		}
	}
	sort.Strings(broken)
	for _, b := range broken {
		t.Errorf("config.Config.%s", b)
	}
}

// TestEveryFlagIsRead fails when a CLI flag is declared and never consulted.
//
// This is the same defect one layer out: `--fusion` existed as a flag before
// anything read it, and RERANK_TOP_K was set in the shipped compose file for
// months while the server read RERANK_POOL. A declared flag is a promise in the
// help output, and `--help` is documentation like any other.
func TestEveryFlagIsRead(t *testing.T) {
	root := repoRoot(t)
	declared := map[string]token.Position{}
	readNames := map[string]bool{}

	for _, path := range goFilesUnder(t, root) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				// Only a cli.*Flag literal declares a flag. A cli.Command also
				// has a Name, and a command is DISPATCHED rather than read — the
				// first version of this check reported every subcommand in the
				// binary as an unread flag, which is how a gate earns its way
				// into being deleted.
				if !isFlagLiteral(node.Type) {
					return true
				}
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Name" {
						continue
					}
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if name := strings.Trim(lit.Value, `"`); isFlagName(name) {
							declared[name] = fset.Position(lit.Pos())
						}
					}
				}
			case *ast.CallExpr:
				// c.String("flag-name"), c.Int(...), c.Bool(...), and friends.
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || len(node.Args) == 0 {
					return true
				}
				switch sel.Sel.Name {
				case "String", "Int", "Bool", "Float", "Float64", "Duration", "StringSlice", "IsSet":
					if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						readNames[strings.Trim(lit.Value, `"`)] = true
					}
				}
			}
			return true
		})
	}
	if len(declared) == 0 {
		t.Fatal("found no CLI flags — this check has stopped checking anything")
	}

	var unread []string
	for name, pos := range declared {
		if !readNames[name] {
			rel, _ := filepath.Rel(root, pos.Filename)
			unread = append(unread, fmt.Sprintf("%s:%d: --%s is declared but never read — it appears in --help and does nothing", rel, pos.Line, name))
		}
	}
	sort.Strings(unread)
	for _, u := range unread {
		t.Error(u)
	}
}

// isFlagLiteral reports whether a composite literal's type is one of urfave's
// flag types — cli.StringFlag, cli.IntFlag and friends.
func isFlagLiteral(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "cli" && strings.HasSuffix(sel.Sel.Name, "Flag")
}

// isFlagName keeps the check to things shaped like CLI flags: lower-case,
// hyphenated, no spaces. A `Name:` key also appears in unrelated structs.
func isFlagName(s string) bool {
	if s == "" || strings.ContainsAny(s, " _/.") || strings.ToLower(s) != s {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

// configFields lists the exported field names of the Config struct.
func configFields(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Config" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if ast.IsExported(name.Name) {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	sort.Strings(out)
	return out
}

// TestLexNormDefaultsAgree: config spells the default normaliser as a literal
// because internal/config must not import the domain, so the two spellings can
// drift into two different defaults with nothing failing. This is the mechanical
// check that stands in for the import.
func TestLexNormDefaultsAgree(t *testing.T) {
	if got, want := config.Default().LexNorm, palace.DefaultLexNorm; got != want {
		t.Errorf("config.Default().LexNorm = %q but palace.DefaultLexNorm = %q — an operator who "+
			"changes nothing would get a normaliser neither file claims", got, want)
	}
	names := palace.LexNormNames()
	found := false
	for _, n := range names {
		if n == config.Default().LexNorm {
			found = true
		}
	}
	if !found {
		t.Errorf("the default %q is not among the selectable names %v — the default is unreachable "+
			"by anyone who states it explicitly", config.Default().LexNorm, names)
	}
}

// TestRerankBudgetIsShorterThanAnyClientWaits: the rerank budget must be short
// enough that the SERVER gives up first.
//
// applyRerank fails open — a cancelled or failed rerank returns the fused order —
// so a slow cross-encoder should cost ranking quality and nothing else. That path
// can only fire if the server's own budget expires before the caller's. With
// RerankTimeout at 90 seconds it never did: measured 2026-08-21, a pool of 50 on a
// CPU cross-encoder cost ~22 seconds, an independent MCP session's searches timed
// out 3 times out of 3, and each one received NOTHING where a fused page was
// available the whole time.
//
// Ten seconds is not a magic number; it is shorter than any MCP client's patience
// observed so far and longer than the measured worst case at the default pool
// (~4.3s). The property this pins is the ordering, not the value.
func TestRerankBudgetIsShorterThanAnyClientWaits(t *testing.T) {
	d := config.Default()
	const clientPatience = 30 * time.Second
	if d.RerankTimeout >= clientPatience {
		t.Errorf("RerankTimeout is %s, at or beyond the %s a client is assumed to wait — the "+
			"fail-open path in applyRerank can never fire, so a slow reranker returns nothing "+
			"instead of the fused order it already has", d.RerankTimeout, clientPatience)
	}
	// And the budget must actually cover the default pool, or every search degrades.
	worst := time.Duration(d.RerankPool) * 600 * time.Millisecond
	if d.RerankTimeout < worst {
		t.Errorf("RerankTimeout %s is below the measured worst case for a pool of %d (%s at "+
			"600ms/doc) — reranking would be cut off on every search, which is a reranker "+
			"configured and never used", d.RerankTimeout, d.RerankPool, worst)
	}
}

// TestGatedArmMatchesTheShippedDefaults: internal/palace mirrors the shipped
// fusion and closet defaults, because evalstats cannot import the config package.
// A mirror with no check is exactly how supersessionGatedArm came to name a
// pipeline nobody ran — the rule lived in a comment and the comment was not
// executed.
func TestGatedArmMatchesTheShippedDefaults(t *testing.T) {
	d := config.Default()
	rrf := strings.EqualFold(strings.TrimSpace(d.Fusion), "rrf")
	closetOn := d.ClosetBoost > 0

	// With a reranker configured, which is what the full stack ships.
	want := palace.ArmRRFReranked
	switch {
	case rrf:
		want = palace.ArmRRFReranked
	case closetOn:
		want = palace.ArmReranked
	default:
		want = palace.ArmHybridRerank
	}
	if got := palace.SupersessionGatedArm(); got != want {
		t.Errorf("the supersession gate judges %q, but config.Default() (fusion=%q closet=%.2f) "+
			"with a reranker is %q.\n"+
			"  The gate would compare a pipeline nobody runs, and both arms are in the report so "+
			"the lookup succeeds and says nothing.", got, d.Fusion, d.ClosetBoost, want)
	}
}

// TestTheGateAsksTheServiceForItsArm is the check that was missing when
// SupersessionGatedArmFor shipped with no production caller at all.
//
// The selector existed, was correct, and was tested — by a test that called it
// directly. Every real call site read the package-level default instead, so a
// deployment with no RERANK_URL was refused as "degraded" rather than judged on
// ArmRRF, and a linear deployment with a working reranker was silently judged as
// rrf+rerank: the "gate judges a pipeline nobody runs" defect, reintroduced by
// the commit that fixed it.
//
// runEval needs a database, so the wiring cannot be driven from a unit test. It
// is read off the source instead — crude, and it fails when the wire is cut,
// which is the only property that matters here.
func TestTheGateAsksTheServiceForItsArm(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "eval.go"))
	if err != nil {
		t.Fatalf("read eval.go: %v", err)
	}
	if !regexp.MustCompile(`printSupersessionGate\([^)]*SupersessionGatedArmFor\(\)`).Match(src) {
		t.Error("cmd/server/eval.go does not pass the SERVICE's own arm to printSupersessionGate — " +
			"the gate would judge the shipped default whatever this server is configured with")
	}
	if regexp.MustCompile(`palace\.SupersessionGatedArm\(\)`).Match(src) {
		t.Error("cmd/server/eval.go reads palace.SupersessionGatedArm(), the SHIPPED default's arm. " +
			"A running eval must gate on the ranking THIS server serves; the package-level value is the " +
			"pre-registration, not the configuration.")
	}
}

// TestTheGateReadsTheRunsOwnPairRecord fails when the temporal branch rebuilds
// its meta from flags instead of taking the generator's.
//
// This is the #35 defect, and it is a SELECTION defect of the class this repo
// keeps shipping: pair verification worked, the pair record was computed, and it
// was written to disk correctly. The one line that carried it into the run's own
// meta was never written, so `--style temporal --supersession-gate` in a single
// command refused on a file it had just written with five verified pairs in it —
// and advised regenerating with the flag the operator had just used.
//
// Every part was tested. Nothing tested the selection, which is why the gate
// could not answer ADR-004's question by the route anyone would take.
//
// Read off the source because loadOrGenerateCases needs a database, an embedder
// and a generative model. Crude, and it fails when the wire is cut.
func TestTheGateReadsTheRunsOwnPairRecord(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "eval.go"))
	if err != nil {
		t.Fatalf("read eval.go: %v", err)
	}
	const marker = `if c.String("style") == "temporal" {`
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatalf("cannot find the temporal branch of loadOrGenerateCases — this gate reads that "+
			"branch by name and the name changed; re-point it rather than deleting it (looked for %q)", marker)
	}
	// The branch is a handful of lines; a window keeps the check on THIS branch
	// rather than on whatever the file happens to say further down.
	branch := string(src[i:min(i+700, len(src))])

	if !regexp.MustCompile(`generateTemporalCases\([^)]*\)`).MatchString(branch) {
		t.Fatal("the temporal branch no longer calls generateTemporalCases")
	}
	if regexp.MustCompile(`Meta:\s*generatedMeta\(`).MatchString(branch) {
		t.Error("the temporal branch builds caseSource.Meta with generatedMeta(c), which reads FLAGS. " +
			"Only the generator knows how many pairs a judge confirmed, and that record is the " +
			"supersession gate's first precondition — so the gate refuses on the run's own verified " +
			"output and ADR-004's question cannot be answered in one command (#35).")
	}
	if !regexp.MustCompile(`Meta:\s*meta\b`).MatchString(branch) {
		t.Error("the temporal branch does not pass the generator's own meta into caseSource. " +
			"The pair record has to travel with the cases, not only to disk.")
	}
}
