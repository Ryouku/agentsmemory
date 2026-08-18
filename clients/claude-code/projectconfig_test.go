package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseProjectConfig covers the hand-edited shapes a .aiagentmemory file
// arrives in: comments, blank lines, padding around the '=', a line with no '='
// at all, and a key a newer client might add.
func TestParseProjectConfig(t *testing.T) {
	raw := []byte("# a comment\n\n" +
		"agent = codex \n" +
		"sandbox=acme\n" +
		"args=--model opus --verbose\n" +
		"no-separator-here\n" +
		"future-key=ignored\n")

	cfg := parseProjectConfig(raw)
	if cfg.agent != "codex" {
		t.Errorf("agent = %q, want codex", cfg.agent)
	}
	if cfg.sandbox != "acme" {
		t.Errorf("sandbox = %q, want acme", cfg.sandbox)
	}
	if !equalStrings(cfg.args, []string{"--model", "opus", "--verbose"}) {
		t.Errorf("args = %v, want [--model opus --verbose]", cfg.args)
	}

	if empty := parseProjectConfig(nil); empty.agent != "" || empty.sandbox != "" || empty.args != nil {
		t.Errorf("parseProjectConfig(nil) = %+v, want a zero config", empty)
	}
}

// TestSplitArgs pins the quoting rules. Without them an argument containing a
// space — the reason a flat KEY=VALUE file needs a real splitter — would reach
// the agent as two arguments.
func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"plain", "--model opus", []string{"--model", "opus"}},
		{"collapses runs of space", "  --a   --b\t--c ", []string{"--a", "--b", "--c"}},
		{"double quotes group", `--prompt "be terse" --v`, []string{"--prompt", "be terse", "--v"}},
		{"single quotes group", `--prompt 'be terse'`, []string{"--prompt", "be terse"}},
		{"quote inside a word", `--flag=a" "b`, []string{"--flag=a b"}},
		{"backslash escapes a space", `--path a\ b`, []string{"--path", "a b"}},
		{"empty quoted argument survives", `--flag "" --next`, []string{"--flag", "", "--next"}},
		{"empty line", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitArgs(tc.line); !equalStrings(got, tc.want) {
				t.Errorf("splitArgs(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

// TestArgsRoundTrip checks that what init writes is what load reads back: any
// argument formatArgs has to quote must survive splitArgs unchanged, or a
// recorded flag would drift every time the file is rewritten.
func TestArgsRoundTrip(t *testing.T) {
	args := []string{"--model", "opus", "--prompt", "be terse", "--path", `a"b`, "--back", `c\d`, ""}
	if got := splitArgs(formatArgs(args)); !equalStrings(got, args) {
		t.Errorf("round trip = %q, want %q (via %q)", got, args, formatArgs(args))
	}
}

// TestRenderProjectConfigOmitsSandbox is the regression guard for the whole
// design: the committed file must never carry a sandbox name that init was given,
// because a teammate's sandbox is called something else.
func TestRenderProjectConfigOmitsSandbox(t *testing.T) {
	out := string(renderProjectConfig(projectConfig{agent: "claude", args: []string{"--model", "opus"}}))
	if strings.Contains(out, "sandbox=") {
		t.Errorf("rendered committed config contains a sandbox entry:\n%s", out)
	}
	if !strings.Contains(out, "agent=claude") || !strings.Contains(out, "args=--model opus") {
		t.Errorf("rendered config missing agent or args:\n%s", out)
	}
	// A hand-added sandbox= is still honoured as the lowest-precedence layer, so
	// the renderer must be able to write one when it is explicitly present.
	if out := string(renderProjectConfig(projectConfig{sandbox: "acme"})); !strings.Contains(out, "sandbox=acme") {
		t.Errorf("explicit sandbox not rendered:\n%s", out)
	}
}

// TestResolveLaunchPrecedence pins the layer order. Each case supplies every
// layer at once and names the one that must win, so a reordering breaks exactly
// one assertion instead of silently changing which config launches.
func TestResolveLaunchPrecedence(t *testing.T) {
	full := launchInputs{
		flagSandbox: "from-flag",
		envSandbox:  "from-env",
		registry:    "from-registry",
		local:       projectConfig{sandbox: "from-local"},
		shared:      projectConfig{sandbox: "from-shared"},
	}
	cases := []struct {
		name       string
		strip      func(*launchInputs)
		wantName   string
		wantOrigin string
	}{
		{"flag wins", func(*launchInputs) {}, "from-flag", "--sandbox"},
		{"env is next", func(in *launchInputs) { in.flagSandbox = "" }, "from-env", "$" + sandboxEnvVar},
		{"registry beats both files", func(in *launchInputs) {
			in.flagSandbox, in.envSandbox = "", ""
		}, "from-registry", "~/.sandboxes/" + agentRegistryFile},
		{"local file beats the committed one", func(in *launchInputs) {
			in.flagSandbox, in.envSandbox, in.registry = "", "", ""
		}, "from-local", projectLocalFile},
		{"committed file is the last resort", func(in *launchInputs) {
			in.flagSandbox, in.envSandbox, in.registry, in.local.sandbox = "", "", "", ""
		}, "from-shared", projectConfigFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := full
			tc.strip(&in)
			res, err := resolveLaunch(in)
			if err != nil {
				t.Fatalf("resolveLaunch: %v", err)
			}
			if res.sandbox != tc.wantName || res.origin != tc.wantOrigin {
				t.Errorf("resolveLaunch = (%q from %q), want (%q from %q)",
					res.sandbox, res.origin, tc.wantName, tc.wantOrigin)
			}
		})
	}
}

// TestResolveLaunchFailsLoud checks the two refusals: nothing configured at all,
// and a hand-edited file naming something that is not a legal sandbox — which
// would otherwise reach a filesystem path.
func TestResolveLaunchFailsLoud(t *testing.T) {
	if _, err := resolveLaunch(launchInputs{}); err == nil {
		t.Error("resolveLaunch with no sandbox anywhere returned no error")
	}
	_, err := resolveLaunch(launchInputs{shared: projectConfig{sandbox: "../escape"}})
	if err == nil {
		t.Fatal("resolveLaunch accepted a traversal sandbox name")
	}
	if !strings.Contains(err.Error(), projectConfigFile) {
		t.Errorf("error %q does not name the file the bad value came from", err)
	}
}

// TestResolveLaunchAgentAndArgs covers the non-sandbox halves: the agent falls
// down the same ladder, a personal args list replaces the committed one whole
// rather than merging, and command-line extras land last so they win.
func TestResolveLaunchAgentAndArgs(t *testing.T) {
	in := launchInputs{
		registry:  "acme",
		shared:    projectConfig{agent: "claude", args: []string{"--model", "opus"}},
		local:     projectConfig{agent: "codex", args: []string{"--model", "gpt"}},
		extraArgs: []string{"--verbose"},
	}
	res, err := resolveLaunch(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.agent != "codex" {
		t.Errorf("agent = %q, want codex (local overrides committed)", res.agent)
	}
	if !equalStrings(res.args, []string{"--model", "gpt", "--verbose"}) {
		t.Errorf("args = %q, want [--model gpt --verbose]", res.args)
	}

	in.flagAgent = "pi"
	if res, _ := resolveLaunch(in); res.agent != "pi" {
		t.Errorf("agent = %q, want pi (--agent overrides every file)", res.agent)
	}

	// With no personal list, the committed one is used verbatim plus extras.
	in.local.args = nil
	res, err = resolveLaunch(in)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.args, []string{"--model", "opus", "--verbose"}) {
		t.Errorf("args = %q, want the committed list plus extras", res.args)
	}
	// The recorded slice must not be aliased: appending extras to it would
	// corrupt the caller's parsed config on a second resolve.
	if !equalStrings(in.shared.args, []string{"--model", "opus"}) {
		t.Errorf("resolveLaunch mutated the source args: %q", in.shared.args)
	}
}

// TestFindProjectConfig checks the upward walk that lets `load` run from any
// directory inside a project: the nearest level holding either file wins, and
// both files are read from that same level so a personal override can never be
// paired with a shared file from a different directory.
// TestResolveLaunchWingPrecedence pins the wing ladder: the shell's variable
// beats the personal file, which beats the committed one. An unresolved wing must
// stay EMPTY rather than being invented here — the memory protocol derives one
// from the git remote, and a guess made here would silently outrank it.
func TestResolveLaunchWingPrecedence(t *testing.T) {
	base := launchInputs{registry: "acme"} // any sandbox, so resolveLaunch succeeds

	cases := []struct {
		name string
		in   launchInputs
		want string
	}{
		{"env wins", launchInputs{registry: "acme",
			envWing: "wing_env",
			local:   projectConfig{wing: "wing_local"},
			shared:  projectConfig{wing: "wing_shared"}}, "wing_env"},
		{"personal file over committed", launchInputs{registry: "acme",
			local:  projectConfig{wing: "wing_local"},
			shared: projectConfig{wing: "wing_shared"}}, "wing_local"},
		{"committed file", launchInputs{registry: "acme",
			shared: projectConfig{wing: "wing_shared"}}, "wing_shared"},
		{"nothing recorded", base, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := resolveLaunch(c.in)
			if err != nil {
				t.Fatalf("resolveLaunch: %v", err)
			}
			if res.wing != c.want {
				t.Errorf("wing = %q, want %q", res.wing, c.want)
			}
		})
	}
}

// TestProjectConfigWingRoundTrip checks the committed file carries the wing back
// out again: teammates share a wing, so unlike the sandbox name it belongs in the
// file everyone clones.
func TestProjectConfigWingRoundTrip(t *testing.T) {
	out := renderProjectConfig(projectConfig{agent: "claude", wing: "wing_zeus"})
	if !strings.Contains(string(out), "wing=wing_zeus") {
		t.Fatalf("rendered config missing the wing:\n%s", out)
	}
	if got := parseProjectConfig(out); got.wing != "wing_zeus" {
		t.Errorf("round-tripped wing = %q, want wing_zeus", got.wing)
	}
}

func TestFindProjectConfig(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "internal", "web")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, projectConfigFile, "agent=claude\nargs=--model opus\n")
	write(root, projectLocalFile, "sandbox=mine\n")

	shared, local, dir := findProjectConfig(deep)
	if dir != root {
		t.Errorf("findProjectConfig(%q) dir = %q, want %q", deep, dir, root)
	}
	if !equalStrings(shared.args, []string{"--model", "opus"}) || local.sandbox != "mine" {
		t.Errorf("walked-up config = (%+v, %+v), want the root's files", shared, local)
	}

	// A nearer file shadows the ancestor entirely, including its args.
	write(filepath.Join(root, "internal"), projectConfigFile, "agent=codex\n")
	if s, _, d := findProjectConfig(deep); d != filepath.Join(root, "internal") || s.agent != "codex" || len(s.args) != 0 {
		t.Errorf("nearest config not used: dir=%q cfg=%+v", d, s)
	}

	if _, _, d := findProjectConfig(t.TempDir()); d != "" {
		t.Errorf("findProjectConfig with no files anywhere returned dir %q, want empty", d)
	}
}

// TestLookupAgentRegistry covers the registry read: nearest-ancestor matching so
// one parent-directory entry covers a whole tree of repos, per-component matching
// so a lookalike sibling cannot be captured, and the reason entries split on the
// last '=' — a project path may contain one, a sandbox name may not.
func TestLookupAgentRegistry(t *testing.T) {
	raw := []byte("# header\n\n" +
		"/home/m/code=work\n" +
		"/home/m/code/one=alpha\n" +
		"/home/m/we=ird/two = beta \n" +
		"malformed-line\n")

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"exact entry", "/home/m/code/one", "alpha"},
		{"nearest wins over the parent", "/home/m/code/one/internal/web", "alpha"},
		{"parent covers an unlisted repo", "/home/m/code/other", "work"},
		{"parent covers a deep path", "/home/m/code/other/a/b/c", "work"},
		{"path containing =", "/home/m/we=ird/two", "beta"},
		{"outside every entry", "/home/m/elsewhere", ""},
		{"sibling with a shared prefix is not captured", "/home/m/codex", ""},
		{"the parent itself is not covered by its child", "/home/m", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lookupAgentRegistry(raw, tc.dir); got != tc.want {
				t.Errorf("lookupAgentRegistry(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}

// TestUpsertAgentRegistry checks that re-running init updates a project's entry
// in place. Appending instead would leave a stale line that the first-match
// lookup returns forever.
func TestUpsertAgentRegistry(t *testing.T) {
	fresh := upsertAgentRegistry(nil, "/home/m/proj", "acme")
	if got := lookupAgentRegistry(fresh, "/home/m/proj"); got != "acme" {
		t.Fatalf("new registry did not record the entry: %q", fresh)
	}
	if !strings.HasPrefix(string(fresh), "#") {
		t.Errorf("new registry lacks its explanatory header:\n%s", fresh)
	}

	updated := upsertAgentRegistry(fresh, "/home/m/proj", "other")
	if got := lookupAgentRegistry(updated, "/home/m/proj"); got != "other" {
		t.Errorf("re-init did not update the entry: %q", updated)
	}
	if n := strings.Count(string(updated), "/home/m/proj="); n != 1 {
		t.Errorf("re-init left %d entries for the project, want 1:\n%s", n, updated)
	}

	second := upsertAgentRegistry(updated, "/home/m/other", "beta")
	if lookupAgentRegistry(second, "/home/m/proj") != "other" ||
		lookupAgentRegistry(second, "/home/m/other") != "beta" {
		t.Errorf("adding a second project disturbed the first:\n%s", second)
	}
}
