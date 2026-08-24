package web

import (
	"embed"
	"encoding/xml"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// aiDocs holds the Open Knowledge Format (OKF v0.2) bundle served under /ai/ —
// the machine-readable mirror of the public pages, so an answer engine can read
// what the product is without parsing HTML. Embedded for the same reason as the
// install guides: the binary stays self-contained and the docs ship with the
// build that describes them.
//
//go:embed ai
var aiDocs embed.FS

// llmsIndex is the /llms.txt document (llmstxt.org convention): a hand-written
// map of this site for language models, listing every bundle document and guide
// as a Markdown link list. It is authored rather than generated because the
// value of llms.txt is the one-line notes that say which document answers which
// question — TestLLMsTxtCoversBundle holds it in sync with aiDocs.
//
//go:embed llms.txt
var llmsIndex string

// aiDocRoot is the directory inside aiDocs holding the OKF bundle, and doubles
// as the URL prefix the bundle is served under (/ai/...). Keeping one constant
// for both is what makes aiDocPaths a straight rename of the embedded names.
const aiDocRoot = "ai"

// sitemapNS is the sitemaps.org 0.9 namespace, required on both the index and
// the urlset documents.
const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

// contentPages are the public URLs offered to crawlers. Auth surfaces (/login,
// /register) are deliberately absent: they carry no search value and indexing a
// sign-in form only competes with the pages that do. The list is a function
// rather than a package var so no caller can mutate the shared slice.
func contentPages() []string {
	return []string{"/", "/sandboxes", "/claude-guide", "/windows-guide", "/bootstrap-memory"}
}

// sitemapIndexDoc is a sitemaps.org <sitemapindex>: a sitemap that lists other
// sitemaps. The schema does not allow an index and a urlset in one document,
// which is why /sitemap.xml delegates to /pages-sitemap.xml and /ai-sitemap.xml
// instead of listing pages itself — robots.txt then needs to advertise only the
// one entry point.
type sitemapIndexDoc struct {
	XMLName  xml.Name       `xml:"sitemapindex"`
	Xmlns    string         `xml:"xmlns,attr"`
	Sitemaps []sitemapEntry `xml:"sitemap"`
}

// sitemapEntry is one child sitemap reference inside a sitemapIndexDoc.
type sitemapEntry struct {
	Loc string `xml:"loc"`
}

// urlSetDoc is a sitemaps.org <urlset>: a flat list of indexable URLs.
type urlSetDoc struct {
	XMLName xml.Name   `xml:"urlset"`
	Xmlns   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

// urlEntry is one indexable URL. Only <loc> is emitted: <lastmod> would have to
// be invented (embed.FS reports no modification times) and search engines ignore
// a lastmod they cannot trust, while <changefreq> and <priority> are ignored
// outright by Google. An honest minimum beats decorative metadata.
type urlEntry struct {
	Loc string `xml:"loc"`
}

// aiDocPaths returns the site-absolute path of every document in the OKF bundle,
// in sorted order. It reads the embedded FS rather than a hand-kept list so a new
// .md file appears in /ai-sitemap.xml by existing — the failure mode of a
// hand-kept list is a document nothing ever links to.
func aiDocPaths() []string {
	entries, err := fs.ReadDir(aiDocs, aiDocRoot)
	if err != nil {
		return nil // unreachable: the directory is embedded at build time
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		paths = append(paths, "/"+aiDocRoot+"/"+e.Name())
	}
	sort.Strings(paths)
	return paths
}

// handleSitemapIndex serves /sitemap.xml as the single entry point advertised in
// robots.txt, pointing at the human-page sitemap and the AI bundle sitemap.
func (s *Server) handleSitemapIndex(w http.ResponseWriter, r *http.Request) {
	base := requestBaseURL(r)
	writeXML(w, sitemapIndexDoc{
		Xmlns: sitemapNS,
		Sitemaps: []sitemapEntry{
			{Loc: base + "/pages-sitemap.xml"},
			{Loc: base + "/ai-sitemap.xml"},
		},
	})
}

// handlePagesSitemap serves /pages-sitemap.xml: the public HTML and Markdown
// pages a search engine should index.
func (s *Server) handlePagesSitemap(w http.ResponseWriter, r *http.Request) {
	writeXML(w, urlSet(requestBaseURL(r), contentPages()))
}

// handleAISitemap serves /ai-sitemap.xml: every document of the OKF bundle,
// listed separately from the page sitemap so an AI crawler can fetch the
// machine-readable corpus without walking the marketing pages.
func (s *Server) handleAISitemap(w http.ResponseWriter, r *http.Request) {
	writeXML(w, urlSet(requestBaseURL(r), aiDocPaths()))
}

// urlSet builds a <urlset> from site-absolute paths resolved against base. The
// entries must share the sitemap's own host, or a crawler rejects them as a
// cross-submission — which is why these URLs are built from the request origin
// and not from the fixed production constant the canonical <link> tags use.
func urlSet(base string, paths []string) urlSetDoc {
	urls := make([]urlEntry, 0, len(paths))
	for _, p := range paths {
		urls = append(urls, urlEntry{Loc: base + p})
	}
	return urlSetDoc{Xmlns: sitemapNS, URLs: urls}
}

// writeXML renders a sitemap document with the XML declaration search engines
// expect. It marshals to a buffer first so an encoding failure can still become a
// 500 — once the header is written, it could only be logged.
func writeXML(w http.ResponseWriter, doc any) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		http.Error(w, "could not render sitemap", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

// robotsTxt is the crawler policy. Everything is allowed, and the AI crawlers are
// named explicitly even though `User-agent: *` already covers them: a growing
// number of sites block them by default, so an explicit Allow states intent
// rather than leaving it to a wildcard a cautious crawler may read narrowly.
// Only /sitemap.xml is advertised — it is an index, so the other two sitemaps are
// reached through it.
const robotsTxt = `# https://www.robotstxt.org/robotstxt.html
User-agent: *
Allow: /

# AI crawlers and answer engines, allowed explicitly.
User-agent: GPTBot
User-agent: OAI-SearchBot
User-agent: ChatGPT-User
User-agent: ClaudeBot
User-agent: Claude-User
User-agent: Claude-SearchBot
User-agent: anthropic-ai
User-agent: PerplexityBot
User-agent: Perplexity-User
User-agent: Google-Extended
User-agent: Applebot-Extended
User-agent: Amazonbot
User-agent: meta-externalagent
User-agent: Bytespider
User-agent: CCBot
User-agent: cohere-ai
User-agent: Diffbot
User-agent: Timpibot
User-agent: YouBot
Allow: /

# Machine-readable mirrors of these pages, for language models:
#   {{BASE_URL}}/llms.txt        — a map of this site
#   {{BASE_URL}}/llms-full.txt   — the whole knowledge bundle in one fetch
#   {{BASE_URL}}/ai/index.md     — the same bundle, one document per page (OKF v0.2)

Sitemap: {{BASE_URL}}/sitemap.xml
`

// handleRobots serves /robots.txt with the sitemap line pointing at the host the
// request arrived through, so a staging or self-hosted deployment advertises its
// own sitemap rather than the production one.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	servePlainText(w, r, robotsTxt)
}

// handleLLMsTxt serves /llms.txt, the llmstxt.org entry point: a short Markdown
// map telling a language model which document answers which question.
func (s *Server) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	servePlainText(w, r, llmsIndex)
}

// handleLLMsFullTxt serves /llms-full.txt: every OKF document concatenated, so a
// model can take the whole corpus in one request instead of crawling the bundle
// document by document. It is generated from the embedded bundle rather than
// stored, so it can never drift from the documents it concatenates.
func (s *Server) handleLLMsFullTxt(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	// index.md leads: it is the bundle's table of contents, so a model reading
	// top-down learns the shape before the detail. fs.ReadDir would sort it into
	// the middle alphabetically.
	for _, p := range orderedAIDocPaths() {
		doc, err := fs.ReadFile(aiDocs, aiDocRoot+"/"+path.Base(p))
		if err != nil {
			continue // unreachable: the path came from the same embedded FS
		}
		b.WriteString("<!-- source: " + p + " -->\n\n")
		b.Write(doc)
		b.WriteString("\n\n")
	}
	servePlainText(w, r, b.String())
}

// orderedAIDocPaths is aiDocPaths with the bundle index first, the reading order
// /llms-full.txt needs.
func orderedAIDocPaths() []string {
	const index = "/" + aiDocRoot + "/index.md"
	ordered := []string{}
	rest := []string{}
	for _, p := range aiDocPaths() {
		if p == index {
			ordered = append(ordered, p)
			continue
		}
		rest = append(rest, p)
	}
	return append(ordered, rest...)
}

// handleAIDoc serves one document of the OKF bundle as Markdown. The requested
// name is attacker-controlled, so it is cleaned to a rooted path (which collapses
// any ..) and required to end in .md before it reaches the embedded FS — which
// rejects traversal on its own terms too, since io/fs treats a path containing ..
// as invalid. A miss is a plain 404: the bundle is a fixed set of documents, and
// distinguishing "no such document" from "not a document" would only describe the
// embedded layout to a prober.
func (s *Server) handleAIDoc(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+chi.URLParam(r, "*")), "/")
	if name == "" {
		name = "index.md" // /ai/ is the bundle root, per OKF §8
	}
	if !strings.HasSuffix(name, ".md") {
		http.NotFound(w, r)
		return
	}
	doc, err := fs.ReadFile(aiDocs, aiDocRoot+"/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveGuide(w, r, string(doc))
}

// servePlainText writes a discovery document as text/plain with {{BASE_URL}}
// resolved. Plain text rather than text/markdown so the .txt files open in a
// browser instead of downloading — these are meant to be readable by a human
// checking the deployment as well as by a crawler.
func servePlainText(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(resolveBaseURL(body, r)))
}
