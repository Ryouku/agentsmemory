// Command server is the agentsmemory remote MCP server. It migrates its SQLite
// schema, wires the tenant/skill/store/embed collaborators, and serves the MCP
// tools over Streamable HTTP so a team's agents can connect with a Bearer token.
//
// This is the day-one skeleton: it boots, migrates, seeds a demo team on first
// run, and answers the status and load_skill tools. Mining and hybrid search
// land in later phases against the same wiring.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/dataexport"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/ollama"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embedworker"
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mergejob"
	"github.com/atvirokodosprendimai/agentsmemory/internal/oauth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/passkey"
	"github.com/atvirokodosprendimai/agentsmemory/internal/share"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pressly/goose/v3"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	// Load a .env file if present (best effort) so secrets and config can live
	// in a local file during development; real env vars still take precedence.
	_ = godotenv.Load()

	def := config.Default()

	// serveAction boots the HTTP server. It backs both the root command (so a
	// bare `agentsmemory`, and the Docker image, keep serving) and the explicit
	// `serve` subcommand — one behaviour, two entry points.
	serveAction := func(ctx context.Context, c *cli.Command) error {
		return run(ctx, configFromCmd(c, def))
	}

	// urfave/cli v3 models the program as a Command. The root keeps the serve
	// flags + action for backward compatibility; subcommands add an explicit
	// `serve` and the read-only `mcp` CLI. Flag builders return fresh slices so
	// the root and the `serve` subcommand never share mutable flag state.
	cmd := &cli.Command{
		Name:   "agentsmemory",
		Usage:  "Remote MCP memory server (Qdrant + Ollama, multi-tenant)",
		Flags:  serveFlags(def),
		Action: serveAction, // no subcommand → serve (bare run + Docker CMD)
		Commands: []*cli.Command{
			{
				Name:   "serve",
				Usage:  "Run the HTTP MCP server + dashboard (the default action)",
				Flags:  serveFlags(def),
				Action: serveAction,
			},
			mcpCommand(def),
			stdioCommand(def),
			syncCommand(def),
			shareCommand(def),
			setPlanCommand(def),
			projectsCommand(def),
			inspectCommand(def),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// configFromCmd reads the storage/embed flags off a (sub)command into a Config.
// The mcp subcommand omits the addr and socket flags, so c.String yields "" for
// both there — harmless, because only the serve path reads them.
func configFromCmd(c *cli.Command, def config.Config) config.Config {
	cfg := config.Config{
		Addr:             c.String("addr"),
		SocketPath:       c.String("socket"),
		DBPath:           c.String("db"),
		VectorBackend:    c.String("vector-backend"),
		QdrantURL:        c.String("qdrant-url"),
		QdrantAPIKey:     c.String("qdrant-api-key"),
		OllamaURL:        c.String("ollama-url"),
		OllamaEmbedModel: c.String("ollama-model"),
		HTTPTimeout:      def.HTTPTimeout,
		Debug:            c.Bool("debug"),
		Local:            c.Bool("local"),
		// Platform-superadmin allowlist (serve only). On the mcp CLI the flag is
		// undefined so c.String returns "" → an empty allowlist, which is correct:
		// the read-only CLI never edits the global skillset.
		SuperAdminEmails: config.ParseSuperAdminEmails(c.String("superadmin-emails")),
	}
	// Local mode serves an UNAUTHENTICATED /mcp, so it defaults to loopback rather
	// than the multi-tenant ":8080" (every interface). An explicit --addr or
	// AGENTSMEMORY_ADDR still wins — serveLocal warns when that choice reaches the
	// network — but the default must never be the exposed one.
	if cfg.Local && !c.IsSet("addr") {
		cfg.Addr = config.LocalAddr
	}
	// Local mode is "one machine, one process", so its default search index must
	// not be a service someone has to run: chromem indexes in-process and stores
	// its files beside the database. The multi-tenant default stays sqlite (see
	// config.Default) — this only moves the floor for self-hosted installs, and
	// an explicit --vector-backend or VECTOR_BACKEND still wins, which is how the
	// Docker stack pins the choice out loud.
	if cfg.Local && !c.IsSet("vector-backend") {
		cfg.VectorBackend = config.VectorBackendChromem
	}
	return cfg
}

// dataFlags are the storage + embedding flags shared by every entry point that
// opens the database (serve and the read-only mcp CLI). It returns a fresh slice
// per call so each command owns its own flag instances.
func dataFlags(def config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "db", Sources: cli.EnvVars("AGENTSMEMORY_DB"), Value: def.DBPath, Usage: "SQLite database path"},
		&cli.StringFlag{Name: "vector-backend", Sources: cli.EnvVars("VECTOR_BACKEND"), Value: def.VectorBackend, Usage: "search index: sqlite|chromem|qdrant (SQLite is always the source of truth; --local defaults to chromem)"},
		&cli.StringFlag{Name: "qdrant-url", Sources: cli.EnvVars("QDRANT_URL"), Value: def.QdrantURL, Usage: "Qdrant base URL"},
		&cli.StringFlag{Name: "qdrant-api-key", Sources: cli.EnvVars("QDRANT_API_KEY"), Value: def.QdrantAPIKey, Usage: "Qdrant API key (optional)"},
		&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("OLLAMA_URL"), Value: def.OllamaURL, Usage: "Ollama base URL"},
		&cli.StringFlag{Name: "ollama-model", Sources: cli.EnvVars("OLLAMA_EMBED_MODEL"), Value: def.OllamaEmbedModel, Usage: "Ollama embedding model"},
		&cli.BoolFlag{Name: "debug", Sources: cli.EnvVars("APP_DEBUG"), Value: def.Debug, Usage: "verbose logging: per-request HTTP access logs + gorm SQL"},
	}
}

// serveFlags are the flags the serving entry points expose: the listen address
// or socket, plus the shared storage/embed flags.
func serveFlags(def config.Config) []cli.Flag {
	return append([]cli.Flag{
		&cli.StringFlag{Name: "addr", Sources: cli.EnvVars("AGENTSMEMORY_ADDR"), Value: def.Addr, Usage: "HTTP listen address"},
		// AGENTSMEMORY_SOCKET is shared with the mcp-stdio proxy on purpose: one
		// exported variable points the server at a socket and tells the proxy
		// where to dial, so the pair cannot drift apart.
		&cli.StringFlag{Name: "socket", Sources: cli.EnvVars("AGENTSMEMORY_SOCKET"), Value: def.SocketPath, Usage: "listen on this Unix socket (mode 0600) instead of --addr; pair it with 'mcp-stdio --socket' to reach the server over stdio"},
		&cli.BoolFlag{Name: "local", Sources: cli.EnvVars("AGENTSMEMORY_LOCAL"), Value: def.Local, Usage: "self-hosted single-workspace mode: one \"local\" workspace, unauthenticated /mcp, no dashboard (defaults to " + config.LocalAddr + ")"},
		&cli.StringFlag{Name: "superadmin-emails", Sources: cli.EnvVars("SUPERADMIN_EMAILS"), Usage: "comma-separated emails allowed to edit the global am_skillset playbook"},
	}, dataFlags(def)...)
}

// run opens the database, migrates, wires dependencies, and serves until error.
func run(ctx context.Context, cfg config.Config) error {
	if cfg.Debug {
		// Make the "why is it silent?" answer obvious on boot: echo the effective
		// wiring so a misread flag/env is visible before any request arrives.
		log.Printf("debug mode ON — request + SQL logging enabled")
		log.Printf("config: addr=%s db=%s vector_backend=%s ollama=%s/%s",
			cfg.Addr, cfg.DBPath, cfg.VectorBackend, cfg.OllamaURL, cfg.OllamaEmbedModel)
	}

	// Claim the database before opening it. Only one server may serve a given
	// database; a second would orphan the first — silently, since the loser keeps
	// running and logs nothing. See lock.go for why the database and not the
	// listener is the thing being guarded.
	//
	// This belongs to serving, not to buildServices: inspect, mcp, plan and share
	// open the same database as readers and must keep working while a server
	// runs, which is exactly what the WAL journal mode enables.
	lock, err := lockDB(cfg.DBPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	// Open + migrate + wire the bounded-context services. The same wiring backs
	// the read-only mcp CLI, so it lives in buildServices (the one place the two
	// driving adapters share). Serving additionally seeds and starts transports.
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	log.Printf("vector backend: %s (SQLite source of truth)", cfg.VectorBackend)
	tenants, skills, usageSvc, drawers := svc.tenants, svc.skills, svc.usage, svc.drawers

	// Background embedder: drains rows that /import absorbed (text written, vector
	// deferred) so a large migration never blocks on the embedder or a proxy read
	// timeout. It runs for the process lifetime; ctx cancels it on shutdown, and the
	// embedded_at queue is durable so a restart simply resumes. Defaults suffice.
	go embedworker.New(drawers, 0, 0, nil).Run(ctx)

	// Background merge worker: drains the durable merge_jobs queue (a GUI enqueues
	// a wing merge; this relabels + rebuilds the graph off the request path). Like
	// the embedder it runs for the process lifetime, resumes from the queue on
	// restart, and stops on ctx cancel.
	go mergejob.New(mergejob.NewRepo(svc.gdb), drawers, nil).Run(ctx)

	// The MCP server, exposed over Streamable HTTP. The HTTP context func runs
	// per request, turning the Bearer token into a tenant on the context the
	// tools read — this is the only place auth touches the transport. Tools
	// meter each call against the workspace's monthly cap via usageSvc.
	mcpSrv := mcpserver.New(mcpserver.Deps{Skills: skills, Skillset: svc.skillsets, Usage: usageSvc, Drawers: drawers})

	// OAuth 2.1 authorization server (stateless), validating client credentials
	// against our own api_keys (the merged authcounterapi role). It guards /mcp
	// and serves the discovery + token endpoints claude.ai's remote connector
	// needs. tenants satisfies both the client validator and the raw-token
	// resolver, so OAuth bearers and direct API tokens share one /mcp.
	sealer, err := oauth.NewSealer(oauthSecret())
	if err != nil {
		return fmt.Errorf("oauth sealer: %w", err)
	}
	issuer := oauthIssuer(cfg.Addr)
	authSrv := oauth.NewAuthServer(issuer, sealer, tenants, tenants)

	streamSrv := server.NewStreamableHTTPServer(
		mcpSrv,
		// The OAuth gate resolves the bearer and puts the tenant on the request
		// context; Bridge forwards it into the per-tool context.
		server.WithHTTPContextFunc(auth.Bridge),
		// Stateless: no server-side session map. Suits a multi-tenant remote
		// server and lets it scale horizontally behind a load balancer.
		server.WithStateLess(true),
	)

	// The base router both modes share: logging (debug only), panic recovery, and
	// the liveness probe. Everything mounted after this point differs per mode.
	r := chi.NewRouter()
	// Logger before Recoverer so even a panicked request (recovered as a 500) is
	// still logged. Gated on Debug: the server is intentionally silent in
	// production, and APP_DEBUG=true is what surfaces per-request access logs.
	if cfg.Debug {
		r.Use(middleware.Logger)
	}
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Self-hosted mode forks here: it serves the agent surfaces only, so none of
	// the multi-tenant machinery below (seeding, OAuth, billing, passkeys, the
	// dashboard) is constructed — let alone mounted.
	if cfg.Local {
		return serveLocal(ctx, cfg, svc, r, streamSrv)
	}

	// Seeding is serve-only: the read-only CLI must never create a demo team. The
	// global skillset is seeded here too (via its repo, bypassing the superadmin
	// gate) so am_skillset is useful on a fresh database before any edit.
	if err := seedIfEmpty(ctx, svc.gdb, tenants, skill.NewRepo(svc.gdb), skillset.NewRepo(svc.gdb), svc.vectors); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// Billing (hosted checkout + webhook; Stripe or Polar per BILLING_PROVIDER).
	// Always constructed so the dashboard and webhook wiring stay simple; it is inert
	// until the active provider's env is set (billingSrv.Enabled() gates the upgrade
	// UI). It flips teams.plan_id, so it reuses tenants as its PlanStore.
	billingSrv := billing.NewService(billingConfig(), tenants, billing.NewRepo(svc.gdb))

	// Per-workspace data export (BDAR right of access): builds a scoped SQLite
	// archive of a tenant's data from the same source-of-truth database.
	exporter := dataexport.New(svc.gdb)

	// Passkey (WebAuthn) service: registers and verifies device credentials for
	// passwordless login and as a second factor. The Relying Party config derives
	// from PUBLIC_BASE_URL — the same public origin the OAuth callbacks use — so
	// the RPID/origin match the domain the browser is on (a mismatch is the classic
	// passkey failure). A bad config is a fatal startup error, never a silent one.
	baseURL := os.Getenv("PUBLIC_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	passkeys, err := passkey.NewService(passkey.ConfigFromBaseURL(baseURL, "AI Agent Memory"), passkey.NewRepo(svc.gdb))
	if err != nil {
		return fmt.Errorf("passkey service: %w", err)
	}

	// The human-facing dashboard (register/login/create project) shares the same
	// chi router and database; agents use /mcp, people use the web routes.
	webSrv := web.New(tenants, usageSvc, skills, svc.skillsets, svc.shares, svc.merges, billingSrv, exporter, passkeys, cfg.SuperAdminEmails, sessionKey())

	// OAuth discovery + endpoints for the claude.ai remote-connector handshake.
	r.Get("/.well-known/oauth-protected-resource", authSrv.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", authSrv.AuthorizationServerMetadata)
	r.Get("/authorize", authSrv.Authorize)
	r.Post("/token", authSrv.Token)

	// Payment webhook: PUBLIC and unauthenticated by design — the provider calls it
	// server-to-server and the signature (verified inside HandleWebhook) IS the
	// authentication. It must see the RAW request body because the signature is
	// computed over the exact bytes, so it reads the body itself rather than relying
	// on any body-parsing middleware. Both provider paths are registered but only the
	// provider selected by BILLING_PROVIDER verifies, so the inactive path simply
	// rejects. A non-nil error is returned as 400 so the provider retries (bad
	// signature or a transient processing failure); a verified-but-unhandled event
	// already returned nil → 200.
	webhookHandler := func(w http.ResponseWriter, req *http.Request) {
		payload, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if err := billingSrv.HandleWebhook(req.Context(), payload, req.Header); err != nil {
			log.Printf("billing webhook: %v", err)
			http.Error(w, "webhook error", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	r.Post("/webhooks/stripe", webhookHandler)
	r.Post("/webhooks/polar", webhookHandler)

	// MCP endpoint, fronted by the OAuth gate: it 401-challenges unauthenticated
	// requests (so the connector starts OAuth) and lets resolved bearers (OAuth
	// or direct API token) through to the stateless MCP handler.
	r.Handle("/mcp", authSrv.Gate(streamSrv))

	// Bulk migration ingest: a user streams their exported mempalace (NDJSON) here
	// with the same Bearer token as /mcp. The gate resolves the tenant, then the
	// importer re-files every drawer/closet/fact/tunnel under it. Fronted by the
	// same gate so auth is identical to the agent surface.
	r.Handle("/import", authSrv.Gate(importer.Handler(drawers, usageSvc)))

	// Dashboard + auth + static assets.
	webSrv.Routes(r)

	ln, err := listenerFor(cfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("agentsmemory listening on %s (dashboard /, MCP /mcp, OAuth issuer %s)", listenDescription(cfg), issuer)
	return http.Serve(ln, r)
}

// seedGlobalSkillset writes the default wakeup playbook when the database holds
// none yet. BOTH serving modes need it: the am_* tools are a catalogue of verbs
// with no instruction to use them, and the playbook is the "call this first, in
// this order, before you act" guidance that makes an agent reach for them at all.
// A server answering 37 tools with an empty preamble is a memory the agent never
// opens.
//
// Written via the repo rather than the gated Service because this is system
// seeding, not a superadmin edit; updated_by stays empty to mark it as the seeded
// default rather than an authored version. Seeding only when unset means an
// operator's edits are never overwritten on restart.
func seedGlobalSkillset(ctx context.Context, skillsets *skillset.Repo) error {
	if _, err := skillsets.Get(ctx); err == nil {
		return nil // already authored or seeded — leave it alone
	} else if !errors.Is(err, skillset.ErrNotSet) {
		return fmt.Errorf("read global skillset: %w", err)
	}
	if _, err := skillsets.Set(ctx, skillset.DefaultPlaybook, ""); err != nil {
		return fmt.Errorf("seed global skillset: %w", err)
	}
	return nil
}

// serveLocal runs the self-hosted, single-workspace server and blocks. It
// resolves (and on a fresh database provisions) the one "local" workspace, then
// mounts only the agent surfaces — /mcp and /import — behind a credential-free
// tenant middleware.
//
// Nothing else is registered. The dashboard, the OAuth handshake and the billing
// webhooks all exist to tell tenants apart, let a human manage their keys, or
// charge them; with one workspace, no token and no billing relationship, each
// would be a surface with nothing behind it.
//
// The trade this mode makes is explicit: authentication is replaced by network
// reachability. That is sound on a machine you own and wrong the moment the port
// is routable, which is what the non-loopback warning is for.
func serveLocal(ctx context.Context, cfg config.Config, svc *services, r chi.Router, mcpHandler http.Handler) error {
	t, err := svc.tenants.EnsureLocalWorkspace(ctx)
	if err != nil {
		// A database holding someone else's workspaces must never be served
		// through an open endpoint, so this is fatal with an actionable message
		// rather than a fallback to "pick the first team".
		if errors.Is(err, tenant.ErrForeignWorkspace) {
			return fmt.Errorf("%w — --local serves exactly one workspace; point --db at a fresh file, or drop --local to run multi-tenant", err)
		}
		return fmt.Errorf("local workspace: %w", err)
	}

	// Ready the workspace's vector namespace so its first write/search has
	// somewhere to land — a no-op for the SQLite backend, a collection create for
	// Qdrant. Mirrors what seeding does on the multi-tenant path.
	if err := svc.vectors.EnsureNamespace(ctx, t.TeamID, defaultVectorDim); err != nil {
		return fmt.Errorf("ensure local vector namespace: %w", err)
	}

	// The wakeup playbook is seeded here as well as on the multi-tenant path.
	// Local mode skips seedIfEmpty (it must never create a demo workspace), but
	// skipping the playbook with it would leave am_skillset returning 37 tools and
	// no guidance — a server the agent can call but never thinks to.
	if err := seedGlobalSkillset(ctx, skillset.NewRepo(svc.gdb)); err != nil {
		return err
	}

	// One middleware stands in for the entire auth stack: every request already
	// belongs to the only workspace there is.
	local := auth.LocalTenant(t)
	r.Handle("/mcp", local(mcpHandler))
	r.Handle("/import", local(importer.Handler(svc.drawers, svc.usage)))

	// The exposure warning is about TCP only. A socket is bound at 0600, so the
	// operating system already restricts the unauthenticated endpoint to this
	// user — a tighter boundary than any loopback port, which every process on
	// the machine may open. Warning there would train operators to ignore it.
	if cfg.SocketPath == "" && !config.IsLoopback(cfg.Addr) {
		log.Printf("WARNING: --local serves an UNAUTHENTICATED /mcp, and %s is not a loopback address — anyone who can reach this port has full read and write access to every memory in %s. Bind %s, use --socket, or run multi-tenant.",
			cfg.Addr, cfg.DBPath, config.LocalAddr)
	}

	ln, err := listenerFor(cfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("agentsmemory listening on %s (local mode: workspace %q, MCP /mcp, no token required, no dashboard)", listenDescription(cfg), tenant.LocalSlug)
	// Registering the server is only half the job: an MCP endpoint is a catalogue
	// of verbs, and an agent calls none of them unless something instructs it to.
	// So point at both halves — the connection AND the protocol that drives it —
	// because a local operator has no dashboard to read this from.
	//
	// A socket has no URL to hand an agent, so the hint switches to the stdio
	// proxy. That is the whole point of shipping the proxy in this binary: the
	// same executable that is already running is also the command the agent
	// spawns, so there is nothing further to install.
	if cfg.SocketPath == "" {
		log.Printf("connect an agent:  claude mcp add --transport http agentsmemory http://%s/mcp", strings.TrimPrefix(cfg.Addr, ":"))
	} else {
		log.Printf("connect an agent:  claude mcp add agentsmemory -- %s mcp-stdio --socket %s", executableName(), cfg.SocketPath)
		log.Printf("           codex:  codex mcp add agentsmemory -- %s mcp-stdio --socket %s", executableName(), cfg.SocketPath)
	}
	log.Printf("then install the memory protocol (CLAUDE.md + /M, /am commands + the end-of-turn hook), or the tools sit unused:  aiagentmemory install")
	return http.Serve(ln, r)
}

// listenDescription renders the bound address for the startup logs: the socket
// path when one is in use, the TCP address otherwise. It exists so both serving
// modes report where they actually are rather than echoing an Addr that a socket
// listener never bound.
func listenDescription(cfg config.Config) string {
	if cfg.SocketPath != "" {
		return "unix:" + cfg.SocketPath
	}
	return cfg.Addr
}

// executableName returns this binary's path for the copy-paste "connect an
// agent" hints. The agent spawns the proxy itself, so the hint has to name a
// command that will still resolve in that context: an absolute path always
// does, where the bare name only works if the binary is on PATH. It falls back
// to the plain name if the path cannot be determined.
func executableName() string {
	exe, err := os.Executable()
	if err != nil {
		return "aiagentmemory-server"
	}
	return exe
}

// oauthSecret returns the key that seals OAuth tokens. OAUTH_SECRET_KEY keeps
// tokens valid across restarts; absent it, a random key is used (and every
// previously issued OAuth token becomes invalid on restart).
func oauthSecret() string {
	if s := os.Getenv("OAUTH_SECRET_KEY"); s != "" {
		return s
	}
	log.Printf("warning: OAUTH_SECRET_KEY unset; using a random key (OAuth tokens reset on restart)")
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating OAuth secret: %v", err)
	}
	return hex.EncodeToString(buf)
}

// oauthIssuer is the public base URL advertised in OAuth metadata. In production
// set OAUTH_ISSUER to the external https URL (no trailing slash, no /mcp); for
// local dev it is derived from the listen address.
func oauthIssuer(addr string) string {
	if v := os.Getenv("OAUTH_ISSUER"); v != "" {
		return strings.TrimRight(v, "/")
	}
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	return "http://" + host
}

// tokenSecret returns the secret that seals API keys at rest so the dashboard can
// reveal them. AGENTSMEMORY_TOKEN_KEY keeps sealed keys revealable across
// restarts; absent it, a random per-boot key is used and a warning is logged —
// reveal still works within a run, but keys minted before a restart become
// reveal-unavailable (the seal can't be opened with the new key). An empty
// secret here disables reveal entirely (tokens stay shown-once).
func tokenSecret() string {
	if s := os.Getenv("AGENTSMEMORY_TOKEN_KEY"); s != "" {
		// The seal key is SHA-256 of this string, so a short/low-entropy value is
		// guessable offline against a leaked token_enc (GCM confirms a correct
		// guess). Warn loudly; it should be 32+ random characters (hex/base64).
		if len(s) < 32 {
			log.Printf("warning: AGENTSMEMORY_TOKEN_KEY is shorter than 32 chars; use 32+ random bytes (hex/base64) so revealed keys resist offline guessing")
		}
		return s
	}
	log.Printf("warning: AGENTSMEMORY_TOKEN_KEY unset; using a random key (revealed keys reset on restart)")
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating token key: %v", err)
	}
	return hex.EncodeToString(buf)
}

// sessionKey returns the cookie signing key. AGENTSMEMORY_SESSION_KEY (hex) keeps
// sessions valid across restarts in production; absent it, a random key is used
// and a warning is logged (dev convenience — sessions reset on restart).
func sessionKey() []byte {
	if hexKey := os.Getenv("AGENTSMEMORY_SESSION_KEY"); hexKey != "" {
		if raw, err := hex.DecodeString(hexKey); err == nil && len(raw) >= 32 {
			return raw
		}
		log.Printf("warning: AGENTSMEMORY_SESSION_KEY is not valid hex of >=32 bytes; using a random key")
	} else {
		log.Printf("warning: AGENTSMEMORY_SESSION_KEY unset; using a random session key (sessions reset on restart)")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating session key: %v", err)
	}
	return buf
}

// billingConfig reads the billing wiring from the environment. Billing is optional
// and single-provider per deployment: BILLING_PROVIDER selects "stripe" (the
// back-compatible default) or "polar", and only that provider's credentials need to
// be set. Price/product ids are environment-specific (test vs live) and differ per
// provider — Stripe price ids vs Polar product ids — so they are configured here
// rather than seeded into the plan catalog. Empty price entries are dropped so a
// half-configured environment treats that plan as "not sellable" instead of priced
// "".
func billingConfig() billing.Config {
	provider := os.Getenv("BILLING_PROVIDER")
	if provider == "" {
		provider = billing.ProviderStripe
	}
	cfg := billing.Config{
		Provider:            provider,
		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		PolarAccessToken:    os.Getenv("POLAR_ACCESS_TOKEN"),
		PolarWebhookSecret:  os.Getenv("POLAR_WEBHOOK_SECRET"),
		PolarServer:         os.Getenv("POLAR_SERVER"),
	}
	// PriceByPlanCode carries the *active* provider's ids, keyed by our sellable plan
	// codes: Stripe price ids or Polar product ids.
	switch provider {
	case billing.ProviderPolar:
		cfg.PriceByPlanCode = map[string]string{
			"pro_monthly": os.Getenv("POLAR_PRODUCT_PRO_MONTHLY"),
			"pro_annual":  os.Getenv("POLAR_PRODUCT_PRO_ANNUAL"),
		}
	default:
		cfg.PriceByPlanCode = map[string]string{
			"pro_monthly": os.Getenv("STRIPE_PRICE_PRO_MONTHLY"),
			"pro_annual":  os.Getenv("STRIPE_PRICE_PRO_ANNUAL"),
		}
	}
	for code, id := range cfg.PriceByPlanCode {
		if id == "" {
			delete(cfg.PriceByPlanCode, code)
		}
	}

	// Warn on the two ways billing silently won't work, using the active provider's
	// credentials.
	secret, webhookSecret := cfg.StripeSecretKey, cfg.StripeWebhookSecret
	if provider == billing.ProviderPolar {
		secret, webhookSecret = cfg.PolarAccessToken, cfg.PolarWebhookSecret
	}
	if secret == "" {
		log.Printf("billing disabled: %s credentials unset (no upgrade-to-Pro button)", provider)
	} else if webhookSecret == "" {
		// Checkout can start, but the webhook fails closed without a secret — so a
		// completed payment would never flip the plan. Surface the misconfiguration.
		log.Printf("warning: %s webhook secret unset; webhooks will reject all events and upgrades will not take effect", provider)
	}
	return cfg
}

// defaultVectorDim is the embedding dimension new vector namespaces are created
// with — bge-m3 (the default Ollama model) produces 1024-d vectors, matching the
// Qdrant collection size in internal/store/qdrant.
const defaultVectorDim = 1024

// services holds the wired domain collaborators shared by the serve and mcp
// entry points: both open the same SQLite source of truth and talk to the same
// palace/skill/usage services. Extracting the wiring keeps the two driving
// adapters — the HTTP MCP server and the read-only CLI — over one domain core.
type services struct {
	gdb       *gorm.DB
	vectors   store.VectorStore
	tenants   *tenant.Repo
	skills    *skill.Service
	skillsets *skillset.Service // the global wakeup-playbook use-cases (am_skillset)
	usage     *usage.Service
	drawers   *palace.Service
	shares    *share.Service    // cross-workspace wing-share handshake (GUI consent flow)
	merges    *mergejob.Service // background wing-merge queue (GUI enqueue/list/detect)
}

// buildServices opens and migrates the database, then wires the bounded-context
// services against it. It deliberately does NOT seed (the serve path seeds; a
// read-only CLI invocation must not create data) and starts no transport, so it
// is safe to call from both entry points.
func buildServices(cfg config.Config) (*services, error) {
	gdb, err := openDB(cfg.DBPath, cfg.Debug)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("sql handle: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Bounded contexts: tenant (auth + workspaces), skill (load_skill), and
	// usage (monthly request metering).
	tenants := tenant.NewRepo(gdb, tenant.WithTokenSecret(tokenSecret()))
	skills := skill.NewService(skill.NewRepo(gdb))
	skillsets := skillset.NewService(skillset.NewRepo(gdb))
	usageSvc := usage.NewService(usage.NewRepo(gdb), tenants)

	// Vector storage: SQLite is always the source of truth; cfg.VectorBackend
	// selects whether it also serves search or Qdrant indexes it.
	vectors, err := buildVectorStore(cfg, gdb)
	if err != nil {
		return nil, fmt.Errorf("vector store: %w", err)
	}

	// The memory loop: Ollama embeds text, the store seam holds the vectors, and
	// the palace service ties them to drawer metadata.
	embedder := ollama.New(cfg.OllamaURL, cfg.OllamaEmbedModel, cfg.HTTPTimeout)
	drawers := palace.NewService(palace.NewRepo(gdb), embedder, vectors, defaultVectorDim)

	// The wing-share handshake bridges the two contexts it sits over: tenant
	// (resolve the destination slug, read roles) and palace (list + copy wings).
	shares := share.NewService(share.NewRepo(gdb), tenants, drawers)

	// The wing-merge queue's web side: enqueue/list jobs and detect duplicates.
	// The background worker that drains it is started in run() (serve-only).
	merges := mergejob.NewService(mergejob.NewRepo(gdb), tenants, drawers)

	return &services{gdb: gdb, vectors: vectors, tenants: tenants, skills: skills, skillsets: skillsets, usage: usageSvc, drawers: drawers, shares: shares, merges: merges}, nil
}

// buildVectorStore assembles the vector layer from cfg. SQLite is always the
// durable source of truth (sqlitevec); cfg.VectorBackend then decides whether it
// also serves search or whether an index is layered on via store.Hybrid — either
// an embedded chromem directory or a Qdrant service. This switch is the single
// swap point for the search backend.
func buildVectorStore(cfg config.Config, gdb *gorm.DB) (store.VectorStore, error) {
	sot := sqlitevec.New(gdb)
	switch cfg.VectorBackend {
	case config.VectorBackendSQLite:
		return sot, nil
	case config.VectorBackendChromem:
		dir := config.ChromemPath(cfg.DBPath)
		index, err := chromemvec.New(dir)
		if err != nil {
			return nil, err
		}
		hybrid := store.NewHybrid(sot, index)
		if err := reconcileChromem(context.Background(), sot, index, hybrid); err != nil {
			return nil, err
		}
		log.Printf("chromem index: %s", dir)
		return hybrid, nil
	case config.VectorBackendQdrant:
		index := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HTTPTimeout)
		return store.NewHybrid(sot, index), nil
	default:
		return nil, fmt.Errorf("unknown vector backend %q (want %q, %q or %q)",
			cfg.VectorBackend, config.VectorBackendSQLite, config.VectorBackendChromem, config.VectorBackendQdrant)
	}
}

// reconcileChromem fills an empty chromem index from the SQLite source of truth,
// namespace by namespace, before the server starts answering searches.
//
// It exists because the index is a directory that can legitimately be absent
// while the database is full: the first boot after an install switches to this
// backend, a restore that brought back only the .db file, or a manually deleted
// index directory. Without the replay, search would return nothing at all and
// look like data loss even though every vector is safe in SQLite. The replay is
// cheap — it reuses the stored vectors, so nothing is re-embedded and Ollama is
// not called.
//
// Only wholly empty namespaces are replayed. An index that merely fell behind is
// a different problem (rebuild it deliberately by deleting the directory), and
// re-writing every vector on every boot would make startup scale with the palace.
func reconcileChromem(ctx context.Context, sot store.SourceOfTruth, index *chromemvec.Index, hybrid *store.Hybrid) error {
	namespaces, err := sot.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("list source-of-truth namespaces: %w", err)
	}
	for _, ns := range namespaces {
		indexed, err := index.Count(ns)
		if err != nil {
			return fmt.Errorf("count chromem namespace %q: %w", ns, err)
		}
		if indexed > 0 {
			continue
		}
		if err := hybrid.Rebuild(ctx, ns); err != nil {
			return fmt.Errorf("rebuild chromem namespace %q: %w", ns, err)
		}
		log.Printf("chromem index: rebuilt namespace %q from the SQLite source of truth", ns)
	}
	return nil
}

// openDB opens a pure-Go (no cgo) SQLite database through gorm's glebarez
// driver. gorm is the query layer; goose owns the schema, so AutoMigrate is
// never called. By default the logger is silenced because expected "record not
// found" lookups (e.g. the create branch of an upsert) are control flow, not
// errors — real failures still surface through returned error values. In debug
// mode it logs every statement (logger.Info) so queries are visible during
// development.
//
// The logger writes to stderr, not gorm's stdout default, so a command whose
// stdout is data — the read-only mcp CLI emits JSON there — stays clean and
// pipeable even with APP_DEBUG=true; serve is unaffected (its stdout is not a
// data channel).
//
// The DSN carries the pragmas described on dbPragmas, because more than one
// process legitimately opens this file: serve holds it open for its lifetime
// while inspect, mcp, plan, share and an export all read it.
func openDB(path string, debug bool) (*gorm.DB, error) {
	level := logger.Silent
	if debug {
		level = logger.Info
	}
	gormLog := logger.New(
		log.New(os.Stderr, "\r\n", log.LstdFlags),
		logger.Config{LogLevel: level},
	)
	return gorm.Open(sqlite.Open(path+dbPragmas), &gorm.Config{Logger: gormLog})
}

// dbPragmas are the connection pragmas appended to the SQLite DSN.
//
// journal_mode(WAL): readers no longer block the writer, so `inspect` or a data
// export can run against a live server instead of colliding with it. WAL is a
// persistent property of the database file, so the first connection converts it
// and every later one simply observes it.
//
// busy_timeout(5000): SQLite's default is 0 — a contended write fails
// *instantly* with "database is locked" rather than waiting. Five seconds turns
// the normal case (a reader and the writer overlapping for microseconds) into a
// brief wait instead of an error.
//
// Deliberately NOT applied to the data-export archive (internal/dataexport):
// that file is handed to the user as a single download, and WAL would leave
// committed rows in a -wal sidecar that does not travel with it.
//
// This is a concurrency *performance* setting and nothing more. It is not a
// substitute for the single-instance lock in lock.go — if anything it raises the
// stakes, because with WAL two servers on one database write happily and
// silently instead of announcing themselves with lock errors.
const dbPragmas = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// migrate applies the embedded goose migrations to the open database.
func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(sqlDB, "migrations")
}

// seedIfEmpty creates a demo team, owner, API key, and one example skill on a
// brand-new database, printing the one-time token so the operator can connect an
// agent immediately. It also seeds the global wakeup playbook so am_skillset is
// useful from the first boot. On an already-seeded database it is a no-op.
func seedIfEmpty(ctx context.Context, gdb *gorm.DB, tenants *tenant.Repo, skills *skill.Repo, skillsets *skillset.Repo, vectors store.VectorStore) error {
	var teamCount int64
	if err := gdb.WithContext(ctx).Model(&tenant.Team{}).Count(&teamCount).Error; err != nil {
		return err
	}
	if teamCount > 0 {
		return nil
	}

	t, cred, err := tenants.SeedTeamWithKey(ctx, "Demo Team", "demo", "owner@demo.local")
	if err != nil {
		return err
	}
	// Ready the demo workspace's vector namespace so its first write/search has
	// somewhere to land — a no-op for the SQLite backend, a collection create
	// for Qdrant.
	if err := vectors.EnsureNamespace(ctx, t.TeamID, defaultVectorDim); err != nil {
		return fmt.Errorf("ensure demo vector namespace: %w", err)
	}
	if _, err := skills.Upsert(ctx, t.TeamID, "hello",
		"A starter skill proving load_skill works.",
		"# Hello Skill\n\nThis is a centralised, team-shared skill served by agentsmemory.\n",
		t.UserID); err != nil {
		return err
	}

	if err := seedGlobalSkillset(ctx, skillsets); err != nil {
		return err
	}

	log.Printf("seeded demo team %s", t.TeamID)
	log.Printf("OAuth client_id (shown once): %s", cred.ClientKey)
	log.Printf("MCP bearer token / secret (shown once): %s", cred.Secret)
	log.Printf("try: curl -H 'Authorization: Bearer %s' ... http://%s/mcp", cred.Secret, "<addr>")
	return nil
}
