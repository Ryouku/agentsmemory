package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>". The
// release workflow sets it from the git tag; a plain `go build` leaves "dev".
var version = "dev"

const (
	// defaultMCPURL is the agentsmemory remote MCP endpoint the installer wires
	// up. It is a stateless Streamable-HTTP MCP server authed by a per-workspace
	// bearer token (see the README "Connect the MCP" section).
	defaultMCPURL = "https://aiagentmemory.dev/mcp"

	// mcpName and codebaseMemoryName are the server names registered with the
	// Claude CLI. A server name doubles as the tool prefix (mcp__<name>__<tool>),
	// which the /am and /M commands reference, so these must stay stable.
	mcpName            = "agentsmemory"
	codebaseMemoryName = "codebasememory"

	// codebaseMemoryInstall is the upstream one-liner that drops the
	// codebase-memory-mcp binary into ~/.local/bin. Run only with --recommended.
	codebaseMemoryInstall = "curl -fsSL https://raw.githubusercontent.com/DeusData/codebase-memory-mcp/main/install.sh | bash"

	// codebaseMemoryBin is where that upstream script installs its binary; we
	// register it with the Claude CLI as a stdio MCP server.
	codebaseMemoryBin = "~/.local/bin/codebase-memory-mcp"
)

// main builds the CLI and dispatches. Errors are printed to stderr with a
// non-zero exit so the curl|bash installer and shell callers can detect failure.
func main() {
	cmd := &cli.Command{
		Name:    "aiagentmemory",
		Usage:   "install the agentsmemory Claude Code kit and wrap Claude with per-project sandboxes",
		Version: version,
		Commands: []*cli.Command{
			installCommand(),
			updateCommand(),
			runCommand(),
			wrapCommand(),
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "aiagentmemory:", err)
		os.Exit(1)
	}
}

// installCommand builds the `install` subcommand. With no --sandbox it performs
// a global install into ~/.claude (wrap your existing Claude with our MCP); with
// --sandbox <name> it installs an isolated config under ~/.sandboxes/<name>.
func installCommand() *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "install the kit globally (~/.claude, ~/.codex) or into an isolated --sandbox",
		Description: "Global (default):   aiagentmemory install\n" +
			"Isolated sandbox:   aiagentmemory install --sandbox <name> [--recommended]\n" +
			"Codex instead:      aiagentmemory install --agent codex\n" +
			"Both agents:        aiagentmemory install --agent both\n\n" +
			"The default install wires up our slash commands, the Stop hook, and the\n" +
			"agentsmemory MCP. --recommended additionally installs the codebase-memory\n" +
			"MCP and (Claude only) the eidos and codex plugins.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "agent",
				Value: agentClaude,
				Usage: "agent CLI to install for: claude | codex | both",
			},
			&cli.BoolFlag{
				Name:  "global",
				Usage: "install into the agent's global config dir non-interactively (skips the mode prompt); mutually exclusive with --sandbox/--claude-dir",
			},
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "install into an isolated config at ~/.sandboxes/<name> instead of the global ~/.claude",
			},
			&cli.StringFlag{
				Name: "claude-dir",
				// config-dir is the name that reads right for codex; claude-dir stays
				// the primary so existing scripts and docs keep working.
				Aliases: []string{"config-dir"},
				Usage:   "override the target agent config dir (ignored when --sandbox is set)",
			},
			&cli.BoolFlag{
				Name:  "recommended",
				Usage: "also install the recommended extensions: codebase-memory MCP, eidos + codex plugins",
			},
			&cli.StringFlag{
				Name:    "token",
				Sources: cli.EnvVars("AGENTSMEMORY_TOKEN"),
				Usage:   "agentsmemory workspace API token for the remote MCP (prompted if omitted)",
			},
			&cli.StringFlag{
				Name:  "mcp-url",
				Value: defaultMCPURL,
				Usage: "agentsmemory remote MCP endpoint",
			},
			&cli.StringFlag{
				Name:  "scope",
				Value: "user",
				Usage: "Claude MCP/plugin scope: user | local | project",
			},
			&cli.StringFlag{
				Name:    "claude-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_CLAUDE_BIN"),
				Usage:   "Claude CLI binary to drive (default: claude)",
			},
			&cli.StringFlag{
				Name:    "codex-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_CODEX_BIN"),
				Usage:   "codex CLI binary to drive (default: codex)",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "non-interactive: never prompt (skip the token prompt if none supplied)",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print what would happen without writing files or running commands",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			// --agent both installs the same kit assets into each agent's config in
			// turn; one failing agent aborts rather than half-installing silently.
			kits, err := resolveAgentKits(c.String("agent"))
			if err != nil {
				return err
			}
			for _, kit := range kits {
				inst, err := newInstaller(kit, c, os.Stdout, os.Stdin)
				if err != nil {
					return err
				}
				if err := inst.run(); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// runCommand builds `run [--agent codex] <name> [agent args...]` — launch an agent
// against an isolated sandbox. SkipFlagParsing forwards every argument after the
// sandbox name to the agent untouched, so `run foo -p "hi"` reaches it as
// `-p "hi"`; that is why --agent is hand-parsed from the front of the argument
// list instead of being declared as a flag.
//
// <name> is a sandbox first; if no such sandbox exists it may name an agent CLI
// (see planRun), which makes `aiagentmemory run claude` launch Claude globally
// rather than erroring on a sandbox nobody created. Either way the launched
// agent inherits the caller's environment, so `SET_NEW_ENV=1 aiagentmemory run
// …` passes that variable straight through.
func runCommand() *cli.Command {
	return &cli.Command{
		Name:            "run",
		Usage:           "run an agent against a sandbox: aiagentmemory run [--agent codex] <name> [agent args...]",
		ArgsUsage:       "[--agent claude|codex] <name> [agent args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			kit, args, err := takeAgentFlag(c.Args().Slice())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return errors.New("run: missing sandbox name (usage: aiagentmemory run [--agent codex] <name> [agent args...])")
			}
			name := args[0]
			plan, err := planRun(kit, name, dirExists(sandboxDir(name)))
			if err != nil {
				return err
			}
			if plan.configDir == "" {
				// The fallback silently changes which config is in play, so say
				// so on stderr — stdout belongs to the agent we are about to become.
				fmt.Fprintf(os.Stderr, "aiagentmemory: no sandbox %q — launching %s with the global config\n", name, plan.bin)
			}
			return execAgent(kit, plan, args[1:])
		},
	}
}

// wrapCommand builds `wrap [--agent codex] [agent args...]` — launch the agent
// against its default global config (~/.claude, ~/.codex). It is the "global mode"
// counterpart to run: same passthrough, but no sandbox and no config-dir override.
func wrapCommand() *cli.Command {
	return &cli.Command{
		Name:            "wrap",
		Usage:           "run an agent against its global config: aiagentmemory wrap [--agent codex] [agent args...]",
		ArgsUsage:       "[--agent claude|codex] [agent args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			kit, args, err := takeAgentFlag(c.Args().Slice())
			if err != nil {
				return err
			}
			// Zero launchPlan → the kit's configured CLI with no config-dir
			// override, so the agent uses its own default config.
			return execAgent(kit, launchPlan{}, args)
		},
	}
}

// takeAgentFlag pulls a leading `--agent <name>` (or `--agent=<name>`) off args and
// resolves it to a kit, returning the remaining arguments. Only the leading
// position is claimed: everything after it belongs to the agent being launched, so
// `run foo --agent codex` passes those two words through untouched rather than
// re-steering the launch. Absent the flag, Claude stays the default.
func takeAgentFlag(args []string) (agentKit, []string, error) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "--agent") {
		return claudeKit, args, nil
	}
	if name, ok := strings.CutPrefix(args[0], "--agent="); ok {
		kit, err := resolveAgentKit(name)
		return kit, args[1:], err
	}
	if args[0] != "--agent" {
		return claudeKit, args, nil // e.g. --agentfoo: not ours, pass it through
	}
	if len(args) < 2 {
		return agentKit{}, nil, errors.New("--agent needs a value: claude or codex")
	}
	kit, err := resolveAgentKit(args[1])
	return kit, args[2:], err
}
