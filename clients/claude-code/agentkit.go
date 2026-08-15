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
	agentPi     = "pi"
	agentBoth   = "both"
	agentAll    = "all"
)

// agentKit describes the parts of an install that differ between the agent CLIs
// we support. Everything structural is the same on both — a config dir holding
// slash-command markdown, an agent memory file, a JSON hook registration, and an
// MCP server list driven through the agent's own CLI — so the differences are
// plain data plus the two registration steps that genuinely diverge (see
// Installer.registerAgentsMemoryMCP and Installer.installRecommended).
//
// Values are verified against Claude Code, codex-cli 0.137 and pi 0.84.2: each
// relocates its whole config with one env var (CLAUDE_CONFIG_DIR / CODEX_HOME /
// PI_CODING_AGENT_DIR), each reads top-level markdown in a commands dir as slash
// commands with the same `description:`/`argument-hint:` front matter and
// `$ARGUMENTS` expansion, and each loads an agent memory file from that dir. Only
// Claude and codex take lifecycle hooks from JSON; pi retired hooks in favour of
// extensions, so its kit carries no hooksFile.
type agentKit struct {
	name        string // agent identifier: claude | codex | pi
	bin         string // default CLI binary name to drive
	configEnv   string // env var that relocates the agent's config dir
	globalDir   string // slash-separated global config dir, relative to $HOME
	commandsDir string // subdir under the config dir that holds slash commands
	memoryFile  string // agent memory file our managed block merges into

	// hooksFile is the JSON file holding the Stop-hook registration, empty for an
	// agent with no hook system. pi is that case: it renamed hooks/ to extensions,
	// so its end-of-turn nudge lives in the extension we install instead.
	hooksFile string

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

// piKit is the pi-coding-agent layout: ~/.pi/agent, prompts/, AGENTS.md with the
// protocol inlined, and no hooks file at all.
//
// pi is the odd one out in two ways, both verified against pi 0.84.2:
//
//   - Its config dir is two levels deep (~/.pi/agent), so globalDir carries a
//     separator where the others carry a bare basename.
//   - It ships no MCP client — "intentionally does not include built-in MCP"
//     (docs/usage.md) — and no hook system. Both gaps are filled by the extension
//     Installer.registerPiMCP writes into <config dir>/extensions.
var piKit = agentKit{
	name:        agentPi,
	bin:         "pi",
	configEnv:   "PI_CODING_AGENT_DIR",
	globalDir:   ".pi/agent",
	commandsDir: "prompts",
	memoryFile:  "AGENTS.md",
	// pi loads AGENTS.md from its agent dir verbatim; like codex it has no import
	// directive, so the protocol is inlined into the managed block.
	supportsImport: false,
	// pi prompt templates are invoked by bare name — no `/prompts:` namespace.
	commandHint: "/M",
}

// resolveAgentKits maps the --agent value to the kits to install. Multi-agent
// values return Claude first so a mixed install's output reads in the same order
// as the docs. An unknown name is an error rather than a silent fallback:
// installing into the wrong agent's config dir would be invisible until the user
// wondered why their commands never showed up.
//
// "both" keeps meaning Claude + codex, the pair it shipped with; pi joins under
// the new "all" instead, so an existing `--agent both` script does not silently
// start installing into a third agent.
func resolveAgentKits(name string) ([]agentKit, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", agentClaude:
		return []agentKit{claudeKit}, nil
	case agentCodex:
		return []agentKit{codexKit}, nil
	case agentPi:
		return []agentKit{piKit}, nil
	case agentBoth:
		return []agentKit{claudeKit, codexKit}, nil
	case agentAll:
		return []agentKit{claudeKit, codexKit, piKit}, nil
	default:
		return nil, fmt.Errorf("unknown --agent %q: use claude, codex, pi, both or all", name)
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

// globalConfigDir is the agent's default config dir under home. globalDir is
// written slash-separated so a nested default like pi's ".pi/agent" stays
// readable in the kit; FromSlash turns it into the host's separator.
func (k agentKit) globalConfigDir(home string) string {
	return filepath.Join(home, filepath.FromSlash(k.globalDir))
}
