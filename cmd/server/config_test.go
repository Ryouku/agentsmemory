package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/urfave/cli/v3"
)

// resolve parses args through the real serve flag set and returns the Config the
// server would run with, so these tests pin the actual flag/env resolution rather
// than a re-implementation of it.
func resolve(t *testing.T, args ...string) config.Config {
	t.Helper()
	def := config.Default()
	var got config.Config
	cmd := &cli.Command{
		Name:  "serve",
		Flags: serveFlags(def),
		Action: func(_ context.Context, c *cli.Command) error {
			got = configFromCmd(c, def)
			return nil
		},
	}
	if err := cmd.Run(context.Background(), append([]string{"serve"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestLocalDefaults pins the two defaults --local moves: a loopback listen
// address (its /mcp is unauthenticated) and the embedded chromem index (a
// self-hosted install must not need a second service). Both are defaults, not
// constraints — an explicit flag or env still wins, which is how the Docker
// stack states its choices out loud.
func TestLocalDefaults(t *testing.T) {
	cfg := resolve(t, "--local")
	if cfg.Addr != config.LocalAddr {
		t.Errorf("--local addr = %q, want %q", cfg.Addr, config.LocalAddr)
	}
	if cfg.VectorBackend != config.VectorBackendChromem {
		t.Errorf("--local vector backend = %q, want %q", cfg.VectorBackend, config.VectorBackendChromem)
	}
}

// TestMultiTenantDefaultsUnchanged guards the blast radius: without --local the
// SaaS process keeps booting on SQLite-serves-search and every interface.
func TestMultiTenantDefaultsUnchanged(t *testing.T) {
	cfg := resolve(t)
	if cfg.VectorBackend != config.VectorBackendSQLite {
		t.Errorf("default vector backend = %q, want %q", cfg.VectorBackend, config.VectorBackendSQLite)
	}
	if cfg.Addr != config.Default().Addr {
		t.Errorf("default addr = %q, want %q", cfg.Addr, config.Default().Addr)
	}
}

func TestLocalRespectsExplicitVectorBackend(t *testing.T) {
	cfg := resolve(t, "--local", "--vector-backend", config.VectorBackendQdrant)
	if cfg.VectorBackend != config.VectorBackendQdrant {
		t.Errorf("explicit flag lost: got %q, want %q", cfg.VectorBackend, config.VectorBackendQdrant)
	}
}

// TestLocalRespectsVectorBackendEnv covers the case the Docker stack relies on:
// VECTOR_BACKEND in .env.docker must beat the --local default, which only holds
// if urfave/cli counts an env-sourced value as "set".
func TestLocalRespectsVectorBackendEnv(t *testing.T) {
	t.Setenv("VECTOR_BACKEND", config.VectorBackendSQLite)
	cfg := resolve(t, "--local")
	if cfg.VectorBackend != config.VectorBackendSQLite {
		t.Errorf("VECTOR_BACKEND env lost: got %q, want %q", cfg.VectorBackend, config.VectorBackendSQLite)
	}
}

// TestLocalTokenFlag pins --token reaching Config, and that it does NOT move the
// listen address: binding a routable interface stays an explicit --addr choice,
// so adding a token never widens exposure on its own.
func TestLocalTokenFlag(t *testing.T) {
	cfg := resolve(t, "--local", "--token", "s3cret")
	if cfg.LocalToken != "s3cret" {
		t.Errorf("--token = %q, want %q", cfg.LocalToken, "s3cret")
	}
	if cfg.Addr != config.LocalAddr {
		t.Errorf("--token moved the listen address to %q; it must stay %q", cfg.Addr, config.LocalAddr)
	}
}

// TestLocalTokenIsTrimmed guards a silent-lockout footgun: the presented bearer
// is trimmed when parsed out of the header, so a configured token carrying a
// trailing newline or space — what a .env file or a copy-paste produces — could
// never match anything a client can send, and every request would 401 with
// nothing in the logs to explain it.
func TestLocalTokenIsTrimmed(t *testing.T) {
	if cfg := resolve(t, "--local", "--token", "  s3cret\n"); cfg.LocalToken != "s3cret" {
		t.Errorf("--token = %q, want it trimmed to %q", cfg.LocalToken, "s3cret")
	}
	// Whitespace-only is indistinguishable from unset once trimmed, and must not
	// arm a gate no client could pass.
	if cfg := resolve(t, "--local", "--token", "   "); cfg.LocalToken != "" {
		t.Errorf("whitespace-only --token = %q, want empty", cfg.LocalToken)
	}
}

// TestLocalTokenEnv pins the env source, which is what a systemd unit or a
// compose file actually uses. It must be AGENTSMEMORY_LOCAL_TOKEN and not
// AGENTSMEMORY_TOKEN: that one is the client half of the pair (mcp-stdio
// presents it), so a developer holding a hosted workspace key in their shell
// must not have their local server silently start demanding it.
func TestLocalTokenEnv(t *testing.T) {
	t.Setenv("AGENTSMEMORY_LOCAL_TOKEN", "from-env")
	if cfg := resolve(t, "--local"); cfg.LocalToken != "from-env" {
		t.Errorf("AGENTSMEMORY_LOCAL_TOKEN = %q, want %q", cfg.LocalToken, "from-env")
	}

	t.Setenv("AGENTSMEMORY_LOCAL_TOKEN", "")
	t.Setenv("AGENTSMEMORY_TOKEN", "hosted-workspace-key")
	if cfg := resolve(t, "--local"); cfg.LocalToken != "" {
		t.Errorf("AGENTSMEMORY_TOKEN leaked into the server's required token as %q", cfg.LocalToken)
	}
}

// TestTokenRequiresLocal pins the refusal: a shared token means nothing on the
// multi-tenant path (which resolves real per-workspace API keys), and silently
// ignoring it would let an operator believe the server was locked down.
func TestTokenRequiresLocal(t *testing.T) {
	err := run(context.Background(), config.Config{LocalToken: "s3cret"})
	if err == nil {
		t.Fatal("--token without --local was accepted; it must fail loudly")
	}
	if !strings.Contains(err.Error(), "--token requires --local") {
		t.Errorf("error = %q, want it to name the flag combination", err)
	}
}

// TestAgentEndpoint pins the copy-paste URL printed at boot. The wildcard binds
// are the interesting half: they are exactly what a home-network install uses,
// and exactly what cannot be dialled from another machine, so they must yield a
// placeholder rather than a line that looks right and silently fails.
func TestAgentEndpoint(t *testing.T) {
	const placeholder = "http://<this-machine-lan-ip>:8080/mcp"
	tests := []struct{ addr, want string }{
		{"0.0.0.0:8080", placeholder},
		{":8080", placeholder},
		{"[::]:8080", placeholder},
		{"127.0.0.1:8080", "http://127.0.0.1:8080/mcp"},
		{"192.168.1.50:8080", "http://192.168.1.50:8080/mcp"},
		{"[::1]:8080", "http://[::1]:8080/mcp"},
		{"memory.lan:8080", "http://memory.lan:8080/mcp"},
	}
	for _, tc := range tests {
		if got := agentEndpoint(tc.addr); got != tc.want {
			t.Errorf("agentEndpoint(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestRerankDefaultsOff pins the switch the whole feature hangs on: no
// RERANK_URL means no reranker, so an existing deployment that never sets it
// keeps the vector+BM25+closet ordering it has always had.
func TestRerankDefaultsOff(t *testing.T) {
	cfg := resolve(t)
	if cfg.RerankURL != "" {
		t.Errorf("default rerank url = %q, want empty (feature off)", cfg.RerankURL)
	}
	if cfg.RerankPool != config.Default().RerankPool {
		t.Errorf("default rerank pool = %d, want %d", cfg.RerankPool, config.Default().RerankPool)
	}
}

// TestRerankFlagsResolve covers the operator-error case that would otherwise
// 404 every rerank call in silence: a URL pasted with surrounding whitespace.
func TestRerankFlagsResolve(t *testing.T) {
	cfg := resolve(t, "--rerank-url", "  http://100.64.79.36:12434  ", "--rerank-pool", "80")
	if want := "http://100.64.79.36:12434"; cfg.RerankURL != want {
		t.Errorf("rerank url = %q, want %q (trimmed)", cfg.RerankURL, want)
	}
	if cfg.RerankPool != 80 {
		t.Errorf("rerank pool = %d, want 80", cfg.RerankPool)
	}
}
