package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Agent identifiers accepted by `install --agent` and `run --agent`.
const (
	agentClaude = "claude"
	agentCodex  = "codex"
	agentBoth   = "both"
)

// agentKit describes the parts of an install that differ between the agent CLIs
// we support. Everything structural is the same on both — a config dir holding
// slash-command markdown, an agent memory file, a JSON hook registration, and an
// MCP server list driven through the agent's own CLI — so the differences are
// plain data plus the two registration steps that genuinely diverge (see
// Installer.registerAgentsMemoryMCP and Installer.installRecommended).
//
// Values are verified against Claude Code and codex-cli 0.137: codex relocates
// its whole config with CODEX_HOME exactly as Claude does with CLAUDE_CONFIG_DIR,
// reads top-level markdown in <CODEX_HOME>/prompts as slash commands (same
// `description:`/`argument-hint:` front matter and `$ARGUMENTS` expansion as
// Claude commands), and loads lifecycle hooks from <CODEX_HOME>/hooks.json in the
// same JSON shape Claude uses in settings.json.
type agentKit struct {
	name        string // agent identifier: claude | codex
	bin         string // default CLI binary name to drive
	configEnv   string // env var that relocates the agent's config dir
	globalDir   string // basename of the global config dir under $HOME
	commandsDir string // subdir under the config dir that holds slash commands
	memoryFile  string // agent memory file our managed block merges into
	hooksFile   string // JSON file holding the Stop-hook registration

	// supportsImport reports whether the memory file can pull in a sibling file
	// by reference. Claude Code resolves `@file.md` imports, so it gets a
	// one-line import of the protocol; codex has no import mechanism in
	// AGENTS.md, so there the protocol is inlined into the managed block.
	supportsImport bool

	// commandHint shows the user how the installed commands are invoked. Codex
	// namespaces prompt files under `/prompts:`, Claude does not.
	commandHint string
}

// claudeKit is the Claude Code layout: ~/.claude, commands/, CLAUDE.md + @import,
// hooks registered in settings.json.
var claudeKit = agentKit{
	name:           agentClaude,
	bin:            "claude",
	configEnv:      "CLAUDE_CONFIG_DIR",
	globalDir:      ".claude",
	commandsDir:    "commands",
	memoryFile:     "CLAUDE.md",
	hooksFile:      "settings.json",
	supportsImport: true,
	commandHint:    "/M",
}

// codexKit is the codex-cli layout: ~/.codex, prompts/, AGENTS.md with the
// protocol inlined, hooks registered in hooks.json.
var codexKit = agentKit{
	name:        agentCodex,
	bin:         "codex",
	configEnv:   "CODEX_HOME",
	globalDir:   ".codex",
	commandsDir: "prompts",
	memoryFile:  "AGENTS.md",
	hooksFile:   "hooks.json",
	// AGENTS.md has no import directive — codex reads the file itself, so the
	// protocol has to live in the managed block rather than beside it.
	supportsImport: false,
	commandHint:    "/prompts:M",
}

// resolveAgentKits maps the --agent value to the kits to install. "both" returns
// Claude first so a mixed install's output reads in the same order as the docs.
// An unknown name is an error rather than a silent fallback: installing into the
// wrong agent's config dir would be invisible until the user wondered why their
// commands never showed up.
func resolveAgentKits(name string) ([]agentKit, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", agentClaude:
		return []agentKit{claudeKit}, nil
	case agentCodex:
		return []agentKit{codexKit}, nil
	case agentBoth:
		return []agentKit{claudeKit, codexKit}, nil
	default:
		return nil, fmt.Errorf("unknown --agent %q: use claude, codex or both", name)
	}
}

// resolveAgentKit maps a single agent name to its kit, rejecting "both" — used by
// `run`, which launches exactly one agent.
func resolveAgentKit(name string) (agentKit, error) {
	kits, err := resolveAgentKits(name)
	if err != nil {
		return agentKit{}, err
	}
	if len(kits) != 1 {
		return agentKit{}, fmt.Errorf("--agent %q selects more than one agent; name just one", name)
	}
	return kits[0], nil
}

// globalConfigDir is the agent's default config dir under home.
func (k agentKit) globalConfigDir(home string) string {
	return filepath.Join(home, k.globalDir)
}
