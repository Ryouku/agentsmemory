package datasetdoc

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestDocumentedMappingKeysAreTheParsedOnes: the README's TOML example is the
// only place an operator learns this format, so it is a promise rather than an
// illustration. A key shown there that nothing parses is a setting someone will
// write and watch do nothing; a key the parser reads that appears nowhere is a
// capability nobody can find.
//
// This repository already runs the same check on env vars and on the tool count,
// for the same reason: prose that must stay true gets a command whose exit code
// says so.
//
// The comparison is BIDIRECTIONAL on purpose. A one-way check ("every documented
// key parses") stays green forever as the struct grows, which is the silent
// forward failure a whitelist has.
func TestDocumentedMappingKeysAreTheParsedOnes(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	block := mappingExample(t, string(readme))

	documented := map[string]bool{}
	// Both forms a TOML key can take in the example: a `key = value` line, and a
	// `[[table]]` header — which is how a repeated section like the dataset list
	// is written, and is a parsed key exactly like the scalars beneath it.
	keyRe := regexp.MustCompile(`(?m)^\s*(?:\[\[?([a-z_]+)\]?\]|([a-z_]+)\s*=)`)
	for _, m := range keyRe.FindAllStringSubmatch(block, -1) {
		if m[1] != "" {
			documented[m[1]] = true
		}
		if m[2] != "" {
			documented[m[2]] = true
		}
	}
	if len(documented) == 0 {
		t.Fatal("no keys found in the documented example — this check would then pass against " +
			"a README that documents nothing")
	}

	parsed := map[string]bool{}
	for _, typ := range []reflect.Type{reflect.TypeOf(Config{}), reflect.TypeOf(Dataset{})} {
		for i := range typ.NumField() {
			if tag := typ.Field(i).Tag.Get("toml"); tag != "" {
				parsed[tag] = true
			}
		}
	}

	for k := range documented {
		if !parsed[k] {
			t.Errorf("README documents %q, which nothing parses — an operator would write it and "+
				"watch it do nothing", k)
		}
	}
	for k := range parsed {
		if !documented[k] {
			t.Errorf("the mapping file accepts %q and the README never shows it — a capability "+
				"nobody can find is one nobody uses", k)
		}
	}
}

// mappingExample returns the TOML block under the import section. It locates the
// section by heading rather than by offset so an edit elsewhere in a 1,400-line
// README cannot silently point this check at a different code block.
func mappingExample(t *testing.T, readme string) string {
	t.Helper()
	const heading = "## Teaching the palace about a project's data"
	start := strings.Index(readme, heading)
	if start < 0 {
		t.Fatalf("README no longer contains %q — the format is undocumented", heading)
	}
	section := readme[start:]
	if end := strings.Index(section[len(heading):], "\n## "); end >= 0 {
		section = section[:len(heading)+end]
	}
	open := strings.Index(section, "```toml")
	if open < 0 {
		t.Fatal("the import section shows no ```toml example of the mapping file")
	}
	rest := section[open+len("```toml"):]
	close := strings.Index(rest, "```")
	if close < 0 {
		t.Fatal("unterminated toml block in the import section")
	}
	return rest[:close]
}

// repoRoot walks up to the module root, so the check reads the real README
// rather than a copy.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
