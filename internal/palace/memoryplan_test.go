package palace

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// TestMemoryChunkLookupSeeksRatherThanScansTheTenant is a gate on an access
// path, not on a result.
//
// ADR-024 put MemoryChunksByRoots on every recall, twice, in BOTH arms. Written
// as `id IN (...) OR parent_id IN (...)` it examined every drawer the tenant
// owned, because no planner seeks both sides of a disjunction in one index pass
// — and adding the parent_id index alone does NOT change that plan, which is
// why the fix is a union and an index together.
//
// Asserting on returned chunks cannot see any of this: the OR spelling returns
// exactly the same rows, correctly, while reading the whole table. So this reads
// the plan. Reverting either half — the UNION in memoryChunkQuery, or migration
// 00024 — puts `SCAN drawers` back and turns this red.
func TestMemoryChunkLookupSeeksRatherThanScansTheTenant(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// A real memory long enough to be stored as several chunks, so the plan is
	// resolved against a table that actually holds roots and children.
	added, err := svc.Add(ctx, "team-plan", AddInput{
		Wing: "wing_plan", Room: "decisions",
		Content: strings.Repeat("a memory long enough to be chunked into siblings ", 80),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("content produced %d chunks; the fixture needs a root and children", len(added.Drawers))
	}
	root := added.Drawers[0].ID

	for _, columns := range []string{"*", "id, parent_id, chunk_index"} {
		plan := memoryChunkQueryPlan(t, svc, ctx, "team-plan", []string{root}, columns)
		if strings.Contains(strings.ToUpper(plan), "SCAN DRAWERS") {
			t.Fatalf("memory chunk lookup (columns %q) scans the tenant's drawers instead of seeking:\n%s", columns, plan)
		}
		// Assert on the CONSTRAINED COLUMNS, not on the index name.
		//
		// This is the whole trap: with migration 00024 applied, the old OR
		// spelling produces `SEARCH drawers USING INDEX idx_drawers_team_parent
		// (team_id=?)`. It names the new index and contains no "SCAN", yet
		// constrains team_id ALONE — every row of the tenant, which is the
		// defect. Only the seek columns tell the two apart.
		for _, seek := range []string{"AND id=?", "AND parent_id=?"} {
			if !strings.Contains(plan, seek) {
				t.Fatalf("memory chunk lookup (columns %q) has no %q seek, so it is reading more than the requested roots:\n%s",
					columns, seek, plan)
			}
		}
	}
}

// memoryChunkQueryPlan returns SQLite's plan for the REAL query the repo issues,
// one plan row per line. It renders the query through a dry-run session so the
// statement under test is the shipped one rather than a hand-copied echo of it.
func memoryChunkQueryPlan(t *testing.T, svc *Service, ctx context.Context, teamID string, roots []string, columns string) string {
	t.Helper()

	dry := &Repo{db: svc.repo.db.Session(&gorm.Session{DryRun: true})}
	stmt := dry.memoryChunkQuery(ctx, teamID, roots, columns).Find(&[]drawerRow{}).Statement
	sql := stmt.SQL.String()
	if sql == "" {
		t.Fatal("dry run produced no SQL; the probe is not probing")
	}

	rows, err := svc.repo.db.WithContext(ctx).Raw("EXPLAIN QUERY PLAN "+sql, stmt.Vars...).Rows()
	if err != nil {
		t.Fatalf("explain: %v (sql=%s)", err, sql)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		out = append(out, detail)
	}
	if len(out) == 0 {
		t.Fatalf("EXPLAIN QUERY PLAN returned no rows for %s", sql)
	}
	return strings.Join(out, "\n")
}
