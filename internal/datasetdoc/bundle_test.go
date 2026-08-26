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
show_values = ["status"]
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
		"twelve euros",           // the human half, carried verbatim
		"3 rows",                 // the measured half
		"open, paid",             // the value set of the ONE field show_values names
		"number|string",          // the inconsistent type, surfaced
		"COUNTED AND NOT QUOTED", // the fields it was not asked to quote, said out loud
		"not\nconstraints the",   // the caveat that a value set is about THIS file
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

// TestValuesAppearOnlyForFieldsTheMappingNames is the disclosure gate.
//
// A drawer is filed into a wing every agent recalls from, and the importer
// embeds and indexes on arrival — so a value quoted here is published to all of
// them, and a later re-import that fixes the mapping replaces the drawer without
// un-publishing anything. That asymmetry is why the profile quotes a column only
// when the mapping file names it.
//
// The assertions are on the VALUES rather than on the section that prints them,
// which is what makes this hold shut against more than the one mistake it was
// written for: deleting the allowlist in summarise turns it red, and so would
// re-introducing a verbatim example row, a "first five rows" sample, or any
// other future path that carries a raw cell out of the file.
func TestValuesAppearOnlyForFieldsTheMappingNames(t *testing.T) {
	const people = `{"id":"u-1","status":"active","email":"ada@example.com","phone":"+370 600 11111"}
{"id":"u-2","status":"invited","email":"grace@example.com","phone":"+370 600 22222"}
`
	const peopleTOML = `
[[dataset]]
file  = "data/users.jsonl"
room  = "schema"
title = "User seed data"
why   = "Two seeded operators; every login-flow test starts from these."
show_values = ["status"]
`
	var b strings.Builder
	if _, err := Bundle(mustParse(t, peopleTOML), openerFor(map[string]string{"data/users.jsonl": people}),
		time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC), &b); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	content := decodeBundle(t, b.String())[1].Content

	for _, value := range []string{
		"ada@example.com", "grace@example.com", "+370 600 11111", "+370 600 22222", "u-1", "u-2",
	} {
		if strings.Contains(content, value) {
			t.Errorf("the drawer quotes %q, from a field show_values does not name — once filed, "+
				"that value is recallable by every agent in the wing:\n%s", value, content)
		}
	}

	// The named field IS quoted, or an allowlist would be indistinguishable from
	// never reporting values at all — and the enumeration is the fact the profile
	// exists to carry.
	if !strings.Contains(content, "active, invited") {
		t.Errorf("status is named in show_values and its values are missing:\n%s", content)
	}

	// Schema is not the secret. A reader must still learn the column exists, what
	// type it holds and how many values it took — that count is the only pointer
	// saying an unnamed field has an enumeration worth asking for.
	for _, want := range []string{
		"email (string)", "phone (string)", "2 distinct value(s), not listed",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the profile withheld %q along with the values; only the values are "+
				"sensitive:\n%s", want, content)
		}
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

// TestTheSameDataProducesTheSameDrawerOnEveryDay is the idempotency gate.
//
// A drawer's id is a hash of (wing, room, source_file, chunk, CONTENT), and the
// import path absorbs without purging by source. So anything volatile in the
// text becomes a NEW memory rather than an update: with the measurement date
// written into the body, a nightly re-import over an unchanged file filed one
// more drawer every night, each saying the same thing, and the ADR's claim that
// a committed mapping file makes re-import a replacement was false.
//
// Proved by mutation: writing measuredAt back into drawerFor's text turns this
// red. The date still travels — as content_date, asserted below — so the profile
// remains a dated snapshot without the date deciding its identity.
func TestTheSameDataProducesTheSameDrawerOnEveryDay(t *testing.T) {
	cfg := mustParse(t, configTOML)
	files := map[string]string{"data/invoices.jsonl": `{"id":"inv-1","status":"paid","due":"2026-01-15"}`}

	drawerOn := func(day time.Time) wingbundle.Record {
		t.Helper()
		var b strings.Builder
		if _, err := Bundle(cfg, openerFor(files), day, &b); err != nil {
			t.Fatalf("Bundle: %v", err)
		}
		for _, r := range decodeBundle(t, b.String()) {
			if r.Kind == wingbundle.KindDrawer {
				return r
			}
		}
		t.Fatal("bundle carried no drawer")
		return wingbundle.Record{}
	}

	today := drawerOn(time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC))
	tomorrow := drawerOn(time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC))

	if today.Content != tomorrow.Content {
		t.Errorf("the same file profiled on two days produced two different memories, so a "+
			"re-import files a second drawer instead of replacing the first:\n--- day one ---\n%s\n"+
			"--- day two ---\n%s", today.Content, tomorrow.Content)
	}
	if today.ContentDate != "2026-08-26" || tomorrow.ContentDate != "2026-08-27" {
		t.Errorf("content_date is %q and %q; the snapshot must still carry the day it was taken",
			today.ContentDate, tomorrow.ContentDate)
	}
}

// TestAPartialReadSaysSoInTheMemory: a line over the size cap ends the scan, and
// keeping what was already read is the right call — the alternative is returning
// nothing because row 40,000 was oversized. But a row count that stopped at two
// looks exactly like a file with two rows, so the drawer has to say which it is.
func TestAPartialReadSaysSoInTheMemory(t *testing.T) {
	var f strings.Builder
	f.WriteString(`{"id":"a"}` + "\n")
	f.WriteString(`{"id":"` + strings.Repeat("x", maxLineBytes) + `"}` + "\n")
	for i := 0; i < 100; i++ {
		f.WriteString(`{"id":"later"}` + "\n")
	}

	cfg := mustParse(t, configTOML)
	var out strings.Builder
	if _, err := Bundle(cfg, openerFor(map[string]string{"data/invoices.jsonl": f.String()}),
		time.Now().UTC(), &out); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "READING STOPPED EARLY") {
		t.Errorf("the scan stopped at an oversized line and the memory reports the partial counts "+
			"as if they were the file:\n%s", body)
	}
}
