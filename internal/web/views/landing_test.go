package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestLandingPageLinksClaudeGuide guards the two additions the landing page must
// carry: a visible link to /claude-guide, and the copy-paste "let Claude install
// it" prompt (with its independent _copiedPrompt copy signal). Rendering the whole
// page verifies the markup an anonymous visitor actually receives.
func TestLandingPageLinksClaudeGuide(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `href="/claude-guide"`) {
		t.Error("landing page has no link to /claude-guide")
	}
	if !strings.Contains(html, "prompt for Claude") {
		t.Error("landing page is missing the copy-paste prompt block")
	}
	// The prompt itself must point the agent at the guide.
	if !strings.Contains(html, claudeGuideURL) {
		t.Errorf("prompt does not reference the guide URL %q", claudeGuideURL)
	}
	// The prompt's Copy button must use its own signal, not the install one-liner's.
	if !strings.Contains(html, "_copiedPrompt") {
		t.Error("prompt copy button is missing its independent _copiedPrompt signal")
	}
}

// TestLandingPageLinksWindowsGuide is the sibling guard for the no-CLI route.
// Every visitor on Windows, or in VS Code / Cursor / Claude Desktop anywhere, has
// no installer to run, so losing this block silently strands them on a page whose
// every other path is a bash one-liner. The signal assertion is the real risk:
// datastar signals are global to the page, so reusing _copiedPrompt here would
// make one Copy button flash both blocks.
func TestLandingPageLinksWindowsGuide(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `href="/windows-guide"`) {
		t.Error("landing page has no link to /windows-guide")
	}
	if !strings.Contains(html, windowsGuideURL) {
		t.Errorf("prompt does not reference the guide URL %q", windowsGuideURL)
	}
	if !strings.Contains(html, "_copiedNoCLI") {
		t.Error("no-CLI copy button is missing its independent _copiedNoCLI signal")
	}
	// The signal has to be declared on the page too, or data-show never resolves.
	if !strings.Contains(landingSignals(), "_copiedNoCLI") {
		t.Error("_copiedNoCLI is not declared in landingSignals()")
	}
}

// TestLandingPageArguesCost guards the pricing-transparency band: the page must
// argue *why* it costs (real GPU compute + electricity for embeddings), and the
// same argument must also reach the schema.org FAQ so AI answer engines can cite
// it. Rendering the whole page verifies what an anonymous visitor receives.
func TestLandingPageArguesCost(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	// The cost-rationale band and its argument must be on the page.
	if !strings.Contains(html, "Why it costs") {
		t.Error("landing page is missing the 'Why it costs' band")
	}
	if !strings.Contains(html, "GPU") {
		t.Error("cost argument does not mention the GPU compute cost")
	}
	if !strings.Contains(html, "bge-m3") {
		t.Error("cost argument does not name the embedding model that drives the cost")
	}

	// The same argument must feed the FAQ — which double-feeds the JSON-LD — so
	// the reasoning is citable, not just visible.
	if !strings.Contains(html, "Why does agent memory cost money?") {
		t.Error("landing FAQ is missing the 'why it costs' question (GEO surface)")
	}
	// json.Marshal HTML-escapes the ampersand in the FAQ JSON-LD, proving the same
	// question also rendered inside the schema.org <script>.
	if !strings.Contains(html, "application/ld+json") ||
		!strings.Contains(html, "Why does agent memory cost money?") {
		t.Error("why-it-costs FAQ did not reach the schema.org JSON-LD")
	}
}

// TestLandingPageDocumentsProjectLaunch guards the init/load story, whose whole
// point is a split a visitor cannot guess: the agent and flags are committed, the
// sandbox name is not. If the page ever implies the sandbox name travels in the
// repository, it is documenting a design we deliberately rejected.
func TestLandingPageDocumentsProjectLaunch(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"aiagentmemory init --sandbox acme -- --model opus", // the install-breakdown card
		".aiagentmemory", // the file that is committed
		"Your sandbox name stays on your machine", // …and what is not
		"Everyone then runs load",                 // why the split pays off
	} {
		if !strings.Contains(html, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}

	// The command reference lists both commands, with <name> HTML-escaped.
	if !strings.Contains(html, "aiagentmemory init --sandbox &lt;name&gt;") {
		t.Error("command reference is missing the init row")
	}
	if !strings.Contains(html, "aiagentmemory load") {
		t.Error("command reference is missing the load row")
	}

	// The teammate question is the one a visitor actually has, so it must also
	// reach the FAQ — which double-feeds the schema.org JSON-LD and is therefore
	// the page's most citable surface.
	const q = "Do my teammates need my sandbox name?"
	if strings.Count(html, q) < 2 {
		t.Errorf("%q should appear in both the FAQ accordion and the JSON-LD; found %d occurrence(s)",
			q, strings.Count(html, q))
	}
	if !strings.Contains(html, "application/ld+json") {
		t.Error("landing page is missing the schema.org JSON-LD block")
	}
}

// TestLandingPageDocumentsInheritFlags guards the answer to "a sandbox means
// starting from nothing and logging in again": --copy and --shared-auth must be
// documented on the landing page itself, not only on the /sandboxes guide, since
// this is where a visitor decides whether to install at all.
func TestLandingPageDocumentsInheritFlags(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	// Both flags reach the page, and each says what it does rather than just
	// appearing in a command.
	for _, want := range []string{
		"--copy",
		"--shared-auth",
		"logins, MCP servers, plugins",
		"one login serves every sandbox",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}

	// The card carries the composed command: the installer seeds with --copy and
	// then links credentials, so the two are documented together, not as rivals.
	if !strings.Contains(html, "aiagentmemory install --sandbox acme --copy --shared-auth") {
		t.Error("install breakdown is missing the composed --copy --shared-auth command")
	}

	// …and the command reference lists each flag on its own line.
	if !strings.Contains(html, "aiagentmemory install --sandbox &lt;name&gt; --copy") {
		t.Error("command reference is missing the --copy row")
	}
	if !strings.Contains(html, "aiagentmemory install --sandbox &lt;name&gt; --shared-auth") {
		t.Error("command reference is missing the --shared-auth row")
	}

	// The copy must not overpromise: --copy leaves the bulk behind and never
	// clobbers an existing sandbox, which is the invariant the installer holds.
	if !strings.Contains(html, "nothing already there is overwritten") {
		t.Error("landing page does not state the never-overwrite invariant")
	}
}

// TestLandingInstallBuilder guards the install picker: the tabs, the agent and
// name inputs, the flag checkboxes and the assembled command must all reach the
// page, and — the part worth a test — the command a visitor *sees* and the command
// the Copy button *writes* must be the same expression. Those two drifting apart
// is the one failure a copy button cannot survive, and it is invisible on screen.
func TestLandingInstallBuilder(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	page := buf.String()

	// Both tabs, each wired to the _mode signal the command expression reads.
	for _, m := range landingInstallModes() {
		if !strings.Contains(page, esc(landingBuilder().SetMode(m.Key))) {
			t.Errorf("install builder has no tab setting _mode to %q", m.Key)
		}
		if !strings.Contains(page, esc(m.Hint)) {
			t.Errorf("tab %q renders no hint explaining what it does", m.Key)
		}
	}

	// The agent picker offers every kit plus the multi-agent sandbox.
	for _, a := range sandboxAgents() {
		if !strings.Contains(page, `<option value="`+a.Key+`">`) {
			t.Errorf("agent picker is missing the %s option", a.Name)
		}
	}
	if !strings.Contains(page, `<option value="all">`) {
		t.Error("agent picker is missing the all-agents option")
	}

	// The sandbox name is typed, not edited into a command by hand.
	if !strings.Contains(page, `data-bind="_sbname"`) {
		t.Error("install builder has no sandbox-name input bound to _sbname")
	}
	if !strings.Contains(page, `placeholder="`+landingNameFallback+`"`) {
		t.Errorf("name input does not show the %q fallback as its placeholder", landingNameFallback)
	}

	// Every flag is discoverable as a checkbox, with what it does beside it —
	// the reason the builder exists rather than a static one-liner plus docs.
	for _, o := range landingInstallOpts() {
		if !strings.Contains(page, `data-bind="`+o.Signal+`"`) {
			t.Errorf("flag %s has no checkbox bound to %s", o.Flag, o.Signal)
		}
		if !strings.Contains(page, esc(o.Desc)) {
			t.Errorf("flag %s is offered without explaining what it does", o.Flag)
		}
	}
	// --copy and --shared-auth are rejected by the installer without --sandbox,
	// so their rows must be gated on the project tab rather than always shown.
	// Now that every control comes from the shared components, the gate is an
	// interpolated value and templ escapes it. Five things are gated on the
	// project tab: its own hint line, the name field, the two sandbox-only flags,
	// and the launch strip.
	gate := `data-show="` + esc(landingBuilder().ModeIs("project")) + `"`
	if got, want := strings.Count(page, gate), 5; got != want {
		t.Errorf("controls gated on the project tab: got %d, want %d", got, want)
	}

	// The displayed command and the copied command are one expression.
	b := landingBuilder()
	expr := b.InstallExpr()
	if !strings.Contains(page, `data-text="`+esc(expr)+`"`) {
		t.Error("the command block does not render the assembled install expression")
	}
	if !strings.Contains(page, esc(clipboardExprJS(expr, "_copiedInstall"))) {
		t.Error("the Copy button does not write the same expression the block displays")
	}
	// Before datastar boots the block must still read as a real command.
	if !strings.Contains(page, b.Default()) {
		t.Error("the command block has no server-rendered default")
	}
	// --global is what makes the Global tab honest: without it the installer
	// stops and asks which mode you wanted.
	if !strings.Contains(page, "--global") {
		t.Error("the default command omits --global, so the Global tab would still prompt")
	}

	// Installing a sandbox is half the answer; the strip says how to work in it.
	for _, s := range b.LaunchSteps() {
		if !strings.Contains(page, `data-text="`+esc(s.Expr)+`"`) {
			t.Errorf("launch strip does not render the %q command", s.Key)
		}
		if !strings.Contains(page, s.Text) {
			t.Errorf("launch step %q has no server-rendered fallback", s.Key)
		}
		if !strings.Contains(page, esc(s.Desc)) {
			t.Errorf("launch step %q is shown without saying when to use it", s.Key)
		}
	}
	// run/init must never carry a value resolveAgentKit rejects.
	for _, bad := range []string{"aiagentmemory run --agent all", "aiagentmemory init --sandbox &lt;name&gt; --agent all"} {
		if strings.Contains(page, bad) {
			t.Errorf("page renders %q, which the launcher rejects", bad)
		}
	}
}

// TestLandingInstallStatesTheTokenStep guards the one step the install command
// cannot carry. resolveToken prompts, and a blank answer makes the installer print
// "no token provided — skipping agentsmemory MCP" and finish anyway — so a visitor
// who meets that prompt empty-handed gets a successful install with no memory
// attached. The page has to say so, and has to point somewhere a token comes from,
// which differs for a signed-in visitor and an anonymous one.
func TestLandingInstallStatesTheTokenStep(t *testing.T) {
	render := func(d LandingData) string {
		var buf bytes.Buffer
		if err := LandingPage(d).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render: %v", err)
		}
		return buf.String()
	}

	for _, tc := range []struct {
		name      string
		data      LandingData
		wantLink  string
		otherLink string
	}{
		{"anonymous", LandingData{}, `<a href="/register">Create a free workspace</a>`, "Copy yours from the"},
		{"signed in", LandingData{SignedIn: true}, `<a href="/dashboard">dashboard</a>`, "Create a free workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := render(tc.data)
			if !strings.Contains(page, "It will ask for your workspace token.") {
				t.Error("install section never mentions that the installer asks for a token")
			}
			if !strings.Contains(page, tc.wantLink) {
				t.Errorf("token note does not point %s visitors anywhere to get one (want %s)", tc.name, tc.wantLink)
			}
			if strings.Contains(page, tc.otherLink) {
				t.Errorf("token note shows the wrong audience's copy (%q) to a %s visitor", tc.otherLink, tc.name)
			}
			// The consequence is the part a visitor cannot infer from the prompt.
			if !strings.Contains(page, "the memory itself stays") {
				t.Error("token note does not say a blank answer leaves memory disconnected")
			}
			// …and the non-interactive escape hatch is named.
			if !strings.Contains(page, "AGENTSMEMORY_TOKEN") {
				t.Error("page never names the env var that skips the prompt")
			}
		})
	}

	// The command reference must carry --token too, with the same consequence.
	page := render(LandingData{})
	if !strings.Contains(page, "aiagentmemory install --token &lt;key&gt;") {
		t.Error("command reference is missing the --token row")
	}
	if !strings.Contains(page, "the memory MCP is not registered") {
		t.Error("--token row does not state what happens without a token")
	}
}

// TestFreeQuotaIsStatedOnce guards the figure that already went stale once. Four
// surfaces advertise the Free plan's allowance, and when the plan changed all
// four kept quoting the old number because each spelled it out in its own words.
// They now read freeRequestsPerMonth, so this test renders the two public pages
// that quote it and fails if the literal old figure reappears anywhere — which is
// what a copy-paste of the previous wording would look like.
func TestFreeQuotaIsStatedOnce(t *testing.T) {
	pages := map[string]func() (string, error){
		"landing": func() (string, error) {
			var buf bytes.Buffer
			err := LandingPage(LandingData{}).Render(context.Background(), &buf)
			return buf.String(), err
		},
		"register": func() (string, error) {
			var buf bytes.Buffer
			err := RegisterPage(AuthData{}).Render(context.Background(), &buf)
			return buf.String(), err
		},
	}
	for name, render := range pages {
		html, err := render()
		if err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		if !strings.Contains(html, freeRequestsPerMonth) {
			t.Errorf("%s page does not state the free quota %q", name, freeRequestsPerMonth)
		}
		// The stale figure, in the exact form every surface used to carry it.
		if strings.Contains(html, "10,000") {
			t.Errorf("%s page still advertises the old 10,000 request quota", name)
		}
	}

	// The FAQ answer feeds schema.org too, so an answer engine can quote it.
	if !strings.Contains(landingJSONLD(), freeRequestsPerMonth) {
		t.Error("structured data does not carry the free quota")
	}
}
