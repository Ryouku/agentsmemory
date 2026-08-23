package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// schemaFloor is the smallest number of tables the migrations may legitimately
// declare. It exists because the derivation below reads SQL with a regexp, and
// the failure mode of a regexp that stops matching is SILENCE: rename the goose
// markers, restructure the directory, and expectedTables returns an empty set
// that every database satisfies. A check that finds nothing to require finds no
// gaps, and reports a clean palace while doing nothing at all.
//
// The value is taken from what the migrations declared when this was written,
// rounded down. It is a floor and not an equality: adding tables is ordinary
// work and must not fail the check.
const schemaFloor = 15

// createTable and dropTable find the tables a migration brings into and out of
// existence.
//
// They are deliberately applied to comment-stripped SQL. Migration files in this
// repository carry long prose explanations, and 00023's own comment contains the
// words "CREATE TABLE IF NOT EXISTS" while explaining why it uses them — matched
// raw, that sentence declares a table named "is". The comment stripping is not
// tidiness; it is the difference between a report an operator can trust and one
// that invents a missing table on every run.
var (
	createTable = regexp.MustCompile(`(?is)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["'` + "`" + `\[]?([a-zA-Z_][a-zA-Z0-9_]*)`)
	dropTable   = regexp.MustCompile(`(?is)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["'` + "`" + `\[]?([a-zA-Z_][a-zA-Z0-9_]*)`)
	lineComment = regexp.MustCompile(`--[^\n]*`)
)

// expectedTables derives the tables a fully migrated database must hold, by
// replaying every migration's Up section in version order.
//
// The universe is DERIVED rather than listed, and that is the whole design. A
// hand-maintained list of tables is one migration away from being wrong, and
// wrong in the direction that matters: the table nobody remembered to add to the
// list is exactly the one whose absence goes unnoticed. Reading the migrations
// means a table cannot enter the schema without entering this set in the same
// commit, because the migration IS how it enters the schema.
//
// Only the Up half of each file is read. A Down section drops what its Up
// created, so including it would cancel every table out and derive nothing.
func expectedTables(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	// Filename order is version order: goose numbers them zero-padded for exactly
	// this reason, so a lexical sort and a numeric one agree.
	sort.Strings(names)

	live := map[string]bool{}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, path.Join("migrations", name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		up := lineComment.ReplaceAllString(upSection(string(raw)), "")
		for _, m := range createTable.FindAllStringSubmatch(up, -1) {
			live[m[1]] = true
		}
		for _, m := range dropTable.FindAllStringSubmatch(up, -1) {
			delete(live, m[1])
		}
	}

	out := make([]string, 0, len(live))
	for t := range live {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// upSection returns the part of a goose migration that runs on the way up.
func upSection(sqlText string) string {
	const down = "-- +goose Down"
	if i := strings.Index(sqlText, down); i >= 0 {
		return sqlText[:i]
	}
	return sqlText
}

// doctorSchema reports tables the migrations declare that the database does not
// have.
//
// THIS IS THE ONE CHECK NO TEST IN THIS REPOSITORY CAN REPLACE, and it was
// written because the gap it covers was found in production rather than reasoned
// about. TestMigrationsRoundTrip runs every migration against a FRESH database,
// where each one applies in order and each effect lands; it cannot represent a
// database that recorded a version without its effect. On 2026-08-23 the hosted
// palace was exactly that: goose past 00021 with search_events absent, so
// am_recall_stats failed while the next migration's table answered normally.
//
// Nothing announced it. palace.recordSearch swallows its write errors on purpose
// — measurement that can break the thing it measures is worse than no
// measurement — so every recall statistic was silently discarded while search
// itself worked perfectly. The verdict therefore has to be an exit code an
// operator can run before trusting a deployment, not a line in a runbook.
//
// It takes no --project: a missing table belongs to the deployment, not to one
// workspace.
//
// ⚠ IT MUST NOT MIGRATE THE DATABASE IT INSPECTS, which is why it opens the file
// directly instead of going through buildServicesWith like doctorRoles and
// doctorIndex do. Those build the full service stack, and that stack runs goose
// on the way up. A schema check that migrates first would repair the very drift
// it exists to report and then announce that everything is present — a check
// structurally incapable of failing, reporting green from one layer about a
// question asked of another. The direct open is the whole reason this can fail.
func doctorSchema(_ context.Context, cfg config.Config, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	gdb, err := openDB(cfg.DBPath, cfg.Debug)
	if err != nil {
		return err
	}

	want, err := expectedTables(db.Migrations)
	if err != nil {
		return err
	}

	var have []string
	if err := gdb.Raw(`SELECT name FROM sqlite_master WHERE type = 'table'`).Scan(&have).Error; err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	var version int64
	// A database with no goose row at all reports 0, which reads correctly as
	// "nothing has been migrated here" rather than as an error.
	_ = gdb.Raw(`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version`).Scan(&version).Error

	return reportSchemaDrift(out, version, want, have)
}

// reportSchemaDrift renders the verdict and returns it as an error so the exit
// code carries it.
//
// Split from the lookup for the reason every other doctor report is: the
// rendering is what an operator reads, and a report that needs a live database
// to exercise is a report that quietly stops saying anything. It prints table
// names and a version number and never memory text — a doctor report gets pasted
// into an issue, and the palace it describes is private.
func reportSchemaDrift(out io.Writer, version int64, want, have []string) error {
	if len(want) < schemaFloor {
		return fmt.Errorf("schema: derived only %d expected table(s) from the migrations, below the floor of %d — "+
			"the derivation has stopped reading them, so this check is not checking anything", len(want), schemaFloor)
	}

	present := make(map[string]bool, len(have))
	for _, t := range have {
		present[t] = true
	}

	var missing []string
	for _, t := range want {
		if !present[t] {
			missing = append(missing, t)
		}
	}

	if len(missing) == 0 {
		fmt.Fprintf(out, "schema: goose version %d, all %d migrated table(s) present\n", version, len(want))
		return nil
	}

	fmt.Fprintf(out, "schema: goose version %d, but %d of %d migrated table(s) are MISSING\n\n", version, len(missing), len(want))
	for _, t := range missing {
		fmt.Fprintf(out, "  missing: %s\n", t)
	}
	fmt.Fprintf(out, "\nThe version counter has moved past a migration whose effect is gone.\n")
	fmt.Fprintf(out, "Migrating forward repairs any table whose migration uses CREATE TABLE IF NOT EXISTS;\n")
	fmt.Fprintf(out, "one that does not needs a repair migration, as 00023 is for search_events.\n")
	return fmt.Errorf("schema: %d table(s) missing", len(missing))
}
