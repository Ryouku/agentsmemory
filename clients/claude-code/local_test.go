package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLocalInstaller builds an installer aimed at a self-hosted server: the local
// endpoint, and deliberately a NON-EMPTY token, so every assertion below also
// proves --local drops an inherited AGENTSMEMORY_TOKEN instead of quietly
// writing it into a config where it would imply the server checks it.
func newLocalInstaller(t *testing.T, kit agentKit) (*Installer, *recordingRunner, string) {
	t.Helper()
	inst, rr, dir := newTestInstallerFor(t, kit, false)
	inst.local = true
	inst.mcpURL = localMCPURL
	inst.token = "INHERITED-TOKEN-THAT-MUST-NOT-BE-USED"
	return inst, rr, dir
}

// TestLocalInstallClaudeRegistersWithoutHeader pins the Claude registration for a
// token-less server: same command, no --header. An empty bearer would still work
// against our own --local server (it ignores inbound credentials) but would read
// as authentication in the user's config, so it must not be written.
func TestLocalInstallClaudeRegistersWithoutHeader(t *testing.T) {
	inst, rr, _ := newLocalInstaller(t, claudeKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	want := []string{
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + localMCPURL,
	}
	got := renderAll(rr.calls)
	if !equalStrings(got, want) {
		t.Fatalf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
	for _, c := range got {
		if strings.Contains(c, "Authorization") || strings.Contains(c, "INHERITED") {
			t.Errorf("token material leaked into a local install: %q", c)
		}
	}
}

// TestLocalInstallCodexWritesNoTokenFile covers codex, where the token is
// normally persisted for the launcher to export. With no token there is nothing
// to persist, so the file must be absent rather than empty — an empty
// AGENTSMEMORY_TOKEN file would only mislead whoever reads it next.
func TestLocalInstallCodexWritesNoTokenFile(t *testing.T) {
	inst, rr, dir := newLocalInstaller(t, codexKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, tokenFile)); !os.IsNotExist(err) {
		t.Errorf("%s exists after a --local install (err=%v), want absent", tokenFile, err)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + localMCPURL,
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestLocalInstallPiWritesEndpointAndLocalFlag covers pi, whose bridge extension
// has no config of its own. The env file must carry the endpoint plus the flag
// that tells the extension a missing token is intentional — without it the
// extension announces "memory tools are off" and never connects.
func TestLocalInstallPiWritesEndpointAndLocalFlag(t *testing.T) {
	inst, _, dir := newLocalInstaller(t, piKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, tokenFile))
	if err != nil {
		t.Fatalf("read %s: %v", tokenFile, err)
	}
	env := string(raw)
	for _, want := range []string{mcpURLEnvVar + "=" + localMCPURL, localEnvVar + "=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env file missing %q, got %q", want, env)
		}
	}
	if strings.Contains(env, tokenEnvVar+"=") {
		t.Errorf("env file carries a token on a --local install: %q", env)
	}
}

// TestLocalInstallNeverPromptsForToken guards the interactive path: resolveToken
// must not read stdin in local mode. A blocked prompt is the failure this
// prevents — a self-hoster has no token to type, so the question has no answer.
func TestLocalInstallNeverPromptsForToken(t *testing.T) {
	inst, _, _ := newLocalInstaller(t, claudeKit)
	inst.token = ""
	out := &bytes.Buffer{}
	inst.out = out
	// Any read from this reader means we prompted; it returns a token that would
	// then show up in the registration command.
	inst.in = strings.NewReader("SNEAKY-TOKEN\n")

	if got := inst.resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty in local mode", got)
	}
	if strings.Contains(out.String(), "token") {
		t.Errorf("local install prompted for a token: %q", out.String())
	}
}

// TestLocalInstallSummaryNamesTheServer checks the one thing a self-hosted setup
// depends on that nothing else in the output mentions: the server has to be
// running, and at the endpoint this install was pointed at.
func TestLocalInstallSummaryNamesTheServer(t *testing.T) {
	inst, _, _ := newLocalInstaller(t, claudeKit)
	out := &bytes.Buffer{}
	inst.out = out
	inst.summary()

	got := out.String()
	if !strings.Contains(got, "agentsmemory --local") || !strings.Contains(got, localMCPURL) {
		t.Errorf("summary does not name the local server or its endpoint: %q", got)
	}
}
