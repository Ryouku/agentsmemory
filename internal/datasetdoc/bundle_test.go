package datasetdoc

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle"
)

const configTOML = `
wing = "wing_acme"

[[dataset]]
file  = "data/invoices.jsonl"
room  = "schema"
title = "Invoice seed data"
why   = """
Seeded from one anonymised quarter, which is why every due date is in Q1.
amount is minor units, not currency units — 1200 is twelve euros."""
`

func mustParse(t *testing.T, s string) Config {
	t.Helper()
	cfg, err := ParseConfig(strings.NewReader(s))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

func openerFor(files map[string]string) Opener {
	return func(path string) (io.ReadCloser, error) {
		body, ok := files[path]
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

func decodeBundle(t *testing.T, s string) []wingbundle.Record {
	t.Helper()
	var out []wingbundle.Record
	dec := json.NewDecoder(strings.NewReader(s))
	for {
		var r wingbundle.Record
		if err := dec.Decode(&r); err == io.EOF {
			return out
		} else if err != nil {
			t.Fatalf("decode bundle: %v", err)
		}
		out = append(out, r)
	}
}

// TestBundleCarriesTheHumanAccountAndTheMeasuredProfile is the whole point of
// the format: either half alone is worth less than the pair. The profile without
// the explanation records what a reader could derive; the explanation without the
// profile is a claim with no data under it.
func TestBundleCarriesTheHumanAccountAndTheMeasuredProfile(t *testing.T) {
	cfg := mustParse(t, configTOML)
	files := map[string]string{"data/invoices.jsonl": sample}
	var b strings.Builder

	n, err := Bundle(cfg, openerFor(files), time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC), &b)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if n != 1 {
		t.Fatalf("wrote %d datasets, want 1", n)
	}

	recs := decodeBundle(t, b.String())
	if len(recs) != 2 || recs[0].Kind != wingbundle.KindManifest {
		t.Fatalf("want manifest + 1 drawer, got %d records", len(recs))
	}
	// The manifest must be the format the importer already reads, or this
	// produces a file only this package understands.
	if recs[0].Format != wingbundle.Format {
		t.Errorf("manifest format %q, want %q", recs[0].Format, wingbundle.Format)
	}

	d := recs[1]
	if d.Kind != wingbundle.KindDrawer || d.Room != "schema" {
		t.Errorf("kind=%q room=%q, want drawer/schema", d.Kind, d.Room)
	}
	// Idempotency is by source, and the source is the dataset's own path — this
	// is what makes a re-import a replacement rather than a duplicate.
	if d.SourceFile != "data/invoices.jsonl" {
		t.Errorf("source_file=%q, want the dataset path", d.SourceFile)
	}

	for _, want := range []string{
		"twelve euros",         // the human half, carried verbatim
		"3 rows",               // the measured half
		"open, paid",           // the value set the profile found
		"number|string",        // the inconsistent type, surfaced
		"ONE ROW, VERBATIM",    // the example
		"not\nconstraints the", // the caveat that a value set is about THIS file
	} {
		if !strings.Contains(d.Content, want) && !strings.Contains(d.Content, strings.ReplaceAll(want, "\n", " ")) {
			t.Errorf("drawer content is missing %q:\n%s", want, d.Content)
		}
	}

	// A search for a column name should reach the dataset that has that column.
	if strings.Join(d.Entities, ",") != "amount,due,id,note,status" {
		t.Errorf("entities=%v, want the field names", d.Entities)
	}
}

// TestTheExplanationComesBeforeTheNumbers: recall returns a WINDOW around the
// match, so whichever half is further down can be cut off. The half that cannot
// be re-derived from the file has to be the one a snippet finds.
func TestTheExplanationComesBeforeTheNumbers(t *testing.T) {
	cfg := mustParse(t, configTOML)
	var b strings.Builder
	if _, err := Bundle(cfg, openerFor(map[string]string{"data/invoices.jsonl": sample}),
		time.Now(), &b); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	c := decodeBundle(t, b.String())[1].Content
	why, measured := strings.Index(c, "twelve euros"), strings.Index(c, "MEASURED FROM THE FILE")
	if why < 0 || measured < 0 || why > measured {
		t.Errorf("the human account must precede the measured profile (why=%d measured=%d)", why, measured)
	}
}

// TestAMissingDatasetIsAHardError: skipping it would leave the mapping file
// describing a dataset the bundle does not contain, and the import would report
// success while dropping the thing someone asked for.
func TestAMissingDatasetIsAHardError(t *testing.T) {
	cfg := mustParse(t, configTOML)
	var b strings.Builder
	if _, err := Bundle(cfg, openerFor(map[string]string{}), time.Now(), &b); err == nil {
		t.Fatal("a dataset whose file could not be opened was skipped silently")
	}
}

// TestConfigRefusesWhatWouldFileNothingWorthReading. Each of these produces a
// bundle that imports cleanly and says nothing — the shape this repository keeps
// naming, an operation that reports success and leaves nothing behind.
func TestConfigRefusesWhatWouldFileNothingWorthReading(t *testing.T) {
	for _, tc := range []struct{ name, toml string }{
		{"no datasets at all", `wing = "wing_acme"`},
		{"a dataset with no explanation", `
[[dataset]]
file  = "a.jsonl"
room  = "schema"
title = "A"
`},
		{"a dataset with no room", `
[[dataset]]
file  = "a.jsonl"
title = "A"
why   = "because"
`},
		{"a dataset with no file", `
[[dataset]]
room  = "schema"
title = "A"
why   = "because"
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseConfig(strings.NewReader(tc.toml)); err == nil {
				t.Error("accepted a config that cannot produce a useful memory")
			}
		})
	}
}
