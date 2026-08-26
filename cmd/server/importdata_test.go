package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
)

// TestImportCommandIsRegistered: a producer nothing can select is this
// repository's characteristic defect, and it has shipped that shape more than
// once. The check reads the CLI's own command list rather than the source, so it
// fails for the reason a user would notice — `agentsmemory import` not existing.
//
// Proved by mutation: removing importCommand() from rootCommand's Commands turns
// this red, and restoring it turns it green.
func TestImportCommandIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
		if c.Name == "import" {
			return
		}
	}
	t.Errorf("the CLI registers %v and not \"import\" — the producer exists and cannot be run", names)
}

// TestImportWritesABundleTheImporterWouldAccept drives the real Action path end
// to end over a temporary project, because the unit tests prove the format and
// this proves the COMMAND reaches it: config resolution, relative dataset paths,
// and the file that lands on disk.
func TestImportWritesABundleTheImporterWouldAccept(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "data", "currencies.jsonl"),
		"{\"code\":\"EUR\",\"minor_units\":2}\n{\"code\":\"JPY\",\"minor_units\":0}\n")
	write(t, filepath.Join(dir, "import.toml"), `
wing = "wing_acme"

[[dataset]]
file  = "data/currencies.jsonl"
room  = "refdata"
title = "Currencies we actually support"
why   = "Only these reach the pricing UI; the rest are rejected at the boundary."
show_values = ["code"]
`)
	out := filepath.Join(dir, "bundle.ndjson")

	if err := runImport(context.Background(), importOptions{
		configPath: filepath.Join(dir, "import.toml"),
		out:        out,
		outSet:     true,
	}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		`"kind":"manifest"`,
		`"kind":"drawer"`,
		`"room":"refdata"`,
		`"source_file":"data/currencies.jsonl"`,
		"pricing UI",  // the human half
		"minor_units", // a measured field name
		"EUR, JPY",    // the value set of the field show_values names
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle is missing %q:\n%s", want, got)
		}
	}

	// show_values has to survive the whole CLI path — parsed from the committed
	// file, carried into the profiler, honoured in the emitted drawer. The unit
	// tests prove the filter; this proves the mapping key reaches it. Dropping the
	// field from Dataset leaves "EUR, JPY" missing above, and dropping the
	// per-field count leaves this line red.
	if !strings.Contains(got, "2 distinct value(s), not listed") {
		t.Errorf("minor_units was not named in show_values, so its values must be counted and "+
			"withheld — and the count must still be there, or the omission is silent:\n%s", got)
	}
}

// TestDatasetPathsResolveAgainstTheConfigNotTheWorkingDirectory: the mapping
// file is committed beside the data, so its own directory is the only stable
// base. Resolving against the cwd would make the same committed config work or
// fail depending on where someone ran it from.
func TestDatasetPathsResolveAgainstTheConfigNotTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "nested", "data", "x.jsonl"), "{\"a\":1}\n")
	write(t, filepath.Join(dir, "nested", "import.toml"), `
[[dataset]]
file  = "data/x.jsonl"
room  = "schema"
title = "X"
why   = "because"
`)
	// Run from the PARENT, so a cwd-relative resolution would look for
	// data/x.jsonl here and miss.
	t.Chdir(dir)

	if err := runImport(context.Background(), importOptions{
		configPath: filepath.Join("nested", "import.toml"),
		out:        filepath.Join(dir, "b.ndjson"),
		outSet:     true,
	}); err != nil {
		t.Fatalf("runImport resolved the dataset against the working directory: %v", err)
	}
}

// TestPushFlagsRefuseTheCombinationsThatWouldSilentlyDoNothing. --as with no
// --push discards the wing (ADR-006: a knob that does nothing must say when),
// and --push with no token would hit the Bearer gate and report a 401 that reads
// like a server problem rather than a missing flag.
//
// The third case is the one review found and it is the expensive one: a bundle
// carries no wing, so --push without --as uploads everything, the endpoint skips
// every record it cannot address, and the operator is told it worked. Deleting
// that check turns this red.
func TestPushFlagsRefuseTheCombinationsThatWouldSilentlyDoNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "import.toml"), `
[[dataset]]
file  = "x.jsonl"
room  = "schema"
title = "X"
why   = "because"
`)
	cfg := filepath.Join(dir, "import.toml")

	// Each case asserts the REASON, not merely that something failed. A missing
	// check here does not make the command succeed — it makes it fail later, at
	// the network, for a reason that has nothing to do with the flags — so a bare
	// err != nil would stay green with the check deleted.
	for _, tc := range []struct {
		name string
		o    importOptions
		want string
	}{
		{
			name: "--as with nothing to push to",
			o:    importOptions{configPath: cfg, as: "wing_x"},
			want: "--as applies to --push",
		},
		{
			name: "--push with no token",
			o:    importOptions{configPath: cfg, push: "https://example.invalid/import", as: "wing_x"},
			want: "--push needs --token",
		},
		{
			name: "--push with no wing to file into",
			o:    importOptions{configPath: cfg, push: "https://example.invalid/import", token: "t"},
			want: "--push needs --as",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runImport(context.Background(), tc.o)
			if err == nil {
				t.Fatalf("accepted; wanted a refusal mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused with %q, which does not mention %q — so this failed for some "+
					"other reason and the check may not exist", err, tc.want)
			}
		})
	}
}

// TestPushRefusesToSendTheTokenInCleartext: the workspace token rides on the
// push, and it is read/write access to the whole palace. A loopback host is
// exempt because `--local` binds exactly that and there is no path to listen on.
func TestPushRefusesToSendTheTokenInCleartext(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		wantErr  bool
	}{
		{"https://example.com/import", false},
		{"http://127.0.0.1:8080/import", false},
		{"http://localhost:8080/import", false},
		{"http://example.com/import", true},
		{"example.com/import", true}, // no scheme: not a URL the client can post to at all
	} {
		_, err := pushTarget(tc.endpoint, "wing_x")
		if (err != nil) != tc.wantErr {
			t.Errorf("pushTarget(%q): error = %v, wanted an error: %v", tc.endpoint, err, tc.wantErr)
		}
	}
}

// TestPushAsksTheEndpointToRebuildTheDerivedGraph. Hallways and entity tunnels
// are derived from the drawers rather than carried in the bundle, so a push that
// does not finalize leaves the new memories filed but outside the graph.
func TestPushAsksTheEndpointToRebuildTheDerivedGraph(t *testing.T) {
	u, err := pushTarget("https://example.com/import", "wing_x")
	if err != nil {
		t.Fatalf("pushTarget: %v", err)
	}
	if got := u.Query().Get("recompute"); got != "1" {
		t.Errorf("recompute=%q; the import files drawers and never rebuilds the graph they belong to", got)
	}
	if got := u.Query().Get("as"); got != "wing_x" {
		t.Errorf("as=%q, want wing_x", got)
	}
}

// TestPushFailsWhenTheServerFiledNothing is the second half of the same defect.
// POST /import consumes the whole body before replying, so it reports a storage
// failure INSIDE a 200 (Result.Error) and counts an unaddressable record as
// Skipped rather than refusing it. A client that stops at the status code cannot
// tell "filed two datasets" from "filed none".
//
// Proved by mutation: returning nil on any 2xx without decoding the summary
// turns both cases below green — which is exactly the shipped behaviour review
// caught.
func TestPushFailsWhenTheServerFiledNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "data", "x.jsonl"), "{\"a\":1}\n")
	write(t, filepath.Join(dir, "import.toml"), `
[[dataset]]
file  = "data/x.jsonl"
room  = "schema"
title = "X"
why   = "because"
`)
	cfg := filepath.Join(dir, "import.toml")

	for _, tc := range []struct {
		name    string
		summary importer.Result
		wantErr string
	}{
		{
			name:    "every record skipped, HTTP 200",
			summary: importer.Result{Drawers: 0, Skipped: 1, Done: true},
			wantErr: "0 of 1 dataset(s) were filed",
		},
		{
			name:    "storage failure reported inside a 200",
			summary: importer.Result{Drawers: 0, Error: "drawer import: absorb drawers: disk full", Done: true},
			wantErr: "disk full",
		},
		{
			name:    "everything filed",
			summary: importer.Result{Drawers: 1, Done: true},
			wantErr: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A loopback server, which is also the one cleartext endpoint the
			// client accepts — so this exercises that exemption rather than
			// working around the scheme check.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.summary)
			}))
			defer srv.Close()

			err := runImport(context.Background(), importOptions{
				configPath: cfg, push: srv.URL, token: "t", as: "wing_x",
			})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("push reported a failure the server did not: %v", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("the server filed nothing and the push reported success")
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error %q does not mention %q, so an operator cannot see what happened",
					err, tc.wantErr)
			}
		})
	}
}

// write creates a file and every directory above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
