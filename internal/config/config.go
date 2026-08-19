// Package config loads the agentsmemory server configuration from CLI flags and
// environment variables. It is intentionally tiny: the SaaS state lives in the
// database, so config only carries process-level wiring (listen address, the
// SQLite path, and the Qdrant/Ollama endpoints decided as day-one defaults).
package config

import (
	"net"
	"path/filepath"
	"strings"
	"time"
)

// Vector backend selection. SQLite is always the durable source of truth; this
// chooses what answers searches (decision 2026-06-26: "sqlite as source of
// truth", "qdrant for search").
const (
	// VectorBackendSQLite makes the SQLite source of truth also serve
	// brute-force search — zero external services, ideal for a dev box.
	VectorBackendSQLite = "sqlite"
	// VectorBackendQdrant keeps SQLite as the source of truth and adds Qdrant
	// as the search index (the production path).
	VectorBackendQdrant = "qdrant"
	// VectorBackendChromem keeps SQLite as the source of truth and adds an
	// embedded chromem-go index, held in memory and persisted to a directory
	// beside the database file. It is a real vector index with no second
	// service to run, which is why --local and the Docker stack default to it
	// (2026-08-16: self-hosted must be one process, one volume).
	VectorBackendChromem = "chromem"
)

// ChromemPath returns the directory the embedded chromem index lives in for a
// given SQLite path: agentsmemory.db -> agentsmemory.chromem, /data/x.db ->
// /data/x.chromem.
//
// Deriving it from the database path rather than exposing a separate flag keeps
// the index in the same directory — and so inside the same Docker volume and the
// same backup — as the source of truth it is derived from. Two databases in one
// directory therefore get two index directories, never a shared one.
func ChromemPath(dbPath string) string {
	return strings.TrimSuffix(dbPath, filepath.Ext(dbPath)) + ".chromem"
}

// LocalAddr is the listen address --local defaults to. Local mode serves an
// unauthenticated /mcp, so the default must not be reachable from the network:
// binding the loopback interface keeps "self-hosted" meaning "this machine"
// rather than "everyone on this wifi". An operator can still override it (to run
// behind a reverse proxy or a private overlay), which is why this is a default
// and not a hard constraint — see IsLoopback for the warning path.
const LocalAddr = "127.0.0.1:8080"

// IsLoopback reports whether a listen address binds only the loopback interface.
// It exists so local mode can warn when its unauthenticated endpoint is about to
// be exposed beyond this machine. A blank host — the ":8080" form — binds every
// interface and is therefore NOT loopback, which is the case most likely to
// surprise someone who overrode the address without thinking about auth.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Not a host:port at all (e.g. a bare port). Treat it as unknown rather
		// than safe: an unparseable address must not silence the warning.
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Config holds the resolved runtime settings for the MCP server process.
//
// Defaults mirror the Python tool's conventions (Ollama bge-m3 at :11434,
// Qdrant at :6333) so a local dev box works with zero flags, while production
// overrides everything via flags or env.
type Config struct {
	// Addr is the host:port the HTTP/MCP server listens on. It is ignored when
	// SocketPath is set.
	Addr string

	// SocketPath, when non-empty, makes the server listen on that Unix domain
	// socket instead of the TCP Addr. The same HTTP/MCP handlers are served
	// either way — only the transport changes — so this stays additive: leaving
	// it empty keeps the day-one TCP behaviour.
	//
	// It exists mainly for self-hosted (Local) installs. A socket created with
	// 0600 permissions is reachable only by its owner, which is a strictly
	// tighter boundary than LocalAddr: every user and process on a machine can
	// open a loopback port, but only one can open the socket. That makes it the
	// safer home for local mode's unauthenticated /mcp — see listenerFor in
	// cmd/server for the creation rules.
	SocketPath string

	// DBPath is the SQLite database file (the relational and vector source of
	// truth).
	DBPath string

	// VectorBackend selects the search index: VectorBackendSQLite (the source of
	// truth serves search too), VectorBackendChromem (SQLite source of truth +
	// an embedded in-process index) or VectorBackendQdrant (SQLite source of
	// truth + a Qdrant service). SQLite is written either way.
	VectorBackend string

	// QdrantURL is the base URL of the Qdrant vector store (no trailing slash).
	QdrantURL string

	// QdrantAPIKey is an optional Qdrant API key; empty for unauthenticated dev.
	QdrantAPIKey string

	// OllamaURL is the base URL of the Ollama server used for embeddings.
	OllamaURL string

	// OllamaEmbedModel is the embedding model name; bge-m3 yields 1024-dim
	// vectors, matching the frozen Python palace so data stays comparable.
	OllamaEmbedModel string

	// RerankURL is the base URL of a cross-encoder rerank service whose scores
	// re-order search results. EMPTY — the default — turns reranking off entirely
	// and search behaves exactly as it did before.
	//
	// It is a separate endpoint from OllamaURL because the two are different jobs
	// and, in practice, different processes: Ollama serves the bge-m3 embeddings,
	// a cross-encoder server serves bge-reranker-v2-m3. Ollama cannot do this one —
	// it exposes only a model's embedding layer, never a classification head, so
	// it has no rerank endpoint to point at (ollama/ollama#10467).
	//
	// text-embeddings-inference is the reference server (`brew install
	// text-embeddings-inference` runs it natively with Metal on Apple Silicon);
	// llama.cpp's server answers the same shape and is what the compose stack
	// runs, since it is the only one with an arm64 image.
	RerankURL string

	// RerankPool is how many fused candidates get cross-encoded per search.
	// Larger means the reranker can rescue a good drawer that the vector+BM25
	// pass buried, at the cost of latency; 0 or less takes palace.DefaultRerankPool.
	// Ignored when RerankURL is empty.
	//
	// There is deliberately no RERANK_MODEL: TEI's /rerank carries no model field,
	// because the model is fixed when the server starts (--model-id). A knob the
	// wire cannot carry would only ever mislead.
	RerankPool int

	// BM25Weight is how much the lexical half of hybrid fusion counts: "auto"
	// (the default) scales it per query by how much lexical signal the query
	// actually has against the candidates, or a fixed number 0..1.
	//
	// Auto is a measured choice, not a guess. The same palace scored the fixed
	// 0.4 as decisively best on identifier-carrying queries and decisively worst
	// on cross-language ones (MRR 0.625 against auto's 0.938); auto matched the
	// best fixed weight in BOTH regimes, because the right weight is a property
	// of the query, not of the corpus.
	BM25Weight string

	// Fusion selects how vector and lexical evidence combine: "linear" (the
	// default weighted blend, tuned by BM25Weight) or "rrf" (reciprocal-rank
	// fusion, which bounds either signal's influence to a rank position).
	//
	// It matters most where lexical evidence is unreliable. On a large diverse
	// palace the eval measured BM25 fusion BELOW vector alone, and because the
	// cross-encoder's pool is taken off the fused head, the damage compounded:
	// the reranker never saw what fusion buried. RRF was the best arm there.
	// Run `agentsmemory eval` on your own corpus before switching — that is what
	// the rrf arm in its table is for.
	Fusion string

	// ClosetBoost scales the closet curation prior in ranking: 1 (default)
	// keeps the full boost, 0 disables it. On a curated palace the boost
	// promotes what a human chose to keep; on a mined-transcript corpus the
	// eval measured it demoting correct answers, and the operator is the one
	// who knows which corpus theirs is.
	ClosetBoost float64

	// RerankWeight is how much of the final ordering the cross-encoder decides,
	// with the rest left to the hybrid score it refines. 1 hands it the whole
	// decision, which measurably loses the lexical evidence a query carries when
	// it names an identifier; 0 disables reranking without unconfiguring it.
	RerankWeight float64

	// RerankTimeout bounds a rerank call. It is separate from HTTPTimeout because
	// the other outbound calls (embed, Qdrant) answer in milliseconds while this
	// one is doing real inference, and one shared budget means either cutting the
	// cross-encoder off or waiting far too long on a dead vector store.
	RerankTimeout time.Duration

	// HTTPTimeout bounds outbound calls to Qdrant and Ollama.
	HTTPTimeout time.Duration

	// Debug turns on verbose logging: per-request HTTP access logs (chi) and
	// gorm SQL logging. Off by default so production stays quiet; set APP_DEBUG=true
	// (or --debug) to see traffic and queries during development.
	Debug bool

	// Local turns the process into a single-workspace, self-hosted server: one
	// workspace (slug LocalSlug) exists, /mcp is unauthenticated, and the human
	// dashboard, the OAuth handshake and the billing webhooks are not mounted at
	// all. It is the "run it on my own machine" mode — there is no tenant to tell
	// apart, so there is no token to carry, and there is nothing for a dashboard
	// to manage.
	//
	// Because the endpoint is unauthenticated by default, anyone who can reach the
	// port owns every memory in the database; LocalAddr is therefore the default
	// listen address in this mode. LocalToken is what lifts that restriction.
	Local bool

	// LocalToken, when non-empty, is the shared secret local mode requires as
	// "Authorization: Bearer <token>" on its agent endpoints. Empty — the default
	// — keeps local mode credential-free, which is the right trade on a loopback
	// or Unix-socket bind where the operating system is already the boundary.
	//
	// It exists for the one case loopback cannot serve: reaching the server from
	// another machine on a home network. Binding a routable address there turns
	// "anyone on this machine" into "anyone on this wifi", and a single shared
	// secret is the proportionate answer — there is exactly one workspace, so
	// there is nothing to tell apart, only something to keep strangers out of.
	//
	// It is deliberately NOT a per-workspace API key: local mode mints none (see
	// tenant.EnsureLocalWorkspace), because a credential the server stores is one
	// somebody has to rotate. This one lives only in the operator's process
	// environment and in the agent config they point at it.
	//
	// Meaningless outside Local mode — the multi-tenant path resolves real
	// per-workspace keys — so cmd/server rejects the flag combination rather than
	// silently ignoring it.
	LocalToken string

	// SuperAdminEmails is the platform-superadmin allowlist: users whose email is
	// in this set may edit the GLOBAL skillset (the am_skillset wakeup playbook)
	// that every tenant shares. It is a deploy-time decision carried as process
	// config (env SUPERADMIN_EMAILS, comma-separated), NOT a database row or a
	// per-team role — mirroring how the sibling forumchat project gates its
	// god-mode surface. Empty means no superadmin: the global skillset can be
	// seeded on a fresh database but not edited from the dashboard.
	SuperAdminEmails []string
}

// ParseSuperAdminEmails splits a comma-separated SUPERADMIN_EMAILS value into a
// normalized allowlist: each address lower-cased and trimmed, blanks dropped. It
// is the single normalization point so the stored allowlist and the email a
// session is checked against are compared on the same footing.
func ParseSuperAdminEmails(raw string) []string {
	var out []string
	for e := range strings.SplitSeq(raw, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Default returns a Config populated with the day-one development defaults.
// Flag and env resolution in cmd/server overlays user-supplied values on top.
func Default() Config {
	return Config{
		Addr:             ":8080",
		DBPath:           "agentsmemory.db",
		VectorBackend:    VectorBackendSQLite,
		QdrantURL:        "http://localhost:6333",
		OllamaURL:        "http://localhost:11434",
		OllamaEmbedModel: "bge-m3",
		HTTPTimeout:      30 * time.Second,
		BM25Weight:       "auto",
		RerankPool:       50,  // palace.DefaultRerankPool; duplicated to keep config dependency-free
		RerankWeight:     0.5, // palace.DefaultRerankWeight, chosen by the eval's weight sweep
		ClosetBoost:      1,
		Fusion:           "linear",
		RerankTimeout:    90 * time.Second,
		Debug:            false,
	}
}
