package main

import (
	"context"
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
