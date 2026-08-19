package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// newLocalInstaller builds an installer aimed at a token-less self-hosted server:
// the local endpoint and no token, which is what a loopback or --socket install
// is. It is the default shape because `agentsmemory --local` requires a
// credential only when started with --token; see newLocalTokenInstaller for that
// case.
func newLocalInstaller(t *testing.T, kit agentKit) (*Installer, *recordingRunner, string) {
	t.Helper()
	inst, rr, dir := newTestInstallerFor(t, kit, false)
	inst.local = true
	inst.mcpURL = localMCPURL
	inst.token = "" // the shared helper pre-seeds a hosted token; a token-less server has none
	return inst, rr, dir
}

// localServerToken is the token a --local server started with --token requires,
// as supplied to the installer by --token / AGENTSMEMORY_LOCAL_TOKEN.
const localServerToken = "LAN-SHARED-SECRET"

// resolveFlagToken parses args through the REAL install flag set and returns the
// token that resolved, so env-source precedence is pinned against the actual
// command rather than a re-declaration of it.
func resolveFlagToken(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := installCommand()
	var got string
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = c.String("token")
		return nil
	}
	err := cmd.Run(context.Background(), append([]string{"install"}, args...))
	return got, err
}

// newLocalTokenInstaller builds an installer aimed at a self-hosted server that
// WAS started with --token — the home-network shape, where the server binds a
// routable address and a shared secret stands in for the loopback boundary.
func newLocalTokenInstaller(t *testing.T, kit agentKit) (*Installer, *recordingRunner, string) {
	t.Helper()
	inst, rr, dir := newLocalInstaller(t, kit)
	inst.token = localServerToken
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
		if strings.Contains(c, "Authorization") {
			t.Errorf("token material leaked into a token-less local install: %q", c)
		}
	}
}

// TestLocalInstallPrefersLocalTokenEnv pins the one inheritance that
// must still be ignored. --token's env sources are AGENTSMEMORY_LOCAL_TOKEN then
// AGENTSMEMORY_TOKEN, so a developer holding a HOSTED workspace key in their
// shell must not have it written into a self-hosted config: the local variable is
// what pairs with the server's --token, and it wins.
func TestLocalInstallPrefersLocalTokenEnv(t *testing.T) {
	t.Setenv("AGENTSMEMORY_LOCAL_TOKEN", localServerToken)
	t.Setenv("AGENTSMEMORY_TOKEN", "HOSTED-KEY-THAT-MUST-NOT-WIN")

	tok, err := resolveFlagToken(t, "--local")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tok != localServerToken {
		t.Errorf("--token resolved to %q, want the local server token %q", tok, localServerToken)
	}
}

// TestLocalInstallClaudeRegistersHeaderWithToken is the home-network case: the
// server was started with --token, so the agent MUST send a bearer or every call
// 401s. This is the behaviour that reverses the old "--local never carries a
// token" rule, and it only applies to an EXPLICIT token — nothing is prompted.
func TestLocalInstallClaudeRegistersHeaderWithToken(t *testing.T) {
	inst, rr, _ := newLocalTokenInstaller(t, claudeKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	want := []string{
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + localMCPURL +
			" --header Authorization: Bearer " + localServerToken,
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Fatalf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestLocalInstallCodexWritesTokenFileWithToken mirrors the Claude case for
// codex, which cannot take a static header and instead reads the token from an
// env var at launch — so the file that was deliberately absent for a token-less
// local install must now be written.
func TestLocalInstallCodexWritesTokenFileWithToken(t *testing.T) {
	inst, rr, dir := newLocalTokenInstaller(t, codexKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, tokenFile))
	if err != nil {
		t.Fatalf("read %s: %v", tokenFile, err)
	}
	if got, want := strings.TrimSpace(string(raw)), tokenEnvVar+"="+localServerToken; got != want {
		t.Errorf("token file = %q, want %q", got, want)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + localMCPURL + " --bearer-token-env-var " + tokenEnvVar,
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestLocalInstallPiWritesTokenWithToken covers pi's bridge extension: with a
// token present the env file carries the credential and must NOT carry the
// "token absence is intentional" flag, or the extension would treat a real
// credential as an opt-out.
func TestLocalInstallPiWritesTokenWithToken(t *testing.T) {
	inst, _, dir := newLocalTokenInstaller(t, piKit)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, tokenFile))
	if err != nil {
		t.Fatalf("read %s: %v", tokenFile, err)
	}
	env := string(raw)
	for _, want := range []string{tokenEnvVar + "=" + localServerToken, mcpURLEnvVar + "=" + localMCPURL} {
		if !strings.Contains(env, want) {
			t.Errorf("env file missing %q, got %q", want, env)
		}
	}
	if strings.Contains(env, localEnvVar+"=1") {
		t.Errorf("env file sets the no-token flag despite carrying a token: %q", env)
	}
}

// TestLocalTokenSummaryEchoesServerFlag pins the follow-up step that turns a
// silent 401 into an obvious fix: if the agent was registered with a bearer, the
// server has to be started with the matching --token, and nothing else in the
// output says so.
func TestLocalTokenSummaryEchoesServerFlag(t *testing.T) {
	inst, _, _ := newLocalTokenInstaller(t, claudeKit)
	out := &bytes.Buffer{}
	inst.out = out
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if want := `agentsmemory --local --token "$` + localTokenEnvVar + `"`; !strings.Contains(out.String(), want) {
		t.Errorf("summary does not tell the operator to start the server with %q:\n%s", want, out.String())
	}
	// The value itself must not reach the terminal: this output is routinely pasted
	// into issues, and redactArgs already holds that line for --dry-run.
	if strings.Contains(out.String(), localServerToken) {
		t.Errorf("summary echoed the token value:\n%s", out.String())
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
