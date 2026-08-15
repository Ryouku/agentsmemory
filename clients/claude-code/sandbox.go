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
}

// launchPlan is the resolved outcome of `run <name>`: which agent binary to exec
// (empty means the configured Claude CLI) and which config dir to pin via
// CLAUDE_CONFIG_DIR (empty means leave it alone, i.e. the global config).
type launchPlan struct {
	bin       string
	configDir string
}

// planRun decides what `run <name>` launches. Sandboxes win: a name that has a
// config dir under ~/.sandboxes always means that sandbox, so existing usage is
// unchanged. Only when no such sandbox exists does a known agent name fall back
// to launching that agent against the global config — so `aiagentmemory run
// claude` does the obvious thing instead of failing on a sandbox nobody created.
//
// sandboxExists is passed in rather than stat'ed here so the decision is pure
// and testable without touching the filesystem.
func planRun(name string, sandboxExists bool) (launchPlan, error) {
	if err := validSandboxName(name); err != nil {
		return launchPlan{}, err
	}
	if sandboxExists {
		return launchPlan{configDir: sandboxDir(name)}, nil
	}
	if agentCLIs[name] {
		return launchPlan{bin: name}, nil
	}
	return launchPlan{}, fmt.Errorf("sandbox config dir %s does not exist — run `aiagentmemory install --sandbox %s` first",
		sandboxDir(name), name)
}

// dirExists reports whether p is an existing directory. Any stat error (missing,
// permission denied, a plain file) counts as "no sandbox here", which is exactly
// the condition planRun branches on.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// execAgent replaces the current process with the planned agent CLI, optionally
// pinning CLAUDE_CONFIG_DIR to an isolated sandbox config dir. It exec-replaces
// (rather than spawning a child) so the terminal, signals, and exit code pass
// straight through — Claude is a TUI, and `aiagentmemory run foo` should behave
// exactly like running claude, only against foo's configuration.
//
// The agent inherits this process's full environment, so shell-prefixed vars
// (`SET_NEW_ENV=1 aiagentmemory run foo`) reach it unchanged; only
// CLAUDE_CONFIG_DIR is layered on top.
func execAgent(plan launchPlan, agentArgs []string) error {
	bin, err := resolveAgentBin(plan.bin)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("cannot find the agent CLI %q on PATH: %w", bin, err)
	}

	env := os.Environ()
	if plan.configDir != "" {
		// CLAUDE_CONFIG_DIR is how Claude Code relocates its entire config
		// (settings, commands, MCP servers); setting it is what makes a sandbox
		// an isolated Claude environment.
		env = append(env, "CLAUDE_CONFIG_DIR="+plan.configDir)
	}

	// syscall.Exec never returns on success; on failure it returns the errno.
	argv := append([]string{bin}, agentArgs...)
	return syscall.Exec(path, argv, env)
}

// resolveAgentBin maps a planned agent name to the binary to exec. An empty name
// — and "claude" itself — goes through resolveClaudeBin so --claude-bin /
// AIAGENTMEMORY_CLAUDE_BIN keeps steering which Claude build runs; any other
// allowlisted agent is exec'd under its own name.
func resolveAgentBin(name string) (string, error) {
	if name == "" || name == "claude" {
		return resolveClaudeBin("")
	}
	return name, nil
}

// resolveClaudeBin decides which Claude CLI to drive. Precedence: an explicit
// override (the --claude-bin flag or AIAGENTMEMORY_CLAUDE_BIN), then plain
// `claude` on PATH. It returns the command name (not the resolved path) so
// callers can exec.LookPath it themselves.
func resolveClaudeBin(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("AIAGENTMEMORY_CLAUDE_BIN"); env != "" {
		return env, nil
	}
	if _, err := exec.LookPath("claude"); err == nil {
		return "claude", nil
	}
	return "", errors.New("no Claude CLI found on PATH (looked for claude); set --claude-bin or AIAGENTMEMORY_CLAUDE_BIN")
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
