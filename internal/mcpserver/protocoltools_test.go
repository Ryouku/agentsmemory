package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// toolRef matches a memory-tool name as the protocol files write it: bare
// (`am_search`) or namespaced by a harness (`mcp__agentsmemory__am_search`).
var toolRef = regexp.MustCompile(`(?:mcp__[a-z0-9_]+__)?\b((?:am|mempalace)_[a-z_]+)\b`)

// protocolDocs are the files that tell an agent how to reach this server. They
// are shipped to every install, so a tool name that is wrong here is wrong on
// every machine that runs the kit.
var protocolDocs = []string{
	"AGENTS.md",
	"CLAUDE.md",
	filepath.Join("clients", "claude-code", "bootstrap.md"),
	filepath.Join("clients", "claude-code", "README.md"),
	filepath.Join("clients", "claude-code", "CLAUDE.md"),
	filepath.Join("clients", "claude-code", "commands", "am.md"),
	filepath.Join("clients", "claude-code", "commands", "M.md"),
	filepath.Join("clients", "claude-code", "commands", "load-skill.md"),
}

// TestProtocolDocsNameToolsThatExist fails when a shipped protocol file tells an
// agent to call a tool this server does not register.
//
// This is the repository's own defect class aimed at its instructions rather than
// its code: documentation that promises a capability nothing provides. It is
// worse here than in a comment, because these files are the ONLY thing telling a
// new session how to reach the palace — an agent that follows them and finds
// nothing has no second route.
//
// It is not hypothetical. `commands/M.md` carried 13 references to a
// `mempalace_*` server that this project does not run, while `am.md` and
// `AGENTS.md` beside it used `am_*` correctly; a maintainer hit it live, running
// `/M` in this repo and watching `mempalace_status` resolve to nothing.
//
// The check reads the real registry rather than a list kept beside it, because a
// list beside the truth is a thing somebody has to remember.
func TestProtocolDocsNameToolsThatExist(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range fullCatalog(true) {
		registered[name] = true
	}
	for _, name := range fullCatalog(false) {
		registered[name] = true
	}
	if len(registered) == 0 {
		t.Fatal("the catalog is empty — this check has stopped checking anything")
	}

	root := filepath.Join("..", "..")
	seen := map[string][]string{}
	for _, rel := range protocolDocs {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if os.IsNotExist(err) {
			continue // an optional kit file; the ones that exist are what matter
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, m := range toolRef.FindAllStringSubmatch(string(raw), -1) {
			name := m[1]
			if registered[name] {
				continue
			}
			seen[name] = append(seen[name], rel)
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		files := seen[n]
		sort.Strings(files)
		t.Errorf("the protocol files name %q, which this server does not register — an agent "+
			"following them calls a tool that is not there (%s)", n, strings.Join(dedupe(files), ", "))
	}
}

// TestRuntimeProtocolDoesNotRequireEidos keeps the agentsmemory workflow
// usable when no third-party spec or planning plugin is installed. The runtime
// protocol must discover the repository's actual intent sources and use the
// harness's native plan tracker instead of naming an optional plugin.
func TestRuntimeProtocolDoesNotRequireEidos(t *testing.T) {
	root := filepath.Join("..", "..")
	docs := append(append([]string(nil), protocolDocs...), "example.md")
	for _, rel := range docs {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if os.IsNotExist(err) {
			continue // protocolDocs includes optional harness-specific files
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, dependency := range []string{"eidos:spec", "eidos:plan"} {
			if strings.Contains(string(raw), dependency) {
				t.Errorf("%s requires optional third-party skill %q", rel, dependency)
			}
		}
	}
}

func dedupe(in []string) []string {
	out, seen := in[:0], map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestProtocolTextTeachesTheInboxConvention.
//
// The handoff convention lived only in the locally-installed bootstrap, and its
// wing placeholder read `wing_<target>`. Two sessions substituted the direction
// of travel for the project name and filed six drawers into wings nothing
// resolves to. The convention has to state how the wing is named, in a file that
// ships, or the next session repeats it.
//
// It checks bootstrap.md specifically rather than "any protocol doc": bootstrap
// is the always-on baseline that applies whether or not a command is typed, and
// a rule that reached only the slash-command files would still be missing from
// every session that does not type one.
func TestProtocolTextTeachesTheInboxConvention(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "clients", "claude-code", "bootstrap.md"))
	if err != nil {
		t.Fatalf("read bootstrap.md: %v", err)
	}
	text := string(body)

	for what, want := range map[string]string{
		"the room a handoff is filed into":            `room ` + "`inbox`",
		"that the wing is named for the project":      "named for the PROJECT",
		"that it is NOT named for where it is going":  "never for the direction of travel",
		"the argument the server's refusal names":     "confirm_new_wing",
		"a concrete wing name rather than a template": "wing_acme-billing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("bootstrap.md does not state %s (looked for %q). A placeholder without the "+
				"naming rule is what produced two undeliverable handoffs", what, want)
		}
	}

	// The template that was mis-substituted must be gone, not merely explained
	// beside its replacement — a reader who copies the first thing that looks
	// like a call copies the template.
	if strings.Contains(text, `wing: "wing_<target>"`) {
		t.Error(`bootstrap.md still shows wing: "wing_<target>" as the call to copy — that is the ` +
			"exact string both failing sessions substituted into")
	}
}
