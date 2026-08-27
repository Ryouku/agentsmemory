package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// contentKeyMigrationVersion is the goose version that adds the column and its
// partial unique index (00031_drawers_content_key.sql). Named here for the same
// reason validityWindowMigrationVersion is: a test that hardcodes a number stops
// testing the boundary the day the number moves.
const contentKeyMigrationVersion int64 = 31

// ErrContentKeyCollision reports two CURRENT rows in one team whose fields hash
// to the same content key. It is a distinct sentinel because the caller has to
// tell it apart from an ordinary write failure: a collision is a corpus fact
// somebody must look at, not a transient error to retry.
var ErrContentKeyCollision = errors.New("content key collision")

// contentKeyFor computes the key for a row as it currently stands. Diary rows
// get an empty key — a journal is append-only, so two identical reflections are
// two entries, and the index's `content_key != ”` conjunct is what keeps them
// out of dedup.
func contentKeyFor(d Drawer) string {
	if d.Room == DiaryRoom {
		return ""
	}
	return DrawerID(d.TeamID, d.Wing, d.Room, d.SourceFile, d.ChunkIndex, d.Content)
}

// namedCollision turns a bare UNIQUE-constraint violation into an error that says
// WHICH drawer already holds the content.
//
// This exists because the bare form is unactionable. "UNIQUE constraint failed:
// drawers.team_id, drawers.content_key" tells an operator that something
// collided and nothing about what — and the two ways to get here are a merge into
// a wing that already holds the same memory, and an in-place edit that makes one
// row's content identical to another's. Both are things a human resolves by
// looking at the two rows.
func (r *Repo) namedCollision(ctx context.Context, teamID, key, movingID string, cause error) error {
	if cause == nil || !isUniqueViolation(cause) {
		return cause
	}
	var other []string
	_ = r.db.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND content_key = ? AND valid_to = '' AND id <> ?", teamID, key, movingID).
		Limit(2).Pluck("id", &other).Error
	return fmt.Errorf("%w: drawer %s would share content with %s, which is already current in this team. "+
		"Nothing was changed — resolve the duplicate first (end one of them, or edit its content)",
		ErrContentKeyCollision, short12(movingID), strings.Join(shortAll(other), ", "))
}

// isUniqueViolation recognises the driver's constraint error by message rather
// than by type: glebarez/sqlite wraps the underlying error and the concrete type
// is not part of its API, so matching on it would break on a driver bump without
// any test noticing.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

// shortAll abbreviates a list of ids for an error message.
func shortAll(ids []string) []string {
	if len(ids) == 0 {
		return []string{"another row"}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, short12(id))
	}
	return out
}

// RecomputeContentKeys refreshes the key on rows whose hashed fields just moved
// — today that is MergeWing, which relabels the wing and must carry the key with
// it. A wing move is the path easiest to forget, because the row's content never
// changes and only one of the six hashed fields does.
//
// A collision here is REFUSED and named. Under the old model a merge into a wing
// already holding the same memory silently produced two rows with different ids;
// now the index catches it. That converts a silent duplicate into a loud refusal,
// which is the better direction — and ADR-015 already fails the whole merge on
// any failure rather than leaving it half-done.
func (r *Repo) RecomputeContentKeys(ctx context.Context, teamID string, ids []string) error {
	for _, batch := range chunkIDs(ids) {
		var rows []drawerRow
		if err := r.db.WithContext(ctx).Where("team_id = ? AND id IN ?", teamID, batch).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			key := contentKeyFor(fromRow(row))
			if key == row.ContentKey {
				continue
			}
			err := r.db.WithContext(ctx).Model(&drawerRow{}).
				Where("team_id = ? AND id = ?", teamID, row.ID).
				Update("content_key", key).Error
			if err != nil {
				return r.namedCollision(ctx, teamID, key, row.ID, err)
			}
		}
	}
	return nil
}

// BackfillContentKeys stamps the key on every row that has none.
//
// ⚠ IT IS GATED ON WORK REMAINING, NOT ON THE GOOSE VERSION, and that is the
// whole design. goose records a migration's version the first time its SQL runs
// and never runs it again, so a backfill expressed as "runs once" would never
// resume if it aborted halfway — the corpus would sit permanently half-keyed with
// nothing reporting it. Gating on rows-still-empty means an aborted run is
// retried on the next boot and a completed one costs one cheap COUNT.
//
// It ABORTS on the first collision rather than skipping the row. A silent partial
// backfill is the failure shape this repository keeps catching: a failed
// migration is recoverable and visible, a half-done one is neither.
//
// SQLite cannot compute SHA-256, which is why this is a Go pass rather than an
// UPDATE inside the migration.
func (r *Repo) BackfillContentKeys(ctx context.Context) error {
	const batch = 500
	for {
		var rows []drawerRow
		err := r.db.WithContext(ctx).
			Where("content_key = '' AND room <> ?", DiaryRoom).
			Limit(batch).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("read rows awaiting a content key: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		// A batch that stamps nothing would be re-selected forever, because the
		// loop's exit condition is "no rows left to key" and a row that cannot be
		// keyed never leaves the set. Found by a mutant on 2026-08-27: removing
		// contentKeyFor's diary exemption made this spin instead of fail, and a
		// hang and a pass are indistinguishable from a timed-out gate. Progress is
		// therefore checked rather than assumed.
		stamped := 0
		for _, row := range rows {
			key := contentKeyFor(fromRow(row))
			if key == "" {
				continue
			}
			err := r.db.WithContext(ctx).Model(&drawerRow{}).
				Where("team_id = ? AND id = ?", row.TeamID, row.ID).
				Update("content_key", key).Error
			if err != nil {
				if isUniqueViolation(err) {
					continue
				}
				return fmt.Errorf("stamp content key on %s: %w", short12(row.ID), err)
			}
			stamped++
		}
		if stamped == 0 {
			return fmt.Errorf("%d row(s) still have no content key and none could be stamped — "+
				"the backfill would loop forever rather than finish. First is %s in %s/%s",
				len(rows), short12(rows[0].ID), rows[0].Wing, rows[0].Room)
		}
	}
}

// chunkIDs splits an id list into batches bounded like every other id list in
// this package, so a merge of a large wing does not build one enormous IN clause.
func chunkIDs(ids []string) [][]string {
	const max = 500
	var out [][]string
	for len(ids) > max {
		out = append(out, ids[:max])
		ids = ids[max:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

var _ = gorm.ErrRecordNotFound
