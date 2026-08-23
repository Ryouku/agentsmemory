package db_test

import (
	"database/sql"
	"embed"
	"io/fs"
	"path"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	_ "github.com/glebarez/sqlite" // registers the cgo-free "sqlite" driver
	"github.com/pressly/goose/v3"
)

// releasedMigrations preserves migration bodies that shipped in an earlier
// history but are absent from the current tree. They are upgrade fixtures, not
// migrations the server should discover at runtime.
//
//go:embed testdata/migrations/*.sql
var releasedMigrations embed.FS

// TestMigrationsRoundTrip runs every migration Up, then all the way Down, then Up
// again against a real (modernc) sqlite file. It guards the whole schema — and
// especially newer statements like 00013's ADD COLUMN / partial index / DROP
// COLUMN — against a migration that applies but cannot be rolled back or re-run.
func TestMigrationsRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "roundtrip.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("up: %v", err)
	}
	assertSearchEventsSchema(t, sqlDB)
	if err := goose.DownTo(sqlDB, "migrations", 22); err != nil {
		t.Fatalf("down repair migration: %v", err)
	}
	// Version 21 remains applied after this rollback, so version 23's Down must
	// preserve the schema that version 21 owns on a fresh database.
	assertSearchEventsSchema(t, sqlDB)
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("reapply repair migration: %v", err)
	}
	if err := goose.DownTo(sqlDB, "migrations", 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	assertSearchEventsSchema(t, sqlDB)
}

// TestMigrationsUpgradeFromV0045 reproduces the released v0.0.45 database
// cohort before applying the current embedded migrations. That release used
// version 21 for the Free-plan cap; the current history uses version 21 for
// search_events, so Goose skips the latter on an upgraded database.
func TestMigrationsUpgradeFromV0045(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v0.0.45-upgrade.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetBaseFS(v0045Migrations(t))
	t.Cleanup(func() { goose.SetBaseFS(db.Migrations) })

	if err := goose.UpTo(sqlDB, "migrations", 21); err != nil {
		t.Fatalf("migrate to released v0.0.45 schema: %v", err)
	}
	assertPlanCap(t, sqlDB, "personal", 1000)
	assertSQLiteObject(t, sqlDB, "table", "search_events", false)

	goose.SetBaseFS(db.Migrations)
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("upgrade released v0.0.45 schema: %v", err)
	}

	assertSearchEventsSchema(t, sqlDB)
	// Repairing the missing table must not silently change the legacy cohort's
	// Free-plan cap; choosing 1,000 or 10,000 is a separate product decision.
	assertPlanCap(t, sqlDB, "personal", 1000)
}

func assertSearchEventsSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()

	assertSQLiteObject(t, sqlDB, "table", "search_events", true)
	assertSQLiteObject(t, sqlDB, "index", "idx_search_events_team_time", true)
}

func v0045Migrations(t *testing.T) fs.FS {
	t.Helper()

	migrations := fstest.MapFS{}
	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			t.Fatalf("parse migration %q: %v", entry.Name(), err)
		}
		if version >= 21 {
			continue
		}
		migrationPath := path.Join("migrations", entry.Name())
		data, err := fs.ReadFile(db.Migrations, migrationPath)
		if err != nil {
			t.Fatalf("read %s: %v", migrationPath, err)
		}
		migrations[migrationPath] = &fstest.MapFile{Data: data}
	}

	const releasedPath = "testdata/migrations/00021_free_plan_cap_1000.sql"
	data, err := releasedMigrations.ReadFile(releasedPath)
	if err != nil {
		t.Fatalf("read released migration fixture: %v", err)
	}
	migrations["migrations/00021_free_plan_cap_1000.sql"] = &fstest.MapFile{Data: data}
	return migrations
}

func assertSQLiteObject(t *testing.T, sqlDB *sql.DB, objectType, name string, want bool) {
	t.Helper()

	var count int
	if err := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?",
		objectType,
		name,
	).Scan(&count); err != nil {
		t.Fatalf("find SQLite %s %q: %v", objectType, name, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("SQLite %s %q exists = %t, want %t", objectType, name, got, want)
	}
}

func assertPlanCap(t *testing.T, sqlDB *sql.DB, code string, want int) {
	t.Helper()

	var got int
	if err := sqlDB.QueryRow(
		"SELECT monthly_request_cap FROM plans WHERE code = ?",
		code,
	).Scan(&got); err != nil {
		t.Fatalf("read %s plan cap: %v", code, err)
	}
	if got != want {
		t.Fatalf("%s plan cap = %d, want %d", code, got, want)
	}
}

// TestRepairMigrationRestoresADriftedSchema reproduces the drift that was found
// on the hosted palace on 2026-08-23 -- a database whose goose version had moved
// past 00021 while the table that migration creates was absent -- and asserts
// that migrating forward repairs it.
//
// The reproduction matters more than the assertion. TestMigrationsRoundTrip
// above only ever runs against a FRESH database, so it cannot see this class at
// all: every migration applies in order and every effect lands. The failure
// being guarded here is a recorded version whose effect is missing, which is
// invisible to any check that starts from nothing. That is why the table is
// dropped out from under goose here rather than simply never created.
//
// Deleting 00023_search_events_repair.sql makes this test fail, which is the
// property that makes it a gate rather than a demonstration.
func TestRepairMigrationRestoresADriftedSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "drifted.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}

	// Stop at 00022 so the database is at the version the live palace was found
	// at, then remove 00021's table without touching goose's version record.
	// That combination -- version present, effect gone -- IS the defect.
	if err := goose.UpTo(sqlDB, "migrations", 22); err != nil {
		t.Fatalf("up to 22: %v", err)
	}
	if _, err := sqlDB.Exec(`DROP TABLE search_events`); err != nil {
		t.Fatalf("simulate drift by dropping search_events: %v", err)
	}
	if tableExists(t, sqlDB, "search_events") {
		t.Fatal("search_events still present after the drop; the reproduction is not reproducing")
	}

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("up (repair): %v", err)
	}
	if !tableExists(t, sqlDB, "search_events") {
		t.Fatal("search_events absent after migrating forward: the repair migration did not repair")
	}

	// The repair must also be writable, not merely present -- a table created
	// with the wrong columns satisfies a name check and still loses every row.
	if _, err := sqlDB.Exec(
		`INSERT INTO search_events (id, team_id, wing, room, query, candidates, hits, top_score, reranked, created_at)
		 VALUES ('t1', 't', 'wing_x', '', 'q', 3, 1, 0.5, 1, '2026-08-23T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert into the repaired table: %v", err)
	}
}

// tableExists reports whether a table of this name is in the schema.
func tableExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master for %s: %v", name, err)
	}
	return n > 0
}
