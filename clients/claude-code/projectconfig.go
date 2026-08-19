package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// projectConfigFile is the per-project launch config, meant to be COMMITTED.
	// It records the team-wide part of a launch — which agent and which agent
	// flags — and deliberately not the sandbox name, since teammates name their
	// sandboxes differently and a committed name would launch the wrong config
	// (or nothing at all) on every machine but the author's.
	projectConfigFile = ".aiagentmemory"

	// projectLocalFile is the personal, git-ignored companion to
	// projectConfigFile. It uses the same format and overrides it key by key,
	// mirroring how Claude Code layers settings.local.json over settings.json.
	projectLocalFile = ".aiagentmemory.local"

	// agentRegistryFile is the machine-local map of project dir → sandbox, kept
	// beside the sandboxes it names (~/.sandboxes/agents). It is the preferred
	// home for your sandbox choice: it needs no .gitignore entry and one file
	// answers "which sandbox does this project use?" for every project at once.
	agentRegistryFile = "agents"

	// sandboxEnvVar overrides the recorded sandbox for a single launch, for
	// one-off work in another sandbox without editing any file.
	sandboxEnvVar = "AIAGENTMEMORY_SANDBOX"

	// wingEnvVar carries the project's memory wing to the agent. A palace holds
	// every project you work on and wings are the per-project partition, but the
	// server speaks HTTP MCP and cannot see the client's working directory — so
	// the wing has to travel from here. The memory protocol reads this variable
	// first and falls back to the git remote when it is unset, which is why an
	// unconfigured project still gets a sane wing instead of a shared one.
	wingEnvVar = "AGENTSMEMORY_WING"
)

// projectConfig is the launch intent recorded for a project directory: which
// sandbox to pin, which agent CLI to drive, and the arguments to hand it. A zero
// value means "nothing recorded", and every field is independently optional so a
// shared file and a personal one can each supply only the part they own.
type projectConfig struct {
	sandbox string
	agent   string
	args    []string
	wing    string // memory wing this project's drawers and diary entries belong to
}

// parseProjectConfig reads the KEY=VALUE form used by both .aiagentmemory files.
// It is deliberately the same shape as tokenEnv's parser — blank lines and #
// comments skipped, a line without '=' ignored — because these files are ours,
// hand-edited, and carry no shell semantics. Unknown keys are ignored rather than
// rejected so a newer client can add keys without breaking an older one.
func parseProjectConfig(raw []byte) projectConfig {
	var cfg projectConfig
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "sandbox":
			cfg.sandbox = value
		case "agent":
			cfg.agent = value
		case "args":
			cfg.args = splitArgs(value)
		case "wing":
			cfg.wing = value
		}
	}
	return cfg
}

// readProjectConfig loads the shared and personal config from a single directory,
// reporting whether either file was there. A missing file is not an error — an
// absent .aiagentmemory just means nothing was recorded here, and `load` reports
// that itself with a far better message than a stat error would.
func readProjectConfig(dir string) (shared, local projectConfig, found bool) {
	read := func(name string) (projectConfig, bool) {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return projectConfig{}, false
		}
		return parseProjectConfig(raw), true
	}
	shared, okShared := read(projectConfigFile)
	local, okLocal := read(projectLocalFile)
	return shared, local, okShared || okLocal
}

// findProjectConfig walks up from startDir and returns the config from the
// nearest directory holding either project file, plus that directory ("" if
// none). Walking matches how the sandbox registry resolves, so `load` behaves the
// same from a repository root and from a directory deep inside it — otherwise a
// subdirectory launch would find the right sandbox by ancestor walk and silently
// drop the recorded agent flags.
//
// The nearest directory wins wholesale: both files are read from it, so a shared
// file and the personal file overriding it are never taken from different levels.
func findProjectConfig(startDir string) (shared, local projectConfig, dir string) {
	for d := startDir; ; {
		if s, l, found := readProjectConfig(d); found {
			return s, l, d
		}
		parent := filepath.Dir(d)
		if parent == d { // reached the filesystem root
			return projectConfig{}, projectConfig{}, ""
		}
		d = parent
	}
}

// splitArgs splits a recorded `args=` line into individual arguments. The flat
// KEY=VALUE format cannot express an argument list natively, so quoting is
// honoured here: without it an argument with a space in it — say
// --append-system-prompt "be terse" — would silently reach the agent as two
// arguments. Single and double quotes group, and a backslash escapes the next
// byte outside quotes; nothing else is interpreted, since this is a config file
// and not a shell.
func splitArgs(line string) []string {
	var (
		args  []string
		cur   strings.Builder
		quote byte
		open  bool // an argument has begun, so "" yields an empty argument
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			// Inside double quotes a backslash escapes the next byte, which is
			// how formatArgs writes an argument that itself contains a quote or
			// a backslash. Single quotes stay fully literal, as in a shell.
			if quote == '"' && c == '\\' && i+1 < len(line) {
				i++
				cur.WriteByte(line[i])
				continue
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote, open = c, true
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			open = true
		case c == ' ' || c == '\t':
			if cur.Len() > 0 || open {
				args = append(args, cur.String())
				cur.Reset()
				open = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 || open {
		args = append(args, cur.String())
	}
	return args
}

// renderProjectConfig produces the committed .aiagentmemory body. The header
// explains the one thing a reader will otherwise get wrong — that the sandbox
// name is absent on purpose — because this file lands in code review on every
// teammate's clone.
func renderProjectConfig(cfg projectConfig) []byte {
	var b strings.Builder
	b.WriteString("# aiagentmemory project config — safe to commit.\n")
	b.WriteString("# Launch this project with: aiagentmemory load\n")
	b.WriteString("#\n")
	b.WriteString("# wing= is the memory wing this project's decisions are filed under, so one\n")
	b.WriteString("# palace can hold every project without them bleeding into each other. It IS\n")
	b.WriteString("# safe to commit: teammates share a wing, that is the point.\n")
	b.WriteString("#\n")
	b.WriteString("# The sandbox name is NOT recorded here: teammates name their sandboxes\n")
	b.WriteString("# differently. Yours lives in ~/.sandboxes/" + agentRegistryFile + " (machine-local),\n")
	b.WriteString("# or in " + projectLocalFile + " if you prefer to keep it in the project.\n")
	if cfg.agent != "" {
		fmt.Fprintf(&b, "agent=%s\n", cfg.agent)
	}
	if len(cfg.args) > 0 {
		fmt.Fprintf(&b, "args=%s\n", formatArgs(cfg.args))
	}
	if cfg.wing != "" {
		fmt.Fprintf(&b, "wing=%s\n", cfg.wing)
	}
	if cfg.sandbox != "" {
		fmt.Fprintf(&b, "sandbox=%s\n", cfg.sandbox)
	}
	return []byte(b.String())
}

// formatArgs renders an argument list back to a single `args=` line, quoting any
// argument that splitArgs would otherwise split or mangle, so a written file
// round-trips through the parser unchanged.
func formatArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t'\"\\") {
			out[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

// launchInputs are the layers `load` resolves a launch from, gathered by the
// caller so the decision itself stays pure and filesystem-free — the same reason
// planRun takes sandboxExists rather than stat'ing.
type launchInputs struct {
	flagSandbox string        // --sandbox on the load command
	flagAgent   string        // --agent on the load command
	envSandbox  string        // $AIAGENTMEMORY_SANDBOX
	registry    string        // this project's entry in ~/.sandboxes/agents
	local       projectConfig // .aiagentmemory.local (personal, git-ignored)
	shared      projectConfig // .aiagentmemory (committed)
	extraArgs   []string      // arguments after `--` on the load command
	envWing     string        // $AGENTSMEMORY_WING, when the shell already pinned one
}

// resolvedLaunch is what `load` decided, including where the sandbox choice came
// from. origin is carried purely for the human: with five layers in play, a
// surprising launch is only debuggable if the tool says which one won.
type resolvedLaunch struct {
	sandbox string
	agent   string
	args    []string
	origin  string
	wing    string // memory wing to export as $AGENTSMEMORY_WING ("" = let the protocol derive it)
}

// resolveLaunch applies the launch precedence, most specific first: an explicit
// --sandbox, then $AIAGENTMEMORY_SANDBOX, then this machine's registry entry,
// then the personal project file, then the committed one. The two overrides sit
// above the files so a one-off launch elsewhere needs no edit, and the registry
// sits above the project files so your machine always wins over whatever a
// teammate committed.
//
// A launch with no sandbox anywhere is an error rather than a quiet fall back to
// the global config: `load` exists to pin a project to an isolated sandbox, so
// silently running unpinned would defeat the command while looking like success.
func resolveLaunch(in launchInputs) (resolvedLaunch, error) {
	sandbox, origin := firstSandbox(in)
	if sandbox == "" {
		return resolvedLaunch{}, fmt.Errorf(
			"no sandbox configured for this project — run `aiagentmemory init --sandbox <name>` here, "+
				"or set %s for a one-off launch", sandboxEnvVar)
	}
	if err := validSandboxName(sandbox); err != nil {
		return resolvedLaunch{}, fmt.Errorf("%s: %w", origin, err)
	}

	// Agent and args resolve down the same ladder, minus the env var: each is a
	// whole-value choice, so a personal file overrides the committed args list
	// rather than merging into it — a half-merged flag list is nobody's intent.
	agent := firstNonEmpty(in.flagAgent, in.local.agent, in.shared.agent)
	args := in.local.args
	if len(args) == 0 {
		args = in.shared.args
	}
	// The wing follows the same ladder as the agent, with the shell's variable on
	// top: a one-off launch in another project's wing needs no file edit. An
	// unresolved wing stays empty rather than being invented here — the memory
	// protocol derives one from the git remote, which is a better guess than
	// anything this function knows.
	wing := firstNonEmpty(in.envWing, in.local.wing, in.shared.wing)

	return resolvedLaunch{
		sandbox: sandbox,
		agent:   agent,
		wing:    wing,
		// Extras land last so a flag typed at the command line beats the same
		// flag recorded in the file, which is how every agent CLI we drive
		// resolves a repeated flag.
		args:   append(append([]string{}, args...), in.extraArgs...),
		origin: origin,
	}, nil
}

// firstSandbox returns the winning sandbox name and a human-readable label for
// the layer it came from.
func firstSandbox(in launchInputs) (name, origin string) {
	for _, layer := range []struct{ name, origin string }{
		{in.flagSandbox, "--sandbox"},
		{in.envSandbox, "$" + sandboxEnvVar},
		{in.registry, "~/.sandboxes/" + agentRegistryFile},
		{in.local.sandbox, projectLocalFile},
		{in.shared.sandbox, projectConfigFile},
	} {
		if layer.name != "" {
			return layer.name, layer.origin
		}
	}
	return "", ""
}

// firstNonEmpty returns the first non-empty value, or "" if there is none.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// agentRegistryPath is the machine-local project→sandbox map, kept in the
// sandbox root so it sits beside the directories it points at.
func agentRegistryPath() string { return filepath.Join(sandboxRoot(), agentRegistryFile) }

// lookupAgentRegistry returns the sandbox recorded for dir, or "" if none is.
//
// The lookup walks up the directory tree and the NEAREST entry wins, so one line
// for a parent directory covers every repository beneath it — `~/code=work`
// pins the whole tree — while a line for a single repo still overrides that
// default for just that repo. Matching is per path component, so a registered
// /home/m/we cannot capture /home/m/website.
//
// Entries are split on the LAST '=' rather than the first: a project path may
// legally contain '=' on Unix, while a sandbox name may not (validSandboxName
// permits only letters, digits, dash and underscore), so the final separator is
// the only unambiguous one.
func lookupAgentRegistry(raw []byte, dir string) string {
	entries := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, "=")
		if i < 0 {
			continue
		}
		// First entry for a path wins, matching the hand-edited-file intuition
		// that the line you read first is the one in force.
		if path := strings.TrimSpace(line[:i]); entries[path] == "" {
			entries[path] = strings.TrimSpace(line[i+1:])
		}
	}
	for d := dir; ; {
		if sandbox := entries[d]; sandbox != "" {
			return sandbox
		}
		parent := filepath.Dir(d)
		if parent == d { // reached the filesystem root
			return ""
		}
		d = parent
	}
}

// upsertAgentRegistry returns raw with dir mapped to sandbox, replacing an
// existing entry in place and appending a new one otherwise. It rewrites the
// whole file rather than appending blindly so re-running `init` in the same
// project updates the mapping instead of stacking dead duplicates that the
// first-match lookup would then shadow.
//
// It is pure — the caller owns reading and writing the file — so the merge is
// testable without a home directory.
func upsertAgentRegistry(raw []byte, dir, sandbox string) []byte {
	entry := dir + "=" + sandbox
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if j := strings.LastIndex(trimmed, "="); j >= 0 && strings.TrimSpace(trimmed[:j]) == dir {
			lines[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
			// A new (or empty) registry starts with the header, not a blank line.
			lines = []string{
				"# aiagentmemory — which sandbox each project launches with.",
				"# One <project dir>=<sandbox> per line. Machine-local; never committed.",
			}
		}
		lines = append(lines, entry)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// writeAgentRegistry records dir→sandbox in the machine-local registry, creating
// the sandbox root if this is the first entry. It is written 0600 like the other
// files we own under the sandbox root: the mapping exposes a developer's project
// layout.
func writeAgentRegistry(dir, sandbox string) error {
	path := agentRegistryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.WriteFile(path, upsertAgentRegistry(raw, dir, sandbox), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
