package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// sandboxRoot is the parent directory that holds per-project sandbox configs.
// Each sandbox is a self-contained Claude config dir, so wrapping a session with
// it (via CLAUDE_CONFIG_DIR) isolates that project's commands, settings, MCP
// servers, and agentsmemory token from every other project and from the global
// ~/.claude.
func sandboxRoot() string { return filepath.Join(homeDir(), ".sandboxes") }

// sandboxDir returns the config directory for the named sandbox. Callers must
// validate name with validSandboxName first.
func sandboxDir(name string) string { return filepath.Join(sandboxRoot(), name) }

// sandboxNameRe is the allowlist for sandbox names: it must start with an
// alphanumeric and then contain only alphanumerics, dash or underscore. A single
// allowlist rejects path separators, "."/".." traversal, leading-dot hidden
// names, and NUL/control bytes at once — safer than blocklisting known-bad forms,
// since the name feeds a filesystem path and CLAUDE_CONFIG_DIR.
var sandboxNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// validSandboxName rejects any name that isn't a plain identifier, so it can
// never escape ~/.sandboxes or steer CLAUDE_CONFIG_DIR to a surprising path.
func validSandboxName(name string) error {
	if name == "" {
		return errors.New("sandbox name is empty")
	}
	if !sandboxNameRe.MatchString(name) {
		return fmt.Errorf("invalid sandbox name %q: use letters, digits, dash or underscore only (no path separators or leading dot)", name)
	}
	return nil
}

// agentCLIs is the allowlist of agent CLIs that `run <name>` may launch directly
// when no sandbox by that name exists. It stays an explicit allowlist rather
// than "any binary on PATH" so a typo'd sandbox name still fails loudly with the
// install hint instead of silently exec'ing whatever shares the name.
var agentCLIs = map[string]bool{
	"claude": true,
	"codex":  true,
	"gemini": true,
	"pi":     true,
}

// launchPlan is the resolved outcome of `run <name>`: which agent binary to exec
// (empty means the kit's configured CLI), which config dir to pin, and the env var
// that pins it (CLAUDE_CONFIG_DIR or CODEX_HOME). An empty configDir means leave
// the variable alone, i.e. the agent's own global config.
type launchPlan struct {
	bin       string
	configDir string
	configEnv string
}

// planRun decides what `run <name>` launches for the selected kit. Sandboxes win:
// a name that has a config dir under ~/.sandboxes always means that sandbox, so
// existing usage is unchanged. Only when no such sandbox exists does a known agent
// name fall back to launching that agent against the global config — so
// `aiagentmemory run claude` does the obvious thing instead of failing on a sandbox
// nobody created.
//
// sandboxExists is passed in rather than stat'ed here so the decision is pure
// and testable without touching the filesystem.
func planRun(kit agentKit, name string, sandboxExists bool) (launchPlan, error) {
	if err := validSandboxName(name); err != nil {
		return launchPlan{}, err
	}
	if sandboxExists {
		return launchPlan{configDir: sandboxDir(name), configEnv: kit.configEnv}, nil
	}
	if agentCLIs[name] {
		return launchPlan{bin: name}, nil
	}
	return launchPlan{}, fmt.Errorf("sandbox config dir %s does not exist — run `aiagentmemory install --agent %s --sandbox %s` first",
		sandboxDir(name), kit.name, name)
}

// dirExists reports whether p is an existing directory. Any stat error (missing,
// permission denied, a plain file) counts as "no sandbox here", which is exactly
// the condition planRun branches on.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// execAgent replaces the current process with the planned agent CLI, optionally
// pinning the kit's config-dir variable to an isolated sandbox config dir. It
// exec-replaces (rather than spawning a child) so the terminal, signals, and exit
// code pass straight through — these agents are TUIs, and `aiagentmemory run foo`
// should behave exactly like running the agent, only against foo's configuration.
//
// The agent inherits this process's full environment, so shell-prefixed vars
// (`SET_NEW_ENV=1 aiagentmemory run foo`) reach it unchanged; only the config-dir
// variable and the stored agentsmemory token are layered on top.
func execAgent(kit agentKit, plan launchPlan, agentArgs []string) error {
	bin, err := resolveAgentBin(kit, plan.bin)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("cannot find the agent CLI %q on PATH: %w", bin, err)
	}

	env := os.Environ()
	configDir := plan.configDir
	if configDir != "" {
		// CLAUDE_CONFIG_DIR / CODEX_HOME is how each agent relocates its entire
		// config (settings, commands, MCP servers); setting it is what makes a
		// sandbox an isolated agent environment.
		env = setEnv(env, plan.configEnv, configDir)
	} else {
		configDir = kit.globalConfigDir(homeDir())
	}
	for k, v := range tokenEnv(configDir) {
		env = setEnv(env, k, v)
	}

	// syscall.Exec never returns on success; on failure it returns the errno.
	argv := append([]string{bin}, agentArgs...)
	return syscall.Exec(path, argv, env)
}

// tokenEnv reads the workspace token the install persisted in the config dir and
// returns it as KEY→VALUE pairs to layer onto the launched agent. Two agents read
// their MCP credentials from the environment rather than from their config: codex
// authes its HTTP MCP server from bearer_token_env_var, and pi's bridge extension
// reads the token and endpoint the same way. Without this the memory tools would
// be installed but unauthenticated whenever the user launches through us instead
// of exporting the variables in their shell.
//
// A missing or unreadable file yields nothing: the agent still launches and simply
// reports the MCP as unauthenticated, which beats refusing to start the session.
// The file is ours (written 0600 by the install), so it is parsed as plain
// KEY=VALUE lines with no shell semantics.
func tokenEnv(configDir string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(configDir, tokenFile))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

// setEnv returns env with key set to value, replacing any existing entry. It
// replaces rather than appends because a duplicate key in an execve environment is
// resolved by the first occurrence on the platforms we ship to — appending would
// silently lose to whatever the caller's shell already exported.
func setEnv(env []string, key, value string) []string {
	entry := key + "=" + value
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

// resolveAgentBin maps a planned agent name to the binary to exec. An empty name
// falls back to the kit's own CLI resolution, so --claude-bin /
// AIAGENTMEMORY_CLAUDE_BIN (and the codex equivalent) keep steering which build
// runs; any other allowlisted agent is exec'd under its own name.
func resolveAgentBin(kit agentKit, name string) (string, error) {
	switch name {
	case "", kit.bin:
		return resolveKitBin(kit, "", kitBinEnv(kit))
	default:
		return name, nil
	}
}

// kitBinEnv is the environment override naming which build of the agent CLI to
// run.
func kitBinEnv(kit agentKit) string {
	switch kit.name {
	case agentCodex:
		return "AIAGENTMEMORY_CODEX_BIN"
	case agentPi:
		return "AIAGENTMEMORY_PI_BIN"
	default:
		return "AIAGENTMEMORY_CLAUDE_BIN"
	}
}

// resolveClaudeBin decides which Claude CLI to drive. Precedence: an explicit
// override (the --claude-bin flag or AIAGENTMEMORY_CLAUDE_BIN), then plain
// `claude` on PATH. It returns the command name (not the resolved path) so
// callers can exec.LookPath it themselves.
func resolveClaudeBin(override string) (string, error) {
	return resolveKitBin(claudeKit, override, "AIAGENTMEMORY_CLAUDE_BIN")
}

// resolveKitBin decides which CLI to drive for a kit. Precedence: an explicit
// override (the --claude-bin / --codex-bin flag), then the kit's env override,
// then the kit's default name on PATH. It returns the command name (not the
// resolved path) so callers can exec.LookPath it themselves.
func resolveKitBin(kit agentKit, override, envVar string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv(envVar); env != "" {
		return env, nil
	}
	if _, err := exec.LookPath(kit.bin); err == nil {
		return kit.bin, nil
	}
	return "", fmt.Errorf("no %s CLI found on PATH (looked for %s); set the override flag or %s", kit.name, kit.bin, envVar)
}

// homeDir returns the user's home directory, falling back to $HOME. It does not
// fail hard here: callers that use the result build paths under it and will
// surface a clear filesystem error if the home dir is unusable.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// expandTilde rewrites a leading ~ to the user's home directory. It is used for
// the codebase-memory binary path handed to the Claude CLI, so the registered
// MCP command is an absolute path rather than a shell-relative ~ that a non-shell
// exec would not expand.
func expandTilde(p string) string {
	switch {
	case p == "~":
		return homeDir()
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(homeDir(), p[2:])
	default:
		return p
	}
}
