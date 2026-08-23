package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/atvirokodosprendimai/agentsmemory/db"
)

// TestExpectedTablesDerivesTheRealSchema checks the derivation against the
// migrations this repository actually ships, rather than against a fixture.
//
// A fixture would prove the regexp works on SQL somebody wrote for the test. The
// point of this check is that it works on SQL somebody wrote for the DATABASE,
// including the long prose comments this repo puts in migrations, so it reads the
// real embed.
func TestExpectedTablesDerivesTheRealSchema(t *testing.T) {
	got, err := expectedTables(db.Migrations)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	if len(got) < schemaFloor {
		t.Fatalf("derived %d tables, below the floor of %d: %v", len(got), schemaFloor, got)
	}

	// Named explicitly because these are the two ends of the story: drawers is
	// the oldest core table, search_events is the one whose absence in production
	// produced this whole check.
	for _, want := range []string{"drawers", "teams", "search_events", "drawer_anchors"} {
		if !contains(got, want) {
			t.Errorf("derived table set is missing %q: %v", want, got)
		}
	}
}

// TestExpectedTablesIgnoresSQLInsideComments is a regression test for a trap this
// repository sets for itself.
//
// Migrations here carry paragraphs of explanation, and 00023's comment contains
// the words "CREATE TABLE IF NOT EXISTS" while explaining why the statement uses
// them. Matched against raw text, that sentence declares a table named "is", and
// doctor --schema would then report a missing table on every healthy database —
// a check that cries wolf, which is the kind that gets deleted and takes the real
// finding with it.
func TestExpectedTablesIgnoresSQLInsideComments(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/00001_real.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\n" +
				"-- This migration uses CREATE TABLE IF NOT EXISTS because it repairs.\n" +
				"-- A DROP TABLE users here would also be a lie.\n" +
				"CREATE TABLE genuine (id TEXT PRIMARY KEY);\n" +
				"-- +goose Down\n" +
				"DROP TABLE genuine;\n")},
	}

	got, err := expectedTables(fsys)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(got) != 1 || got[0] != "genuine" {
		t.Fatalf("comments leaked into the derived set: got %v, want [genuine]", got)
	}
}

// TestExpectedTablesForgetsWhatALaterMigrationDropped checks that the derivation
// replays history rather than accumulating it. A table created in 00001 and
// dropped in 00002 must not be expected, or the check demands a table the schema
// deliberately no longer has.
func TestExpectedTablesForgetsWhatALaterMigrationDropped(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/00001_add.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nCREATE TABLE temporary_thing (id TEXT);\nCREATE TABLE keeper (id TEXT);\n-- +goose Down\n")},
		"migrations/00002_remove.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nDROP TABLE temporary_thing;\n-- +goose Down\n")},
	}

	got, err := expectedTables(fsys)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if contains(got, "temporary_thing") {
		t.Errorf("a dropped table is still expected: %v", got)
	}
	if !contains(got, "keeper") {
		t.Errorf("a surviving table was lost: %v", got)
	}
}

// TestExpectedTablesReadsOnlyTheUpSection guards the mistake that would make this
// check derive nothing at all: every Down section drops what its Up created, so
// reading the whole file cancels the schema out to an empty set — which then
// passes against every database.
func TestExpectedTablesReadsOnlyTheUpSection(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/00001_x.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nCREATE TABLE kept (id TEXT);\n-- +goose Down\nDROP TABLE kept;\n")},
	}

	got, err := expectedTables(fsys)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !contains(got, "kept") {
		t.Fatalf("the Down section cancelled the Up section: got %v", got)
	}
}

// TestReportSchemaDriftNamesEveryMissingTable is the check doing its job: a
// database holding every table but one is reported, by name, with a non-nil error
// so the exit code carries the verdict.
func TestReportSchemaDriftNamesEveryMissingTable(t *testing.T) {
	want := make([]string, 0, schemaFloor+1)
	for _, n := range []string{"drawers", "teams", "users", "search_events"} {
		want = append(want, n)
	}
	for len(want) < schemaFloor+1 {
		want = append(want, "filler"+string(rune('a'+len(want))))
	}
	have := want[:len(want)-1] // every table except the last

	var out bytes.Buffer
	err := reportSchemaDrift(&out, 23, want, have)
	if err == nil {
		t.Fatal("a missing table returned a nil error, so the exit code says the palace is fine")
	}
	if !strings.Contains(out.String(), "MISSING") {
		t.Errorf("report does not announce the drift:\n%s", out.String())
	}
	if !strings.Contains(out.String(), want[len(want)-1]) {
		t.Errorf("report does not name the missing table %q:\n%s", want[len(want)-1], out.String())
	}
	if !strings.Contains(out.String(), "goose version 23") {
		t.Errorf("report omits the version, which is the whole point — a version past a missing effect:\n%s", out.String())
	}
}

// TestReportSchemaDriftPassesAHealthyDatabase pins the other half: extra tables
// the migrations never declared (goose's own bookkeeping, a manual index table)
// are not drift, and must not fail the check.
func TestReportSchemaDriftPassesAHealthyDatabase(t *testing.T) {
	want := make([]string, 0, schemaFloor)
	for len(want) < schemaFloor {
		want = append(want, "t"+string(rune('a'+len(want))))
	}
	have := append(append([]string{}, want...), "goose_db_version", "sqlite_sequence")

	var out bytes.Buffer
	if err := reportSchemaDrift(&out, 23, want, have); err != nil {
		t.Fatalf("a healthy database was reported as drifted: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "all") {
		t.Errorf("healthy report is not legible:\n%s", out.String())
	}
}

// TestReportSchemaDriftRefusesAnEmptyUniverse is the floor.
//
// If the derivation stops matching — the goose markers are renamed, the directory
// moves — expectedTables returns few or no tables and every database satisfies
// it. The check would then pass forever while checking nothing, which is strictly
// worse than not having it, because the green is believed.
func TestReportSchemaDriftRefusesAnEmptyUniverse(t *testing.T) {
	var out bytes.Buffer
	err := reportSchemaDrift(&out, 23, []string{"drawers"}, []string{"drawers"})
	if err == nil {
		t.Fatal("a universe of 1 table passed the floor: the check can now succeed while checking nothing")
	}
	if !strings.Contains(err.Error(), "floor") {
		t.Errorf("the failure does not explain itself: %v", err)
	}
}
