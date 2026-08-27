package palace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
)

// ADR-038 T2. drawers.content_key carries the hash dedup matches on, and the
// unique index over it is scoped to CURRENT rows.

func TestAddStampsTheContentKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ck"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "s", Content: "hello"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	d, _ := svc.Get(ctx, team, res.Drawers[0].ID)
	want := DrawerID(team, "w", "r", "s", 0, "hello")
	if d.ContentKey != want {
		t.Errorf("ContentKey = %q; want the DrawerID recipe over the row's own fields, %q", d.ContentKey, want)
	}
}

func TestUpdateRecomputesTheContentKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ck2"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "before"})
	id := res.Drawers[0].ID
	after := "after"
	up, err := svc.Update(ctx, team, id, DrawerPatch{Content: &after, Reason: "the first version was wrong"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := DrawerID(team, "w", "r", "", 0, "after")
	if up.Drawer.ContentKey != want {
		t.Errorf("ContentKey = %q on the correcting record; want the hash of the NEW content %q",
			up.Drawer.ContentKey, want)
	}
	// The ended record keeps the key of the text it still holds. Both rows carry a
	// key at once, which the partial unique index permits precisely because it is
	// scoped to valid_to = '' — a key is unique among CURRENT rows, not for all time.
	prev, _ := svc.Get(ctx, team, id)
	if prev.ContentKey != DrawerID(team, "w", "r", "", 0, "before") {
		t.Errorf("the superseded row's ContentKey = %q; it keeps its text, so it keeps its key", prev.ContentKey)
	}
}

func TestMergeWingRecomputesTheContentKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ck3"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "src", Room: "r", Content: "moved"})
	id := res.Drawers[0].ID
	if _, err := svc.MergeWing(ctx, team, []string{"src"}, "dst"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	d, _ := svc.Get(ctx, team, id)
	want := DrawerID(team, "dst", "r", "", 0, "moved")
	if d.ContentKey != want {
		t.Errorf("ContentKey = %q after a wing move; want the hash computed with the TARGET wing %q.\n"+
			"A merge is the path easiest to forget: the row moves and the key must move with it", d.ContentKey, want)
	}
}

func TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ck4"

	for i := 0; i < 2; i++ {
		if _, err := svc.WriteDiary(ctx, team, DiaryWriteInput{
			Wing: "w", Agent: "claude", Topic: "general", Entry: "the identical reflection",
		}); err != nil {
			t.Fatalf("diary write %d: %v", i, err)
		}
	}
	rows, err := svc.repo.CurrentDrawers(ctx, team, "w")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d diary rows; a journal is append-only and must NOT dedupe — two identical "+
			"reflections are two entries", len(rows))
	}
	for _, d := range rows {
		if d.ContentKey != "" {
			t.Errorf("diary row carries ContentKey %q; it must be EMPTY so the partial index never "+
				"sees it — that predicate is the only thing keeping a journal out of dedup", d.ContentKey)
		}
	}
}

// TestTheContentKeyIndexIsPartialOnBothConjuncts reads the REAL index definition
// rather than trusting the migration text. Each conjunct fails differently and
// each gets its own mutant:
//
//   - without content_key != ”, every keyless row shares one index entry and an
//     upsert overwrites an unrelated memory. The only silent, destroying failure.
//   - without valid_to = ”, a superseded row keeps competing for content it no
//     longer asserts, and text once superseded can never be filed again.
func TestTheContentKeyIndexIsPartialOnBothConjuncts(t *testing.T) {
	svc := newTestService(t)
	var ddl string
	if err := svc.repo.db.Raw(
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_drawers_content_key'`).
		Scan(&ddl).Error; err != nil {
		t.Fatalf("read index ddl: %v", err)
	}
	if strings.TrimSpace(ddl) == "" {
		t.Fatal("idx_drawers_content_key does not exist")
	}
	if !strings.Contains(strings.ToUpper(ddl), "UNIQUE") {
		t.Errorf("index is not UNIQUE: %s", ddl)
	}
	norm := strings.Join(strings.Fields(ddl), " ")
	if !strings.Contains(norm, "content_key != ''") {
		t.Errorf("index predicate is missing `content_key != ''` — every keyless row would share one "+
			"index entry and an upsert would overwrite an unrelated memory.\n  ddl: %s", norm)
	}
	if !strings.Contains(norm, "valid_to = ''") {
		t.Errorf("index predicate is missing `valid_to = ''` — a superseded row would keep competing "+
			"for content it no longer asserts, so text once superseded could never be filed again.\n  ddl: %s", norm)
	}
}

func TestAnEndedRowDoesNotBlockRefilingItsOwnText(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ck5"

	// SOURCE-LESS on purpose. A named source goes through purgeSource, which still
	// HARD-DELETES the whole source before re-inserting — converting that to a set
	// difference that ENDS is T3's job, not this task's. Here the question is only
	// whether the unique index blocks the re-file, and whether Save's conflict
	// clause quietly resurrects the ended row.
	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the original claim"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	first := res.Drawers[0].ID
	if err := svc.EndDrawer(ctx, team, first, "turned out to be wrong"); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the original claim"}); err != nil {
		t.Fatalf("re-filing the text of an ENDED row must succeed — the unique index is scoped to "+
			"CURRENT rows precisely so history does not compete for a name: %v", err)
	}
	// And the ending must survive: a re-add must not resurrect the retracted row
	// by clobbering its window. Save's UpdateAll would do exactly that.
	ended, err := svc.Get(ctx, team, first)
	if err != nil {
		t.Fatalf("the ended row must still be readable: %v", err)
	}
	if ended.ValidTo == "" || ended.EndedReason != "turned out to be wrong" {
		t.Errorf("re-filing resurrected the ended row: ValidTo=%q EndedReason=%q.\n"+
			"An ending is a decision; a later re-file must not silently undo it",
			ended.ValidTo, ended.EndedReason)
	}
}

// TestBackfillAbortsOnCollision builds the one corpus shape that can collide —
// two rows whose ids differ but whose CURRENT fields hash the same, which is
// what an in-place edit produces — and asserts the backfill refuses rather than
// skipping a row. A silent partial backfill is the failure this repo keeps
// catching; a failed one is recoverable, a half-done one is invisible.
func TestBackfillAbortsOnCollision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "collide.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := gdb.DB()
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, id := range []string{"id-alpha", "id-beta"} {
		if _, err := sqlDB.Exec(
			`INSERT INTO drawers (team_id,id,wing,room,source_file,chunk_index,content,entities,parent_id,filed_at,content_date,agent,topic,valid_to,superseded_by,ended_reason,ended_at,content_key)
			 VALUES ('t',?,'w','r','',0,'identical text','','','2026-01-01T00:00:00Z','','','','','','','','')`, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	svc := NewService(NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim)
	err = svc.repo.BackfillContentKeys(ctx)
	if err == nil {
		t.Fatal("the backfill succeeded on a corpus where two rows hash to one key; it must ABORT, " +
			"because skipping the row leaves a half-keyed corpus nothing reports")
	}
	if !strings.Contains(err.Error(), "id-alpha") && !strings.Contains(err.Error(), "id-beta") {
		t.Errorf("error %q names neither colliding row; an abort that does not say WHICH rows collided "+
			"cannot be acted on", err)
	}
}
