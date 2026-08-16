package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

// hookAsset is the embedded Stop-hook path inside the binary's embed FS.
const hookAsset = "hooks/agentsmemory-stop-hook.sh"

const (
	// hookFile is where the Stop hook is installed: flat in the config dir, not
	// under hooks/. The directory name matters because a sandbox is shared — pi
	// treats any hooks/ directory as its own deprecated layout and halts the
	// launch on a "press any key to continue" deprecation notice, even though the
	// directory is ours and has nothing to do with pi. Claude and codex register
	// the hook by absolute path, so where it lives is ours to choose.
	hookFile = "agentsmemory-stop-hook.sh"

	// legacyHookRel is where installs before that change put the hook. It is
	// removed on the next install (along with its now-stale Stop entry) so the
	// pi warning stops firing on sandboxes created earlier.
	legacyHookRel = "hooks/agentsmemory-stop-hook.sh"
)

// piExtensionAsset is the embedded pi bridge extension, installed at the same
// relative path under the target config dir — pi auto-discovers any *.ts under
// <config dir>/extensions. It is pi's stand-in for both the MCP registration and
// the Stop hook, neither of which pi supports natively.
const piExtensionAsset = "extensions/agentsmemory.ts"

const (
	// bootstrapAsset is the embedded always-on protocol; bootstrapFile is the name
	// it is installed under in the target config dir; memoryImportLine is the line
	// merged into CLAUDE.md to pull it in. Claude Code resolves an @import relative
	// to the importing file, so the import names a sibling of CLAUDE.md.
	bootstrapAsset   = "bootstrap.md"
	bootstrapFile    = "agentsmemory-bootstrap.md"
	memoryImportLine = "@agentsmemory-bootstrap.md"
)

const (
	// tokenEnvVar is the environment variable an agent reads the workspace bearer
	// token from. Two agents need it: unlike `claude mcp add`, `codex mcp add` has
	// no static-header flag, so an HTTP MCP server is authed with
	// `bearer_token_env_var` — codex stores the variable NAME and reads the value
	// from its own environment at launch — and pi has no MCP client at all, so our
	// bridge extension reads the same variable.
	tokenEnvVar = "AGENTSMEMORY_TOKEN"

	// mcpURLEnvVar tells the pi bridge extension which endpoint to talk to. Only
	// pi needs it: Claude and codex store the URL in their own MCP config, but
	// the extension has no config of its own to read.
	mcpURLEnvVar = "AGENTSMEMORY_MCP_URL"

	// localEnvVar tells the pi bridge extension that the endpoint is a self-hosted
	// `agentsmemory --local` server, so a missing token means "this server wants
	// none" rather than "the user skipped it". The extension needs the difference:
	// without a token it must stay silent against the hosted service (where it
	// would only 401), but connect anyway against a local one.
	localEnvVar = "AGENTSMEMORY_LOCAL"

	// tokenFile is where we persist that token (0600) inside the agent's config
	// dir, so `aiagentmemory run` can export it without the user wiring up a shell
	// rc. Kept beside the config it belongs to, so deleting a sandbox deletes its
	// token with it.
	tokenFile = "agentsmemory.env"
)

// commandRunner executes external commands on behalf of the installer. It is an
// interface so tests can record calls and --dry-run can print them without ever
// shelling out. Kept tiny on purpose (accept interfaces) so the whole install
// flow is exercisable end to end in a unit test.
type commandRunner interface {
	// run executes program name with args. env holds extra KEY=VALUE entries
	// appended to the current environment (used to pin CLAUDE_CONFIG_DIR).
	run(name string, args, env []string) error
	// runShell executes a shell pipeline — needed for the codebase-memory
	// `curl … | bash` one-liner, which has no argv form.
	runShell(script string) error
}

// execRunner is the production commandRunner: it runs commands for real and
// streams their output to the installer's writer.
type execRunner struct{ out io.Writer }

func (e execRunner) run(name string, args, env []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = e.out, e.out
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}

func (e execRunner) runShell(script string) error {
	// bash -c so the pipe (curl | bash) is interpreted; the upstream installer
	// is distributed exactly this way, so we run it exactly as documented.
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout, cmd.Stderr = e.out, e.out
	return cmd.Run()
}

// dryRunner prints the commands that would run and does nothing else. It backs
// --dry-run, letting a user preview the exact install plan (including the
// external Claude CLI calls) before committing to it.
type dryRunner struct{ out io.Writer }

func (d dryRunner) run(name string, args, env []string) error {
	var prefix strings.Builder
	for _, e := range env {
		prefix.WriteString(e)
		prefix.WriteByte(' ')
	}
	fmt.Fprintf(d.out, "  would run: %s%s %s\n", prefix.String(), name, strings.Join(redactArgs(args), " "))
	return nil
}

// redactArgs masks secret-bearing argument values so --dry-run never echoes a
// token to the terminal or a captured log. The Authorization bearer header is
// the only secret the installer passes on a command line.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "Authorization: Bearer ") {
			out[i] = "Authorization: Bearer ***"
		} else {
			out[i] = a
		}
	}
	return out
}

func (d dryRunner) runShell(script string) error {
	fmt.Fprintf(d.out, "  would run: %s\n", script)
	return nil
}

// Installer performs a single `install` invocation: it writes the embedded
// command + hook assets into a target Claude config dir, registers the Stop
// hook, wires up the agentsmemory MCP, and (with recommended=true) installs the
// companion extensions.
type Installer struct {
	kit            agentKit      // which agent CLI we are installing for (claude|codex|pi)
	targetDir      string        // agent config dir to install into (~/.claude, ~/.codex, ~/.pi/agent or a sandbox)
	sandboxName    string        // non-empty in isolated mode; drives messaging + run hint
	explicitTarget bool          // true when --sandbox/--config-dir pinned the target ⇒ skip the mode prompt
	agentBin       string        // resolved agent CLI name to drive for mcp/plugin ops
	mcpURL         string        // agentsmemory remote MCP endpoint
	socket         string        // non-empty ⇒ reach a --local server over this Unix socket, via the mcp-stdio bridge
	serverBin      string        // agentsmemory server binary the stdio bridge is spawned from (socket mode only)
	scope          string        // Claude MCP/plugin scope (user|local|project)
	local          bool          // target a self-hosted `agentsmemory --local` server: no token anywhere
	token          string        // agentsmemory workspace token (empty ⇒ prompt or skip)
	copyGlobal     bool          // seed the target from the agent's global config dir
	sharedAuth     bool          // link credentials back to the global config dir
	recommended    bool          // also install codebase-memory + eidos + codex
	yes            bool          // non-interactive: never prompt
	dryRun         bool          // print instead of doing
	out            io.Writer     // progress + banners
	in             io.Reader     // interactive prompt source (mode + token)
	reader         *bufio.Reader // shared line reader over in; lazily built so both prompts read one stream
	runner         commandRunner // how external commands execute (exec / dry / fake)
}

// resolveInstallTarget picks the install target from the mode flags and reports
// whether it was pinned on the command line. Precedence is --sandbox, then
// --config-dir, then an explicit --global, then the bare default (the kit's
// global dir: ~/.claude for Claude, ~/.codex for codex). explicit is true whenever
// the user named the target on the command line; when it is false, run() offers
// the interactive mode prompt so a bare `curl|bash` install isn't silently forced
// global.
//
// --global is the flag form of the global choice: it pins the kit's global dir and
// marks the target explicit, so `install --global --token <t>` is fully
// non-interactive. Because --global names the same target the bare default and the
// prompt would, combining it with --sandbox or --config-dir is ambiguous and
// rejected rather than silently resolved. home is passed in (not read here) so the
// helper is pure and testable.
//
// A sandbox holds both agents' configs in one directory: Claude and codex never
// share a filename (settings.json vs hooks.json, commands/ vs prompts/, CLAUDE.md
// vs AGENTS.md), so `install --agent both --sandbox x` yields one dir that
// CLAUDE_CONFIG_DIR and CODEX_HOME can each point at.
// --local implies the global target too, but as a DEFAULT rather than an
// assertion: someone self-hosting is setting up their own machine, so stopping to
// ask global-vs-sandbox is a prompt with an obvious answer. It therefore behaves
// like --global when no target is named, and yields to --sandbox/--config-dir
// when one is — which is why it is not part of the mutual-exclusion check below,
// and why "--local --sandbox x" is a legitimate combination (a local server, an
// isolated config) rather than an error.
func resolveInstallTarget(kit agentKit, global, local bool, sandbox, configDir, home string) (targetDir, sandboxName string, explicit bool, err error) {
	if global && (sandbox != "" || configDir != "") {
		return "", "", false, fmt.Errorf("--global cannot be combined with --sandbox or --config-dir")
	}
	switch {
	case sandbox != "":
		if err := validSandboxName(sandbox); err != nil {
			return "", "", false, err
		}
		return sandboxDir(sandbox), sandbox, true, nil
	case configDir != "":
		return configDir, "", true, nil
	case global, local:
		return kit.globalConfigDir(home), "", true, nil
	default:
		return kit.globalConfigDir(home), "", false, nil
	}
}

// serverBinCandidates are the names the agentsmemory server binary is commonly
// installed under, tried in order when --server-bin is not given: the release
// asset's own name first, then the shorter name the README's download snippet
// saves it as.
var serverBinCandidates = []string{"aiagentmemory-server", "agentsmemory"}

// resolveServerBin finds the server binary the stdio bridge will be spawned from
// and returns an ABSOLUTE path.
//
// Absolute matters: the agent launches this command itself, from whatever working
// directory it happens to be in and with a PATH that may not match the installing
// shell's. A bare name that resolves here can easily fail there, and the failure
// surfaces to the user as an MCP server that simply never connects.
//
// Under --dry-run a missing binary is tolerated so the plan still prints, matching
// how the agent CLI itself is resolved.
func resolveServerBin(flagValue string, dryRun bool) (string, error) {
	candidates := serverBinCandidates
	if flagValue != "" {
		candidates = []string{flagValue}
	}

	for _, name := range candidates {
		// LookPath handles both a bare name (searched on PATH) and an explicit
		// path, and confirms the file is actually executable either way.
		if path, err := exec.LookPath(name); err == nil {
			if abs, err := filepath.Abs(path); err == nil {
				return abs, nil
			}
			return path, nil
		}
	}

	if dryRun {
		return candidates[0], nil
	}
	return "", fmt.Errorf("cannot find the agentsmemory server binary (tried %s) — pass --server-bin /path/to/agentsmemory",
		strings.Join(candidates, ", "))
}

// newInstaller builds an Installer for one agent kit from parsed CLI flags. It
// resolves the target config dir (isolated sandbox vs the kit's global dir) and
// the agent CLI to drive, selecting a dry-run runner when --dry-run is set.
// `install --agent both` calls this once per kit.
func newInstaller(kit agentKit, c *cli.Command, out io.Writer, in io.Reader) (*Installer, error) {
	// Resolve the install target (and whether it was pinned on the command line)
	// from the mode flags. Kept as a pure helper so the precedence and the
	// mutually-exclusive-flags rule are testable without CLI plumbing.
	local := c.Bool("local")
	targetDir, sandboxName, explicitTarget, err := resolveInstallTarget(
		kit, c.Bool("global"), local, c.String("sandbox"), c.String("claude-dir"), homeDir())
	if err != nil {
		return nil, err
	}

	// --local swaps the endpoint default, not the endpoint: an explicit --mcp-url
	// still wins, so a self-hosted server on another port or host is reachable
	// with both flags.
	mcpURL := c.String("mcp-url")
	if local && !c.IsSet("mcp-url") {
		mcpURL = localMCPURL
	}

	dryRun := c.Bool("dry-run")

	// --socket registers the stdio bridge instead of an HTTP endpoint, which only
	// makes sense against a self-hosted server: the bridge carries no credential,
	// so pointing it at the multi-tenant service would register an MCP that can
	// only ever answer 401. Requiring --local says that up front instead.
	socket, serverBin := c.String("socket"), ""
	if socket != "" {
		if !local {
			return nil, fmt.Errorf("--socket requires --local: a socket-served MCP carries no token, so it only reaches a self-hosted server")
		}
		if serverBin, err = resolveServerBin(c.String("server-bin"), dryRun); err != nil {
			return nil, err
		}
	}

	// We always register our MCP, which needs the agent's own CLI, so resolve it
	// now. Under --dry-run tolerate a missing CLI so the plan can still be printed.
	agentBin, err := resolveAgentCLI(kit, c)
	if err != nil {
		if !dryRun {
			return nil, err
		}
		agentBin = kit.bin
	}

	var runner commandRunner = execRunner{out: out}
	if dryRun {
		runner = dryRunner{out: out}
	}

	return &Installer{
		kit:            kit,
		targetDir:      targetDir,
		sandboxName:    sandboxName,
		explicitTarget: explicitTarget,
		agentBin:       agentBin,
		mcpURL:         mcpURL,
		socket:         socket,
		serverBin:      serverBin,
		scope:          c.String("scope"),
		local:          local,
		token:          c.String("token"),
		copyGlobal:     c.Bool("copy"),
		sharedAuth:     c.Bool("shared-auth"),
		recommended:    c.Bool("recommended"),
		yes:            c.Bool("yes"),
		dryRun:         dryRun,
		out:            out,
		in:             in,
		runner:         runner,
	}, nil
}

// run executes the full install: assets + hook (core), our MCP (core), and the
// recommended extensions (opt-in). Core failures are fatal; the MCP and the
// extension steps are best-effort so a partial environment still leaves the
// useful pieces installed.
func (i *Installer) run() error {
	// Ask global-vs-sandbox before anything is written, so the banner and every
	// subsequent step reflect the chosen target. No-op unless we're interactive.
	i.promptInstallMode()
	i.banner()

	// Seeding runs before anything of ours is written, so the kit's own files
	// (which the copy never overwrites) land on top of the inherited config.
	if err := i.seedFromGlobal(); err != nil {
		return err
	}
	// Sharing comes after the copy: --copy may have just written a private
	// snapshot of the credentials, and a link supersedes a snapshot.
	if err := i.linkSharedAuth(); err != nil {
		return err
	}

	i.step("1/4  commands, memory protocol, Stop hook")
	if err := i.writeAssets(); err != nil {
		return fmt.Errorf("writing kit assets: %w", err)
	}
	if err := i.registerStopHook(); err != nil {
		return fmt.Errorf("registering Stop hook: %w", err)
	}
	if err := i.registerMemoryBootstrap(); err != nil {
		return fmt.Errorf("installing memory bootstrap: %w", err)
	}

	i.step("2/4  agentsmemory MCP")
	if err := i.registerAgentsMemoryMCP(); err != nil {
		// Non-fatal: the commands + hook are installed and useful on their own.
		i.warn("agentsmemory MCP not registered: %v", err)
	}

	i.step("3/4  recommended extensions")
	switch {
	case i.kit.name == agentPi:
		// Both companions need something pi does not have: codebase-memory is a
		// stdio MCP server and eidos/codex are Claude plugin marketplaces. Say so
		// instead of running an installer whose output nothing would consume.
		fmt.Fprintln(i.out, "  none for pi: codebase-memory is a stdio MCP and eidos/codex are Claude plugins — pi supports neither")
	case i.recommended:
		i.installRecommended()
	default:
		fmt.Fprintf(i.out, "  skipped (pass --recommended to add %s)\n", extensionsList(i.kit))
	}

	i.step("4/4  done")
	i.summary()
	return nil
}

// seedFromGlobal copies the agent's existing global configuration into the
// target dir before the kit is installed, so a fresh sandbox inherits the things
// that are painful to recreate: the provider logins in auth.json, the MCP servers
// and plugins already registered, custom skills, themes and settings.
//
// Only configuration travels. Conversation history, logs, caches and extracted
// binaries are excluded (see skipCopy) — a global ~/.codex runs to hundreds of
// megabytes, nearly all of it per-machine runtime state that a new sandbox is
// better off without.
//
// Nothing already in the target is overwritten, so this is safe to re-run: the
// copy fills gaps, the install then writes the kit on top.
func (i *Installer) seedFromGlobal() error {
	if !i.copyGlobal {
		return nil
	}
	src := i.kit.globalConfigDir(homeDir())
	// Copying the global dir onto itself would be a no-op at best; more likely the
	// user meant --sandbox and would otherwise get a silent nothing.
	if sameDir(src, i.targetDir) {
		return fmt.Errorf("--copy needs a target other than the global config dir: pass --sandbox <name> or --config-dir <dir>")
	}
	if _, err := os.Stat(src); err != nil {
		i.warn("--copy: no global %s config at %s — nothing to inherit", i.kit.name, src)
		return nil
	}

	i.step("0/4  inherit the global " + i.kit.name + " config")
	if i.dryRun {
		fmt.Fprintf(i.out, "  would copy %s → %s (config, credentials, plugins and skills; no history, logs or caches)\n", src, i.targetDir)
		return nil
	}
	stats, err := copyConfigTree(src, i.targetDir)
	if err != nil {
		// A partial copy is still useful, and the install that follows is what the
		// user actually asked for — report and carry on rather than abort.
		i.warn("--copy: %v (copied %d files before stopping)", err, stats.Files)
		return nil
	}
	i.ok("copied %d files (%s) from %s", stats.Files, humanBytes(stats.Bytes), src)
	if stats.Skipped > 0 {
		i.ok("kept %d file(s) already in the target untouched", stats.Skipped)
	}
	fmt.Fprintln(i.out, "  note: credentials came too — this config can act as you until you sign it out")
	return nil
}

// sameDir reports whether two paths name the same directory, resolving symlinks
// so ~/.claude and a symlinked twin are not treated as different targets.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// writeAssets writes the embedded slash commands and the Stop hook into the
// target config dir. M.md and am.md are the bootstrap commands; load-skill.md is
// the /load-skill nicety over the am_load_skill tool. The legacy agentsmemory.md
// was retired and is intentionally not shipped.
//
// The same markdown serves both agents: codex reads top-level files in
// <CODEX_HOME>/prompts with the same `description:` / `argument-hint:` front
// matter and `$ARGUMENTS` expansion Claude uses for commands/, so only the
// directory name (and the `/prompts:` invocation prefix) differs.
func (i *Installer) writeAssets() error {
	for _, name := range []string{"M.md", "am.md", "load-skill.md"} {
		data, err := assets.ReadFile("commands/" + name)
		if err != nil {
			return err // embed guarantees presence; an error here is a build bug
		}
		if err := i.writeFile(filepath.Join(i.targetDir, i.kit.commandsDir, name), data, 0o644); err != nil {
			return err
		}
		i.ok("command %s", i.commandLabel(name))
	}

	// An agent with no hook system gets no hook script: pi retired hooks/ in
	// favour of extensions, so its end-of-turn checkpoint ships inside the bridge
	// extension (see registerPiMCP) and a stray .sh here would only confuse.
	if i.kit.hooksFile == "" {
		i.notePiLegacyHook()
		return nil
	}

	hook, err := assets.ReadFile(hookAsset)
	if err != nil {
		return err
	}
	if err := i.writeFile(i.hookPath(), hook, 0o755); err != nil {
		return err
	}
	i.ok("hook %s", filepath.Base(i.hookPath()))
	// Only a hook-owning kit relocates the script: it is the one that also
	// re-registers the new path, so no agent is left pointing at a deleted file.
	i.clearLegacyHook()
	return nil
}

// notePiLegacyHook warns when a pi install finds a hooks/ directory it must not
// touch. pi halts its launch on one, but the directory belongs to the Claude or
// codex kit installed in this same (shared) config dir: deleting the script here
// would leave that agent's Stop registration pointing at a missing file. So the
// user is told which install re-locates it instead.
func (i *Installer) notePiLegacyHook() {
	if _, err := os.Stat(filepath.Join(i.targetDir, "hooks")); err != nil {
		return
	}
	i.warn("pi halts on the hooks/ directory in %s", i.targetDir)
	fmt.Fprintf(i.out, "       it belongs to the Claude/codex kit — re-run that install to relocate it:\n")
	fmt.Fprintf(i.out, "         aiagentmemory install --agent both --config-dir %s --yes\n", i.targetDir)
}

// hookPath is the absolute install path of the Stop hook under the target dir.
func (i *Installer) hookPath() string { return filepath.Join(i.targetDir, hookFile) }

// legacyHookPath is where earlier installs wrote the hook, under hooks/.
func (i *Installer) legacyHookPath() string { return filepath.Join(i.targetDir, legacyHookRel) }

// clearLegacyHook removes the pre-relocation hook script and, if it leaves the
// directory empty, the hooks/ directory itself — which is the whole point: pi
// halts its launch on any hooks/ directory in a config dir it shares. The
// directory is only removed when empty, so a hooks/ folder holding the user's own
// scripts is left alone (they keep the warning, but losing their files would be
// far worse).
func (i *Installer) clearLegacyHook() {
	legacy := i.legacyHookPath()
	if _, err := os.Stat(legacy); err != nil {
		return // nothing from an older install here
	}
	if i.dryRun {
		fmt.Fprintf(i.out, "  would remove the legacy hook %s (pi halts on a hooks/ dir)\n", legacy)
		return
	}
	if err := os.Remove(legacy); err != nil {
		i.warn("could not remove the legacy hook %s: %v", legacy, err)
		return
	}
	// os.Remove on a directory succeeds only when it is empty, which is exactly
	// the condition we want — no need to read it first.
	if err := os.Remove(filepath.Dir(legacy)); err == nil {
		i.ok("removed the legacy hooks/ directory (pi halts on it)")
	} else {
		i.ok("removed the legacy hook script from hooks/")
	}
}

// writeFile writes data to path with perm, creating parent dirs. Under dry-run
// it prints the intended write instead of touching the filesystem.
func (i *Installer) writeFile(path string, data []byte, perm os.FileMode) error {
	if i.dryRun {
		fmt.Fprintf(i.out, "  would write: %s (%d bytes, %#o)\n", path, len(data), perm)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// registerStopHook adds the Stop hook to the agent's hook JSON, idempotently —
// settings.json for Claude, hooks.json for codex. Both take the same shape
// ({"hooks":{"Stop":[{"hooks":[{"type":"command","command":…}]}]}}) and both hand
// the hook a Stop event carrying stop_hook_active, which is what our script uses
// for loop prevention, so one script and one merge serve both.
//
// Codex additionally gates non-managed hooks behind a trust review: the hook is
// listed but skipped until it is trusted in `/hooks`. summary() says so.
func (i *Installer) registerStopHook() error {
	if i.kit.hooksFile == "" {
		// pi has no hook system at all; the checkpoint rides in the extension.
		i.ok("%s has no hooks — the memory checkpoint ships in the bridge extension", i.kit.name)
		return nil
	}
	hookCmd := "bash " + i.hookPath()
	hooksFile := filepath.Join(i.targetDir, i.kit.hooksFile)
	if i.dryRun {
		fmt.Fprintf(i.out, "  would register Stop hook in %s: %q\n", hooksFile, hookCmd)
		return nil
	}
	changed, err := ensureStopHook(hooksFile, hookCmd, foreignHookPredicate(hookCmd))
	if err != nil {
		return err
	}
	if changed {
		i.ok("registered Stop hook in %s", i.kit.hooksFile)
	} else {
		i.ok("Stop hook already registered")
	}
	return nil
}

// foreignHookPredicate matches any Stop registration of our hook script that is
// not the one this install is writing. Two of those turn up: the pre-relocation
// entry under hooks/ (whose script the install has just deleted), and an entry
// inherited by --copy from another config dir (whose script still exists, so the
// checkpoint would fire twice every stop). Both are ours to retire; a hook the
// user wrote never matches, because the match is on our own filename.
func foreignHookPredicate(keep string) func(string) bool {
	return func(cmd string) bool {
		return cmd != keep && strings.Contains(cmd, hookFile)
	}
}

// registerAgentsMemoryMCP wires up the agentsmemory remote MCP. It resolves the
// workspace token (flag/env, else an interactive prompt) and registers the HTTP
// server with the agent's own CLI. This is the product's core value, so it runs in
// the default install — not gated behind --recommended.
//
// The two CLIs authenticate an HTTP MCP server differently, so the registration
// itself is the one step that genuinely diverges per agent.
func (i *Installer) registerAgentsMemoryMCP() error {
	token := i.resolveToken()
	// A self-hosted --local server has no credential to present, so an empty token
	// is the expected state there rather than the "user skipped it" state below.
	if token == "" && !i.local {
		fmt.Fprintln(i.out, "  no token provided — skipping agentsmemory MCP.")
		fmt.Fprintf(i.out, "  add it later: %s\n", i.mcpAddHint())
		return nil
	}
	// A socket has no URL, so the agent cannot speak HTTP to it: it spawns the
	// server's own mcp-stdio bridge instead. This is checked before the per-agent
	// split because the stdio registration is identical for Claude and codex.
	if i.socket != "" {
		return i.registerSocketMCP()
	}

	switch i.kit.name {
	case agentCodex:
		return i.registerCodexMCP(token)
	case agentPi:
		return i.registerPiMCP(token)
	default:
		return i.registerClaudeMCP(token)
	}
}

// registerSocketMCP wires the agent to a server listening on a Unix socket, via
// the mcp-stdio bridge built into the server binary.
//
// No token is passed. --socket requires --local (enforced in newInstaller), and
// a local server authenticates by socket permissions rather than a credential —
// which is just as well, since an MCP command line is stored in plain config and
// visible in `ps`, so a token on argv would leak.
func (i *Installer) registerSocketMCP() error {
	if i.kit.name == agentPi {
		// pi has no MCP client of its own; its bridge extension speaks HTTP to a
		// URL. Spawning a stdio child is a different mechanism entirely, so this
		// is reported rather than silently registered as something else.
		return fmt.Errorf("--socket is not supported for pi (its bridge extension connects over HTTP): run the server on --addr and install pi with --mcp-url")
	}

	argv := []string{"mcp-stdio", "--socket", i.socket}
	if err := i.addStdioMCP(mcpName, i.serverBin, argv...); err != nil {
		return err
	}
	i.ok("registered MCP %q → %s (stdio bridge to %s)", mcpName, i.socket, i.serverBin)
	return nil
}

// registerClaudeMCP registers the remote MCP with the Claude CLI, which takes the
// bearer token inline as a header value.
func (i *Installer) registerClaudeMCP(token string) error {
	args := []string{"mcp", "add", "--transport", "http", "--scope", i.scope, mcpName, i.mcpURL}
	// A token-less server takes no Authorization header at all. Sending an empty
	// bearer would work against our own --local server (which ignores inbound
	// credentials) but is a lie in the config file: it reads as auth that exists.
	if token != "" {
		args = append(args, "--header", "Authorization: Bearer "+token)
	}
	// `mcp add` is not idempotent by name, so remove any prior entry first
	// (ignoring "not found") and then add cleanly, all in one shot.
	i.agent(true, "mcp", "remove", "--scope", i.scope, mcpName)
	if err := i.agent(false, args...); err != nil {
		return err
	}
	i.ok("registered MCP %q → %s", mcpName, i.mcpURL)
	return nil
}

// registerCodexMCP registers the remote MCP with codex. `codex mcp add` has no
// static-header flag: a streamable-HTTP server is authed with
// --bearer-token-env-var, which persists the variable NAME in config.toml and
// makes codex read the value from its environment at launch. So we register the
// variable and persist the token itself (0600) inside CODEX_HOME, where
// `aiagentmemory run --agent codex` picks it up. Users who launch plain `codex`
// get the export line in summary().
//
// Writing the token to a file we own beats the alternatives: rewriting the user's
// config.toml to hold a static Authorization header would reformat a file that
// also carries their plugins, hook trust hashes and shell policy, and passing it
// on argv would leak it to `ps`.
func (i *Installer) registerCodexMCP(token string) error {
	args := []string{"mcp", "add", mcpName, "--url", i.mcpURL}
	// With no token there is nothing to persist and no variable for codex to read,
	// so the token file is not written at all — an empty AGENTSMEMORY_TOKEN file
	// would only mislead the next reader (and summary() would tell them to source
	// it for nothing).
	if token != "" {
		if err := i.writeFile(i.tokenPath(), []byte(tokenEnvVar+"="+token+"\n"), 0o600); err != nil {
			return err
		}
		i.ok("stored workspace token in %s (0600)", tokenFile)
		args = append(args, "--bearer-token-env-var", tokenEnvVar)
	}

	// Same remove-then-add shape as Claude: `codex mcp add` fails on a name that
	// already exists, and `remove` fails when nothing is there — so ignore that one.
	i.agent(true, "mcp", "remove", mcpName)
	if err := i.agent(false, args...); err != nil {
		return err
	}
	if token == "" {
		i.ok("registered MCP %q → %s (no token: self-hosted server)", mcpName, i.mcpURL)
		return nil
	}
	i.ok("registered MCP %q → %s (token via $%s)", mcpName, i.mcpURL, tokenEnvVar)
	return nil
}

// registerPiMCP wires the remote MCP into pi. pi ships no MCP client — it
// "intentionally does not include built-in MCP" and points at extensions instead
// — so there is no `pi mcp add` to call. Instead we install our bridge extension
// into <config dir>/extensions, where pi auto-discovers it: at startup it lists
// the remote tools and re-registers each one as a native pi tool, so `am_*` calls
// in the memory protocol work unchanged. The same extension carries the
// end-of-turn checkpoint that the Stop hook provides on the other agents.
//
// The extension reads its endpoint and token from the environment (it has no
// config of its own), so both are persisted 0600 beside it and exported by
// `aiagentmemory run --agent pi`. Nothing is passed on argv, which would leak the
// token to `ps`.
func (i *Installer) registerPiMCP(token string) error {
	ext, err := assets.ReadFile(piExtensionAsset)
	if err != nil {
		return err // embed guarantees presence; an error here is a build bug
	}
	if err := i.writeFile(filepath.Join(i.targetDir, piExtensionAsset), ext, 0o644); err != nil {
		return err
	}
	i.ok("installed pi bridge extension %s", piExtensionAsset)

	// A local server needs no token, so the file carries the endpoint plus the flag
	// that tells the extension the absence is intentional. Everything the
	// extension reads still lives in one file that `aiagentmemory run` exports.
	env := fmt.Sprintf("%s=%s\n%s=%s\n", tokenEnvVar, token, mcpURLEnvVar, i.mcpURL)
	if token == "" {
		env = fmt.Sprintf("%s=%s\n%s=1\n", mcpURLEnvVar, i.mcpURL, localEnvVar)
	}
	if err := i.writeFile(i.tokenPath(), []byte(env), 0o600); err != nil {
		return err
	}
	if token == "" {
		i.ok("stored endpoint in %s (0600; no token: self-hosted server)", tokenFile)
		i.ok("bridged MCP %q → %s", mcpName, i.mcpURL)
		return nil
	}
	i.ok("stored workspace token + endpoint in %s (0600)", tokenFile)
	i.ok("bridged MCP %q → %s (token via $%s)", mcpName, i.mcpURL, tokenEnvVar)
	return nil
}

// resolveAgentCLI picks the CLI binary to drive for the kit, honouring the
// per-agent override flag (--claude-bin / --codex-bin / --pi-bin) and its env var.
func resolveAgentCLI(kit agentKit, c *cli.Command) (string, error) {
	switch kit.name {
	case agentCodex:
		return resolveKitBin(kit, c.String("codex-bin"), kitBinEnv(kit))
	case agentPi:
		return resolveKitBin(kit, c.String("pi-bin"), kitBinEnv(kit))
	default:
		return resolveKitBin(kit, c.String("claude-bin"), kitBinEnv(kit))
	}
}

// tokenPath is where the workspace token is persisted inside CODEX_HOME.
func (i *Installer) tokenPath() string { return filepath.Join(i.targetDir, tokenFile) }

// mcpAddHint is the command a user runs to add the MCP later, when they skipped
// the token prompt. It mirrors exactly what the installer would have run.
func (i *Installer) mcpAddHint() string {
	switch i.kit.name {
	case agentCodex:
		return fmt.Sprintf("%s=<token> %s mcp add %s --url %s --bearer-token-env-var %s",
			tokenEnvVar, i.agentBin, mcpName, i.mcpURL, tokenEnvVar)
	case agentPi:
		// pi has no `mcp add`; the bridge is our own extension plus its env file,
		// so the way to add it later is to re-run this installer with a token.
		return fmt.Sprintf("aiagentmemory install --agent pi --config-dir %s --token <token>", i.targetDir)
	default:
		return fmt.Sprintf("%s mcp add --transport http %s %s --header \"Authorization: Bearer <token>\"",
			i.agentBin, mcpName, i.mcpURL)
	}
}

// registerMemoryBootstrap installs the always-on operating protocol so the
// memory-first workflow applies every session without the user typing /am. It
// writes our owned copy of the embedded protocol as agentsmemory-bootstrap.md and
// merges a managed block into the agent's memory file. Both agents load that file
// as user memory from their config dir, so this applies in a sandbox (where we own
// the whole dir) and in the global dir (where the merge preserves whatever the
// user already wrote).
//
// What goes in the block differs: Claude Code resolves `@file.md` imports, so it
// gets a one-line import of the sibling protocol file — edit the file, every
// session picks it up. Codex has no import directive in AGENTS.md, so the protocol
// is inlined there instead; the sibling copy is still written, as the file the
// block is regenerated from on the next install.
func (i *Installer) registerMemoryBootstrap() error {
	data, err := assets.ReadFile(bootstrapAsset)
	if err != nil {
		return err // embed guarantees presence; an error here is a build bug
	}
	bootstrapPath := filepath.Join(i.targetDir, bootstrapFile)
	if err := i.writeFile(bootstrapPath, data, 0o644); err != nil {
		return err
	}
	i.ok("memory protocol %s", bootstrapFile)

	body := memoryImportLine
	if !i.kit.supportsImport {
		body = string(data)
	}

	// The block lands in the user's memory file, so it goes through the managed
	// idempotent merge (not a blind overwrite). Under dry-run, print the intent —
	// mirroring registerStopHook, which also can't preview through the merge.
	memoryPath := filepath.Join(i.targetDir, i.kit.memoryFile)
	if i.dryRun {
		fmt.Fprintf(i.out, "  would merge the memory protocol into %s (managed block)\n", memoryPath)
		return nil
	}
	changed, err := ensureManagedBlock(memoryPath, body)
	if err != nil {
		return err
	}
	if changed {
		i.ok("merged memory protocol into %s", i.kit.memoryFile)
	} else {
		i.ok("%s already carries the memory protocol", i.kit.memoryFile)
	}
	return nil
}

// installRecommended installs the companion ecosystem. Both agents get the
// codebase-memory MCP (its own installer + a stdio registration); Claude
// additionally gets the eidos and codex plugins, which live in Claude plugin
// marketplaces and have no codex equivalent. Each step is best-effort — one
// already-installed plugin or a network hiccup should not abort the whole install
// — so failures are reported, not fatal.
func (i *Installer) installRecommended() {
	// Register the stdio MCP only if its binary actually landed: if the upstream
	// installer failed, pointing the agent CLI at a missing path would register
	// a broken server. (--dry-run still shows the full plan.)
	shellErr := i.runner.runShell(codebaseMemoryInstall)
	if shellErr != nil {
		i.warn("codebase-memory install script failed: %v", shellErr)
	} else {
		i.ok("installed codebase-memory-mcp")
	}
	bin := expandTilde(codebaseMemoryBin)
	if shellErr == nil || i.dryRun {
		if err := i.addStdioMCP(codebaseMemoryName, bin); err != nil {
			i.warn("register codebasememory MCP failed: %v", err)
		} else {
			i.ok("registered MCP %q → %s", codebaseMemoryName, bin)
		}
	} else {
		i.warn("skipping codebasememory MCP registration — installer did not complete")
	}

	if i.kit.name == agentCodex {
		// eidos and codex are Claude plugin marketplaces; codex has its own
		// (openai-bundled) and carries no equivalent, so say what is not happening
		// rather than silently installing less than the flag promises.
		fmt.Fprintln(i.out, "  note: the eidos and codex plugins are Claude-only — nothing to install for codex")
		return
	}

	// Marketplace add is effectively idempotent; ignore its error and let the
	// install surface any real problem.
	for _, p := range []struct{ marketplace, plugin string }{
		{"agenticnotetaking/eidos", "eidos@eidos"},
		{"openai/codex-plugin-cc", "codex@openai-codex"},
	} {
		i.agent(true, "plugin", "marketplace", "add", p.marketplace)
		if err := i.agent(false, "plugin", "install", p.plugin); err != nil {
			i.warn("install plugin %s failed: %v", p.plugin, err)
		} else {
			i.ok("installed plugin %s", p.plugin)
		}
	}
}

// addStdioMCP registers a local stdio MCP server, remove-then-add so a re-run is
// idempotent. The two CLIs spell it differently: Claude scopes the entry and marks
// the command with --transport stdio, codex infers stdio from a trailing command
// and has no scope.
//
// argv carries any arguments the command needs after the binary — everything past
// the `--` separator, so a flag like --socket reaches the server rather than the
// agent CLI parsing it as its own.
func (i *Installer) addStdioMCP(name, bin string, argv ...string) error {
	if i.kit.name == agentCodex {
		i.agent(true, "mcp", "remove", name)
		return i.agent(false, append([]string{"mcp", "add", name, "--", bin}, argv...)...)
	}
	i.agent(true, "mcp", "remove", "--scope", i.scope, name)
	return i.agent(false, append([]string{"mcp", "add", "--transport", "stdio", "--scope", i.scope, name, "--", bin}, argv...)...)
}

// agent runs the resolved agent CLI with its config-dir env var (CLAUDE_CONFIG_DIR
// or CODEX_HOME) pinned to the target dir, so MCP/plugin registration lands in the
// config we are installing into (a sandbox or the global dir) rather than wherever
// the process happens to point. When ignoreErr is true a failure is swallowed —
// used for the pre-emptive `mcp remove` and `marketplace add` that legitimately
// fail when nothing exists.
func (i *Installer) agent(ignoreErr bool, args ...string) error {
	env := []string{i.kit.configEnv + "=" + i.targetDir}
	if err := i.runner.run(i.agentBin, args, env); err != nil && !ignoreErr {
		return err
	}
	return nil
}

// promptInstallMode asks, interactively, whether to install globally or into an
// isolated sandbox — the choice a bare `curl|bash` user otherwise never gets,
// since the mode is only selectable via the --sandbox flag and thus defaults to
// global silently. It runs only when no target was pinned on the command line and
// we can actually interact (not --yes, not --dry-run). On blank input or EOF it
// leaves the global default in place; a typed, valid name switches the install to
// that sandbox. It never fails the install: an unreadable stdin just falls back
// to global, which is the safe, documented default.
func (i *Installer) promptInstallMode() {
	// Respect an explicit choice and every non-interactive path. install.sh adds
	// --yes when there is no /dev/tty (CI), so this correctly stays silent there.
	if i.explicitTarget || i.yes || i.dryRun {
		return
	}
	fmt.Fprintln(i.out, "Where should the kit be installed?")
	fmt.Fprintln(i.out, "  - press Enter for a GLOBAL install into ~/.claude (wraps your existing Claude)")
	fmt.Fprintln(i.out, "  - or type a NAME for an isolated sandbox at ~/.sandboxes/<name>")
	for {
		fmt.Fprint(i.out, "Sandbox name (blank = global): ")
		line, err := i.line()
		name := strings.TrimSpace(line)
		if name == "" {
			// Blank line, or EOF on a piped/closed stdin → keep global default.
			return
		}
		if verr := validSandboxName(name); verr != nil {
			fmt.Fprintf(i.out, "  %v\n", verr)
			if err != nil {
				// Reader is exhausted (EOF); don't spin forever re-prompting a
				// closed stdin — fall back to the global default.
				return
			}
			continue // re-prompt on a live terminal
		}
		i.sandboxName = name
		i.targetDir = sandboxDir(name)
		return
	}
}

// line reads one line from the shared prompt reader, building it from i.in on
// first use. A single *bufio.Reader is essential: two separate bufio readers over
// the same terminal fd would let the first buffer-read swallow bytes meant for
// the second, so the mode prompt and the token prompt must share this one.
func (i *Installer) line() (string, error) {
	if i.reader == nil {
		i.reader = bufio.NewReader(i.in)
	}
	return i.reader.ReadString('\n')
}

// resolveToken returns the agentsmemory token from --token/env, or prompts for
// it interactively. Under --dry-run it returns a visible placeholder so the plan
// prints the full `mcp add`. In --yes / non-interactive mode (or on an empty
// stdin) it returns "" and the caller skips MCP registration with a hint.
func (i *Installer) resolveToken() string {
	// A self-hosted server issues no tokens, so prompting for one would ask a
	// question with no possible answer. Any inherited AGENTSMEMORY_TOKEN (the
	// --token flag reads that env var) is dropped too, rather than written into a
	// config where it would imply the local server checks it.
	if i.local {
		return ""
	}
	if i.token != "" {
		return i.token
	}
	if i.dryRun {
		return "<token>"
	}
	if i.yes {
		return ""
	}
	fmt.Fprint(i.out, "  Enter your agentsmemory workspace API token (blank to skip): ")
	line, err := i.line()
	if err != nil && line == "" {
		return "" // EOF (piped / non-interactive stdin) → skip
	}
	return strings.TrimSpace(line)
}

// --- terminal UX helpers -------------------------------------------------
//
// Output is intentionally plain ASCII (no ANSI): it stays readable when piped
// to a log or captured in a test, and the curl|bash installer often runs with
// stdout redirected.

// banner prints the header block describing the install target and mode.
func (i *Installer) banner() {
	fmt.Fprintln(i.out, "== agentsmemory installer ==")
	fmt.Fprintf(i.out, "agent       : %s\n", i.kit.name)
	fmt.Fprintf(i.out, "mode        : %s\n", i.modeLabel())
	fmt.Fprintf(i.out, "config dir  : %s\n", i.targetDir)
	fmt.Fprintf(i.out, "agent CLI   : %s\n", i.agentBin)
	fmt.Fprintf(i.out, "extensions  : %s\n", i.extensionsLabel())
	if i.dryRun {
		fmt.Fprintln(i.out, "dry-run     : no files written, no commands run")
	}
}

// modeLabel names the install mode for the banner.
func (i *Installer) modeLabel() string {
	if i.sandboxName != "" {
		return "isolated sandbox " + i.sandboxName
	}
	return fmt.Sprintf("global (wrap your existing %s)", i.kit.name)
}

// commandLabel renders how an installed command file is invoked in this agent —
// "/M" on Claude, "/prompts:M" on codex, since codex namespaces prompt files.
func (i *Installer) commandLabel(assetName string) string {
	return strings.Replace(i.kit.commandHint, "M", strings.TrimSuffix(assetName, ".md"), 1)
}

// extensionsLabel describes whether the recommended extensions are included.
func (i *Installer) extensionsLabel() string {
	if i.kit.name == agentPi {
		return "core only (pi takes neither a stdio MCP nor Claude plugins)"
	}
	if i.recommended {
		return "core + recommended (" + extensionsList(i.kit) + ")"
	}
	return "core only"
}

// extensionsList names the companion extensions --recommended installs for the
// kit. The eidos and codex plugins live in Claude plugin marketplaces, so a codex
// install gets the codebase-memory MCP only.
func extensionsList(kit agentKit) string {
	if kit.name == agentCodex {
		return "codebase-memory"
	}
	return "codebase-memory, eidos, codex"
}

func (i *Installer) step(title string)       { fmt.Fprintf(i.out, "\n> %s\n", title) }
func (i *Installer) ok(f string, a ...any)   { fmt.Fprintf(i.out, "  [ok] "+f+"\n", a...) }
func (i *Installer) warn(f string, a ...any) { fmt.Fprintf(i.out, "  [!!] "+f+"\n", a...) }

// summary prints the closing next-steps block, tailored to the agent and the
// install mode. The codex lines carry two things Claude does not need: the hook
// trust review codex requires before a non-managed hook runs, and the token env
// var, which codex reads from its environment rather than from its config.
func (i *Installer) summary() {
	fmt.Fprintln(i.out)
	fmt.Fprintln(i.out, "Next steps:")
	if i.sandboxName != "" {
		fmt.Fprintf(i.out, "  - launch it in this sandbox:  aiagentmemory run --agent %s %s\n", i.kit.name, i.sandboxName)
	} else {
		fmt.Fprintf(i.out, "  - restart %s to pick up the new commands + hook\n", i.kit.name)
	}
	fmt.Fprintf(i.out, "  - the memory protocol auto-loads every session via %s — no need to type %s\n",
		i.kit.memoryFile, i.commandLabel("am.md"))
	fmt.Fprintf(i.out, "  - run %s or %s with a task to run the full grounding sequence on demand\n",
		i.commandLabel("M.md"), i.commandLabel("am.md"))

	if i.local {
		// The self-hosted server is the one thing that has to be running for any of
		// this to work, and nothing else in the output would say so. The reminder
		// must echo the transport that was actually registered: telling a socket
		// install to run the server on a port would wire up a bridge that dials a
		// socket nothing is listening on.
		if i.socket != "" {
			fmt.Fprintf(i.out, "  - keep your server running: agentsmemory --local --socket %s   (this install bridges to that socket over stdio)\n", i.socket)
		} else {
			fmt.Fprintf(i.out, "  - keep your server running: agentsmemory --local   (this install points at %s)\n", i.mcpURL)
		}
	}

	if i.kit.name == agentPi {
		fmt.Fprintln(i.out, "  - pi has no MCP client: the memory tools arrive through the bridge extension in extensions/")
		if i.sandboxName != "" {
			// PI_CODING_AGENT_DIR relocates the whole agent dir, and pi keeps
			// auth.json there — so an isolated config starts with no provider
			// credentials of its own.
			fmt.Fprintf(i.out, "  - a sandbox has its own auth.json: sign in inside it, or pass a provider key (PI_CODING_AGENT_DIR=%s pi)\n", i.targetDir)
		}
		what := "the token"
		if i.local {
			what = "the endpoint" // no token was written; the file carries the URL
		}
		fmt.Fprintf(i.out, "  - launching plain `pi`? export %s first, e.g. add to your shell rc:\n", what)
		fmt.Fprintf(i.out, "      set -a; . %s; set +a\n", i.tokenPath())
		return
	}

	if i.kit.name != agentCodex {
		return
	}
	fmt.Fprintln(i.out, "  - codex skips untrusted hooks: open /hooks in codex and trust the agentsmemory Stop hook")
	if i.sandboxName != "" {
		// A sandbox is a whole CODEX_HOME, and codex keeps auth.json there — so an
		// isolated config starts logged out and every request 401s until you say so.
		fmt.Fprintf(i.out, "  - a sandbox has its own login: CODEX_HOME=%s codex login\n", i.targetDir)
	}
	// Only meaningful when a token was actually persisted; a --local install wrote
	// no file, and codex reads the endpoint straight out of its own config.toml.
	if i.local {
		return
	}
	fmt.Fprintf(i.out, "  - launching plain `codex`? export the token first, e.g. add to your shell rc:\n")
	fmt.Fprintf(i.out, "      set -a; . %s; set +a\n", i.tokenPath())
}
