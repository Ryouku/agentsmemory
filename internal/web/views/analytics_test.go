package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// analyticsPages are the three self-contained documents this site serves. Every
// one owns its own <head>, so "add analytics to the layout" is three wirings,
// not one — and a page added later with a fourth head would not be covered by
// any of the tests below. That is exactly what this table is for: it names the
// full set, so a new page is a visible omission rather than a silent gap.
func analyticsPages() map[string]templ.Component {
	return map[string]templ.Component{
		"Layout":        Layout("Test", "user@example.com"),
		"LandingPage":   LandingPage(LandingData{}),
		"SandboxesPage": SandboxesPage(LandingData{}),
	}
}

// withClientID sets the package-level client id for one test and restores it
// afterwards. The id is process config (stamped once by web.New), so a test that
// leaked a value into it would silently arm the tracker for every later test.
func withClientID(t *testing.T, id string) {
	t.Helper()
	prev := OpenPanelClientID
	OpenPanelClientID = id
	t.Cleanup(func() { OpenPanelClientID = prev })
}

// renderPage renders a component to its HTML string.
func renderPage(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// TestAnalyticsAbsentWhenUnconfigured is the privacy guarantee: with no client
// id — a self-hosted install, a dev box, CI — no page may contact OpenPanel at
// all. A regression here would ship a third-party beacon to operators who never
// opted in, which is worse than analytics simply not working.
func TestAnalyticsAbsentWhenUnconfigured(t *testing.T) {
	withClientID(t, "")

	for name, page := range analyticsPages() {
		html := renderPage(t, page)
		if strings.Contains(html, "openpanel") {
			t.Errorf("%s emitted an OpenPanel tag with no client id configured", name)
		}
		if strings.Contains(html, "window.op") {
			t.Errorf("%s emitted the OpenPanel init call with no client id configured", name)
		}
	}
}

// TestAnalyticsPresentOnEveryPage checks the wiring actually reaches all three
// heads. The landing page and /sandboxes are the reason this matters: they are
// separate documents from Layout, so tracking the dashboard alone would leave
// the public front door — the pages analytics exists to measure — unreported.
func TestAnalyticsPresentOnEveryPage(t *testing.T) {
	const clientID = "op-client-1234"
	withClientID(t, clientID)

	for name, page := range analyticsPages() {
		html := renderPage(t, page)
		if !strings.Contains(html, openPanelScriptURL) {
			t.Errorf("%s is missing the OpenPanel SDK tag (%s)", name, openPanelScriptURL)
		}
		if !strings.Contains(html, `clientId:"`+clientID+`"`) {
			t.Errorf("%s did not initialise OpenPanel with the configured client id", name)
		}
		// The snippet belongs in the head, before the document body starts —
		// that is what lets it record a view that navigates away early.
		if head, _, ok := strings.Cut(html, "<body"); !ok || !strings.Contains(head, openPanelScriptURL) {
			t.Errorf("%s put the OpenPanel tag outside <head>", name)
		}
	}
}

// TestAnalyticsClientIDCannotBreakOutOfScript proves the id is escaped rather
// than concatenated raw. templ does not evaluate expressions inside a <script>,
// so the snippet is emitted via templ.Raw — which means the escaping in
// openPanelScript is the only thing standing between a malformed value and
// injected markup. json.Marshal's HTML escaping is what holds that line.
func TestAnalyticsClientIDCannotBreakOutOfScript(t *testing.T) {
	withClientID(t, `</script><img src=x onerror=alert(1)>`)

	html := renderPage(t, Layout("Test", ""))
	if strings.Contains(html, "<img src=x") {
		t.Error("a client id containing markup escaped the <script> tag and injected HTML")
	}
	if strings.Contains(html, "</script><img") {
		t.Error("a client id containing </script> terminated the tag early")
	}
}
