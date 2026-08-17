package web

import (
	"context"
	"encoding/xml"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// testOrigin is the host every discovery request in this file arrives through.
// The sitemaps must echo it back rather than the production constant, because a
// sitemap whose entries sit on a different host than the sitemap itself is a
// cross-submission and gets rejected.
const testOrigin = "http://memory.example"

// discoveryRouter mounts the real route table so these tests fail if a handler
// is written but never wired — the failure mode a handler-only test cannot see.
// Routes only registers handlers, so a zero-value Server is enough; the
// discovery handlers read no Server fields.
func discoveryRouter(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	(&Server{}).Routes(r)
	return r
}

// get issues a GET against the mounted router and returns the recorder.
func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	discoveryRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, testOrigin+path, nil))
	return rec
}

// TestSitemapIndexDelegatesToBothSitemaps pins the shape the whole discovery
// surface depends on: /sitemap.xml is an index, not a urlset, so robots.txt can
// advertise one entry point and still lead a crawler to both sitemaps.
func TestSitemapIndexDelegatesToBothSitemaps(t *testing.T) {
	rec := get(t, "/sitemap.xml")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Fatalf("Content-Type = %q, want application/xml", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), xml.Header) {
		t.Error("body is missing the XML declaration")
	}

	var doc sitemapIndexDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body does not parse as a sitemapindex: %v", err)
	}
	if doc.XMLName.Local != "sitemapindex" {
		t.Fatalf("root element = %q, want sitemapindex", doc.XMLName.Local)
	}
	if doc.XMLName.Space != sitemapNS {
		t.Errorf("namespace = %q, want %q", doc.XMLName.Space, sitemapNS)
	}

	got := []string{}
	for _, s := range doc.Sitemaps {
		got = append(got, s.Loc)
	}
	want := []string{testOrigin + "/pages-sitemap.xml", testOrigin + "/ai-sitemap.xml"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("child sitemaps = %v, want %v", got, want)
	}
}

// TestPagesSitemapCoversContentPagesOnly asserts the page sitemap lists exactly
// the four content pages. The exclusions matter as much as the inclusions: a
// sign-in form in the index competes with the pages that should rank.
func TestPagesSitemapCoversContentPagesOnly(t *testing.T) {
	rec := get(t, "/pages-sitemap.xml")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var doc urlSetDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body does not parse as a urlset: %v", err)
	}
	if doc.XMLName.Local != "urlset" {
		t.Fatalf("root element = %q, want urlset", doc.XMLName.Local)
	}

	got := map[string]bool{}
	for _, u := range doc.URLs {
		got[u.Loc] = true
	}
	for _, p := range contentPages() {
		if !got[testOrigin+p] {
			t.Errorf("page sitemap is missing %s", p)
		}
	}
	if len(doc.URLs) != len(contentPages()) {
		t.Errorf("urlset has %d entries, want %d", len(doc.URLs), len(contentPages()))
	}
	for _, excluded := range []string{"/login", "/register", "/dashboard", "/account"} {
		if got[testOrigin+excluded] {
			t.Errorf("%s must not be advertised to crawlers", excluded)
		}
	}
}

// TestAISitemapCoversEveryBundleDoc holds the AI sitemap to the embedded bundle
// itself, so a document added to internal/web/ai/ can never be published without
// being discoverable.
func TestAISitemapCoversEveryBundleDoc(t *testing.T) {
	rec := get(t, "/ai-sitemap.xml")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var doc urlSetDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body does not parse as a urlset: %v", err)
	}

	got := map[string]bool{}
	for _, u := range doc.URLs {
		got[u.Loc] = true
	}
	entries, err := fs.ReadDir(aiDocs, aiDocRoot)
	if err != nil {
		t.Fatalf("reading the embedded bundle: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		found++
		if !got[testOrigin+"/ai/"+e.Name()] {
			t.Errorf("ai sitemap is missing /ai/%s", e.Name())
		}
	}
	if found == 0 {
		t.Fatal("the embedded bundle holds no .md documents")
	}
	if len(doc.URLs) != found {
		t.Errorf("urlset has %d entries, want %d", len(doc.URLs), found)
	}
}

// TestRobotsAllowsEveryoneAndAdvertisesOneSitemap checks the policy the task
// asked for: allow all, and exactly one Sitemap line, because /sitemap.xml is an
// index that leads to the rest.
func TestRobotsAllowsEveryoneAndAdvertisesOneSitemap(t *testing.T) {
	rec := get(t, "/robots.txt")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}

	sitemapLines := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "Sitemap:") {
			sitemapLines++
			if want := "Sitemap: " + testOrigin + "/sitemap.xml"; line != want {
				t.Errorf("sitemap line = %q, want %q", line, want)
			}
		}
		// "Allow all" is the whole policy — any Disallow would contradict it.
		if strings.HasPrefix(line, "Disallow:") {
			t.Errorf("robots.txt must not disallow anything, found %q", line)
		}
	}
	if sitemapLines != 1 {
		t.Errorf("found %d Sitemap lines, want exactly 1", sitemapLines)
	}

	if !strings.Contains(body, "User-agent: *") || !strings.Contains(body, "Allow: /") {
		t.Error("robots.txt does not allow all user agents")
	}
	// The named AI crawlers are the point of the file for AI indexing; losing one
	// silently narrows reach with no other symptom.
	for _, agent := range []string{"GPTBot", "ClaudeBot", "PerplexityBot", "Google-Extended", "CCBot"} {
		if !strings.Contains(body, "User-agent: "+agent) {
			t.Errorf("robots.txt does not name %s", agent)
		}
	}
}

// TestAIDocServesBundleDocuments covers the happy paths of the OKF bundle: a
// named document, and the bundle root, which OKF §8 says is index.md.
func TestAIDocServesBundleDocuments(t *testing.T) {
	for _, tc := range []struct{ name, path, want string }{
		{"named document", "/ai/landing.md", "type: Product"},
		{"bundle root", "/ai/", "okf_version:"},
		{"bundle index", "/ai/index.md", "okf_version:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, tc.path)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
				t.Fatalf("Content-Type = %q, want text/markdown", ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("body does not contain %q", tc.want)
			}
			if strings.Contains(body, guideBaseURLPlaceholder) {
				t.Errorf("placeholder %q was not substituted", guideBaseURLPlaceholder)
			}
			if !strings.Contains(body, testOrigin) {
				t.Error("request origin not substituted into the document")
			}
		})
	}
}

// TestAIDocRejectsEverythingElse is the security case for the one handler here
// that takes an attacker-controlled path. The wildcard is injected through a chi
// route context rather than a URL so the raw traversal string reaches the handler
// exactly as written, without a router or URL parser normalising it away first.
func TestAIDocRejectsEverythingElse(t *testing.T) {
	for _, tc := range []struct{ name, wildcard string }{
		{"parent traversal", "../guide.go"},
		{"deep traversal", "../../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
		{"non-markdown sibling", "sitemap.go"},
		{"missing extension", "landing"},
		{"unknown document", "does-not-exist.md"},
		{"nested unknown", "sub/dir/landing.md"},
		{"dot segments back to root", "./../../llms.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testOrigin+"/ai/x", nil)
			rc := chi.NewRouteContext()
			rc.URLParams.Add("*", tc.wildcard)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))

			rec := httptest.NewRecorder()
			(&Server{}).handleAIDoc(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d for %q, want 404 — body: %.120s",
					rec.Code, tc.wildcard, rec.Body.String())
			}
		})
	}
}

// TestAIDocTraversalStaysInsideTheBundle is the other half of the security case.
// A traversal that happens to name a document the bundle also has — the bundle
// and its parent directory both hold a claude-guide.md — must serve the BUNDLE's
// copy, because path.Clean collapses the escape against the root before the read.
// The weaker "it returned 200" assertion would pass even if the handler had
// walked out of the embedded FS, so this compares the bytes.
func TestAIDocTraversalStaysInsideTheBundle(t *testing.T) {
	const wildcard = "../claude-guide.md"

	req := httptest.NewRequest(http.MethodGet, testOrigin+"/ai/x", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("*", wildcard)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))

	rec := httptest.NewRecorder()
	(&Server{}).handleAIDoc(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the path collapses onto a real bundle doc)", rec.Code)
	}
	want, err := fs.ReadFile(aiDocs, aiDocRoot+"/claude-guide.md")
	if err != nil {
		t.Fatalf("reading the embedded document: %v", err)
	}
	if got := rec.Body.String(); got != resolveBaseURL(string(want), req) {
		t.Error("traversal served something other than the embedded bundle document")
	}
	// The parent directory's claude-guide.md is the install guide; leaking it here
	// would be the escape this test exists to catch.
	if strings.Contains(rec.Body.String(), "# Install agentsmemory into Claude Code") {
		t.Fatal("traversal escaped the bundle and served internal/web/claude-guide.md")
	}
}

// TestLLMsTxtCoversBundle keeps /llms.txt honest against the bundle it maps.
// The file is authored by hand for the sake of its one-line notes, so nothing but
// a test stops a new document from being left out of it.
func TestLLMsTxtCoversBundle(t *testing.T) {
	rec := get(t, "/llms.txt")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}
	// llmstxt.org requires an H1; the blockquote summary is what makes the file
	// useful to a model that reads nothing else.
	if !strings.HasPrefix(body, "# ") {
		t.Error("llms.txt must open with an H1")
	}
	if !strings.Contains(body, "\n> ") {
		t.Error("llms.txt is missing the blockquote summary")
	}
	for _, p := range aiDocPaths() {
		if !strings.Contains(body, testOrigin+p) {
			t.Errorf("llms.txt does not link %s", p)
		}
	}
}

// TestLLMsFullTxtConcatenatesBundle checks the single-fetch corpus carries every
// document, index first.
func TestLLMsFullTxtConcatenatesBundle(t *testing.T) {
	rec := get(t, "/llms-full.txt")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}
	for _, p := range aiDocPaths() {
		if !strings.Contains(body, "<!-- source: "+p+" -->") {
			t.Errorf("llms-full.txt is missing %s", p)
		}
	}
	if !strings.HasPrefix(body, "<!-- source: /ai/index.md -->") {
		t.Errorf("llms-full.txt must lead with the bundle index, got %.60s", body)
	}
}

// TestBundleIsOKFConformant enforces OKF v0.2 §11: every non-reserved document
// carries parseable frontmatter with a non-empty `type`, and the bundle-root
// index declares the version. This is the one rule a consumer is allowed to
// reject a bundle for, so it is worth a test rather than a review comment.
func TestBundleIsOKFConformant(t *testing.T) {
	entries, err := fs.ReadDir(aiDocs, aiDocRoot)
	if err != nil {
		t.Fatalf("reading the embedded bundle: %v", err)
	}
	sawIndex := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(aiDocs, aiDocRoot+"/"+e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		front, ok := frontmatter(string(raw))
		if !ok {
			t.Errorf("%s has no closed --- frontmatter block", e.Name())
			continue
		}
		if e.Name() == "index.md" {
			// Reserved file: §8 exempts it from `type`, §12 puts okf_version here.
			sawIndex = true
			if !strings.Contains(front, `okf_version: "0.2"`) {
				t.Errorf("index.md must declare okf_version: \"0.2\"")
			}
			continue
		}
		if frontmatterValue(front, "type") == "" {
			t.Errorf("%s has no non-empty `type` — OKF §11 makes it the one mandatory field", e.Name())
		}
	}
	if !sawIndex {
		t.Error("the bundle has no index.md")
	}
}

// frontmatter returns the body of a document's leading --- fenced YAML block.
// A hand-rolled scan avoids pulling a YAML dependency into the web package for a
// conformance check that only ever reads top-level scalar keys.
func frontmatter(doc string) (string, bool) {
	const fence = "---\n"
	if !strings.HasPrefix(doc, fence) {
		return "", false
	}
	rest := doc[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return "", false
	}
	return rest[:end+1], true
}

// frontmatterValue reads a top-level scalar key out of a frontmatter block,
// returning "" when the key is absent, nested, or has an empty value.
func frontmatterValue(front, key string) string {
	for _, line := range strings.Split(front, "\n") {
		if !strings.HasPrefix(line, key+":") {
			continue // indented lines are nested keys, not the top-level one
		}
		return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
	}
	return ""
}
