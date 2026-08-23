package main

import "testing"

// TestCursorKitResolves pins that --agent cursor reaches a kit at all.
//
// resolveAgentKits is the single function every --agent value goes through, so a
// kit that compiles but is not named here is unreachable from the command line —
// the shape this repository ships often enough to have a rule about it.
func TestCursorKitResolves(t *testing.T) {
	kits, err := resolveAgentKits(agentCursor)
	if err != nil {
		t.Fatalf("--agent cursor: %v", err)
	}
	if len(kits) != 1 || kits[0].name != agentCursor {
		t.Fatalf("--agent cursor resolved to %v, want exactly the cursor kit", names(kits))
	}

	// `all` means all. A kit missing from it is installed only by people who
	// already know it exists.
	all, err := resolveAgentKits(agentAll)
	if err != nil {
		t.Fatalf("--agent all: %v", err)
	}
	if !contains(names(all), agentCursor) {
		t.Errorf("--agent all resolved to %v, which omits cursor", names(all))
	}

	// `both` keeps meaning the pair it shipped with, exactly as it did when pi
	// joined: an existing script must not silently start writing to ~/.cursor.
	both, err := resolveAgentKits(agentBoth)
	if err != nil {
		t.Fatalf("--agent both: %v", err)
	}
	if contains(names(both), agentCursor) {
		t.Errorf("--agent both resolved to %v; both is claude+codex and must not grow", names(both))
	}
}

// TestCursorKitDeclaresWhatCursorHasAndWhatItLacks pins the shape against what was
// measured, so a later "tidy-up" that fills in a plausible value fails.
//
// Every empty field here is a MEASURED absence on the reference machine, not an
// omission: cursor-agent reads no config-dir variable, there is no
// ~/.cursor/commands, there is no agent memory file, and ~/.cursor/hooks' shape
// was never established.
func TestCursorKitDeclaresWhatCursorHasAndWhatItLacks(t *testing.T) {
	k := cursorKit
	for _, f := range []struct{ name, got string }{
		{"configEnv", k.configEnv},
		{"commandsDir", k.commandsDir},
		{"memoryFile", k.memoryFile},
		{"hooksFile", k.hooksFile},
	} {
		if f.got != "" {
			t.Errorf("cursorKit.%s = %q; Cursor has none of these and a plausible value here "+
				"writes files nothing reads", f.name, f.got)
		}
	}
	if k.globalDir != ".cursor" {
		t.Errorf("cursorKit.globalDir = %q, want .cursor", k.globalDir)
	}
	if k.agentsDir == "" || k.agentAssetExt != ".md" {
		t.Errorf("cursorKit agent definitions = %q/%q; Cursor reads the same markdown dialect "+
			"Claude does", k.agentsDir, k.agentAssetExt)
	}
	if k.rulesFile == "" {
		t.Error("cursorKit.rulesFile is empty, so the protocol reaches Cursor by no route at all")
	}
}

func names(kits []agentKit) []string {
	out := make([]string, len(kits))
	for i, k := range kits {
		out[i] = k.name
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
