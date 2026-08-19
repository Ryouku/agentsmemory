package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envVarPattern matches the shape of an environment variable name as this
// project writes them: SCREAMING_SNAKE, at least two segments, so ordinary
// capitalised prose ("HTTP", "NOTE") does not trip the check.
var envVarPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)\b`)

// commentPromise matches a variable shown WITH a value in a comment, which is
// what makes it an offer rather than a mention.
var commentPromise = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)=\S`)

// TestDocumentedEnvVarsAreRead fails when a variable this project documents to
// operators is read by nothing in the program.
//
// It exists because that is not a hypothetical. `EMBED_BACKEND=tei` was named in
// teiembed's package comment as the way to select a complete, unit-tested,
// live-tested embedding backend — and no code read it, so the sentence described
// a feature that did not exist. The same shape shipped three more times in one
// week: an eval arm that was never registered, an IDF coverage function with no
// branch in Search, a config field nothing consumed. In every case the capability
// was finished and the line making it selectable was missing, and every test
// passed because they exercised the component rather than the selection.
//
// Prose cannot be trusted to stay true; this makes the documentation load-bearing.
// A variable is "documented" if it appears in the operator-facing env examples or
// in a Go comment, and "read" if the program mentions it in a cli.EnvVars
// declaration or an os.Getenv call.
func TestDocumentedEnvVarsAreRead(t *testing.T) {
	root := repoRoot(t)

	documented := map[string][]string{} // var -> where it was promised
	for _, rel := range []string{".env.example", ".env.docker.example"} {
		for _, v := range envVarsIn(t, filepath.Join(root, rel)) {
			documented[v] = append(documented[v], rel)
		}
	}
	// Compose files are the strongest promise of all: an operator does not merely
	// read them, the stack RUNS with those values, so a variable set there and
	// read nowhere means the deployment is configured by a line that does
	// nothing. RERANK_TOP_K sat in docker-compose.full.yml claiming a rerank pool
	// of 20 that the server never read — the .env example happened to carry it
	// too, which is the only reason the first version of this check saw it.
	composeFiles, _ := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	for _, path := range composeFiles {
		rel, _ := filepath.Rel(root, path)
		for _, v := range composeEnvVarsIn(t, path) {
			documented[v] = append(documented[v], rel)
		}
	}
	// Go comments count too: teiembed's promise lived in a package comment, not
	// in an env file, which is precisely why nobody noticed.
	for _, path := range goFilesUnder(t, root) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") {
				continue
			}
			// A comment counts as a PROMISE only when it shows the variable
			// with a value — "selected with EMBED_BACKEND=tei" is an offer an
			// operator can act on, while "there is deliberately no
			// RERANK_MODEL" or "(frozen CLOSET_CHAR_LIMIT)" is prose ABOUT a
			// variable, including prose explaining why it does not exist.
			// Without this distinction the check flags its own documentation
			// and gets deleted for noise.
			for _, m := range commentPromise.FindAllStringSubmatch(trimmed, -1) {
				if isProjectEnvVar(m[1]) {
					rel, _ := filepath.Rel(root, path)
					documented[m[1]] = append(documented[m[1]], rel)
				}
			}
		}
	}

	read := readEnvVars(t, root)
	var unread []string
	for v, where := range documented {
		if read[v] || !isProjectEnvVar(v) {
			continue
		}
		unread = append(unread, v+" (promised in "+strings.Join(dedupe(where), ", ")+")")
	}
	sort.Strings(unread)
	for _, u := range unread {
		t.Errorf("%s — documented to operators but read by nothing: either wire it or stop promising it", u)
	}
}

// isProjectEnvVar filters the pattern down to variables this project actually
// owns. Without it the check would flag every SCREAMING_SNAKE token in a comment
// — SQL keywords, HTTP headers, acronyms — and a noisy gate gets deleted.
func isProjectEnvVar(v string) bool {
	for _, prefix := range []string{
		"AGENTSMEMORY_", "EMBED_", "OLLAMA_", "QDRANT_", "RERANK_", "BM25_",
		"CLOSET_", "EVAL_", "APP_", "SUPERADMIN_", "VECTOR_", "ABSTAIN_",
	} {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return v == "FUSION" || v == "DB_PATH" || v == "LISTEN_ADDR"
}

// envVarsIn reads the assignments and commented-out assignments in an env
// example file: both `KEY=value` and `# KEY=value` are promises to an operator.
func envVarsIn(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil // an absent example file is not this check's business
	}
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); envVarPattern.MatchString(key) && isProjectEnvVar(key) {
			out = append(out, key)
		}
	}
	return out
}

// composeEnvVarsIn reads the `KEY: value` entries of an `environment:` block.
// Compose also supports the `- KEY=value` list form; both are matched, because
// which one a file uses is a style choice and the promise is identical.
func composeEnvVarsIn(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	inEnv := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasSuffix(trimmed, "environment:") {
			inEnv = true
			continue
		}
		// Any non-indented key ends the block; so does a sibling key at the same
		// depth as `environment:` itself.
		if inEnv && trimmed != "" && !strings.HasPrefix(line, " ") {
			inEnv = false
		}
		if !inEnv || trimmed == "" {
			continue
		}
		entry := strings.TrimPrefix(trimmed, "- ")
		key, _, ok := strings.Cut(entry, ":")
		if !ok {
			key, _, ok = strings.Cut(entry, "=")
		}
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); envVarPattern.MatchString(key) && isProjectEnvVar(key) {
			out = append(out, key)
		}
	}
	return out
}

// readEnvVars collects every variable the program actually consults.
func readEnvVars(t *testing.T, root string) map[string]bool {
	t.Helper()
	read := map[string]bool{}
	consult := regexp.MustCompile(`(?:cli\.EnvVars\(|os\.Getenv\(|os\.LookupEnv\(|t\.Setenv\()([^)]*)`)
	for _, path := range goFilesUnder(t, root) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, m := range consult.FindAllStringSubmatch(string(src), -1) {
			for _, v := range envVarPattern.FindAllString(m[1], -1) {
				read[v] = true
			}
		}
	}
	return read
}

func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "testdata", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

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

func dedupe(in []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
