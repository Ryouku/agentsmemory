package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Admin operations over a team's palace: merge_wing (relabel a wing), delete_wing
// (purge one), and memories_filed_away (a recent-activity summary). The frozen
// sync (prune drawers whose on-disk source files vanished) and hook_settings
// (local Claude Code hook config) are intentionally absent — both are
// single-user-local concepts with no meaning on a multi-tenant server that has
// neither the user's filesystem nor per-team hooks, the same reason mine takes
// text rather than a server path.

// MergeWingResult reports a wing merge: the sources folded in, the target, and how
// many drawers and closets were relabeled.
type MergeWingResult struct {
	Sources []string `json:"sources"`
	Target  string   `json:"target"`
	Drawers int64    `json:"drawers_relabeled"`
	Closets int64    `json:"closets_relabeled"`
}

// MergeWing folds one or more source wings into a target, relabeling the `wing` of
// every drawer and closet in place (ids unchanged), the frozen merge_wing. The
// derived graph (hallways/tunnels) is NOT rebuilt here — call recompute_graph
// afterwards, as the frozen tool instructs. Vectors are not re-written: their
// payload wing is advisory (search filters on the drawer row's wing), so a merge
// needs no re-embedding. Idempotent: merging an already-merged wing is a no-op.
func (s *Service) MergeWing(ctx context.Context, teamID string, sources []string, target string) (MergeWingResult, error) {
	tgt, err := SanitizeName(target, "target")
	if err != nil {
		return MergeWingResult{}, err
	}
	clean := make([]string, 0, len(sources))
	for _, src := range sources {
		c, err := SanitizeName(src, "source")
		if err != nil {
			return MergeWingResult{}, err
		}
		if c != tgt { // merging a wing into itself is a no-op; drop it
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return MergeWingResult{Sources: clean, Target: tgt}, nil
	}

	drawers, err := s.repo.RelabelDrawerWing(ctx, teamID, clean, tgt)
	if err != nil {
		return MergeWingResult{}, fmt.Errorf("relabel drawers: %w", err)
	}
	closets, err := s.repo.RelabelClosetWing(ctx, teamID, clean, tgt)
	if err != nil {
		return MergeWingResult{}, fmt.Errorf("relabel closets: %w", err)
	}
	return MergeWingResult{Sources: clean, Target: tgt, Drawers: drawers, Closets: closets}, nil
}

// ErrConfirmMismatch is returned when a wing delete was not confirmed by echoing
// the wing's own name back. It is a distinct sentinel so a caller can tell "you
// must confirm" apart from "that input was malformed" and prompt accordingly.
var ErrConfirmMismatch = errors.New("confirmation does not match the wing name")

// deleteBatch bounds how many ids are removed per round, keeping the `IN (...)`
// list inside SQLite's parameter limit no matter how large the wing is.
const deleteBatch = 500

// DeleteWingResult reports what a wing delete removed — or, from CountWing, what
// one would remove.
type DeleteWingResult struct {
	Wing     string `json:"wing"`
	Drawers  int64  `json:"drawers_deleted"`
	Closets  int64  `json:"closets_deleted"`
	Hallways int64  `json:"hallways_deleted"`
	Tunnels  int64  `json:"tunnels_deleted"`
}

// DeleteWing permanently removes one wing: every drawer and closet filed in it
// (with their vectors), every hallway derived within it, and every tunnel with an
// endpoint in it. It is the counterpart to a palace where wings are derived rather
// than declared — a wing exists because rows carry its name, so deleting those
// rows is the only thing "deleting a wing" can mean.
//
// confirm must equal the wing name. Nothing about a delete is recoverable and its
// size is unbounded, so the guard lives here rather than in each caller: every
// surface refuses identically, and a typo'd or hallucinated wing name fails closed
// instead of emptying a wing nobody meant to touch. Export it first
// (`agentsmemory wing export`) if it might be wanted back.
//
// Unlike MergeWing, this needs no recompute_graph afterwards: a merge relabels
// drawers and leaves the derived graph describing a layout that no longer holds,
// whereas a delete removes exactly the hallways of this wing and exactly the
// tunnels that touched it. Every hallway and tunnel left standing is still backed
// by drawers that still exist.
//
// Knowledge-graph facts are deliberately untouched: they are team-global rather
// than wing-scoped, so purging them here would delete other wings' facts — the
// same boundary a wing bundle draws when it exports.
func (s *Service) DeleteWing(ctx context.Context, teamID, wing, confirm string) (DeleteWingResult, error) {
	name, err := SanitizeName(wing, "wing")
	if err != nil {
		return DeleteWingResult{}, err
	}

	// Count first so a refusal can name the blast radius. An operator who mistyped
	// learns what they nearly deleted, which is the whole value of the guard.
	contents, err := s.repo.CountWing(ctx, teamID, name)
	if err != nil {
		return DeleteWingResult{}, fmt.Errorf("count wing: %w", err)
	}
	// Trimmed, because SanitizeName trimmed the wing name as well: a pasted
	// "doomed " selects the same wing, so it has to confirm the same wing. The
	// guard is against naming the wrong wing, not against sloppy whitespace.
	if strings.TrimSpace(confirm) != name {
		return DeleteWingResult{}, fmt.Errorf(
			"%w: refusing to delete wing %q (%d drawers, %d closets, %d hallways, %d tunnels) — pass confirm=%q to proceed",
			ErrConfirmMismatch, name, contents.Drawers, contents.Closets, contents.Hallways, contents.Tunnels, name)
	}

	res := DeleteWingResult{Wing: name}

	// Take the head of the wing repeatedly rather than paging by offset: the set
	// shrinks as we delete, so a moving offset would step over rows and leave them.
	for {
		ids, err := s.repo.DrawerIDsByWing(ctx, teamID, name, deleteBatch)
		if err != nil {
			return res, fmt.Errorf("list wing drawers: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		// Rows before vectors, exactly as Delete does: the authoritative record goes
		// first, so a failure here leaves an orphaned vector the next search
		// harmlessly skips rather than a drawer that search can still surface.
		n, err := s.repo.DeleteDrawersByIDs(ctx, teamID, ids)
		if err != nil {
			return res, fmt.Errorf("delete wing drawers: %w", err)
		}
		if err := s.vectors.Delete(ctx, teamID, ids); err != nil {
			return res, fmt.Errorf("delete wing drawer vectors: %w", err)
		}
		res.Drawers += n
	}

	for {
		ids, err := s.repo.ClosetIDsByWing(ctx, teamID, name, deleteBatch)
		if err != nil {
			return res, fmt.Errorf("list wing closets: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		n, err := s.repo.DeleteClosetsByIDs(ctx, teamID, ids)
		if err != nil {
			return res, fmt.Errorf("delete wing closets: %w", err)
		}
		if err := s.vectors.Delete(ctx, closetNamespace(teamID), ids); err != nil {
			return res, fmt.Errorf("delete wing closet vectors: %w", err)
		}
		res.Closets += n
	}

	if res.Hallways, err = s.repo.DeleteWingHallways(ctx, teamID, name); err != nil {
		return res, fmt.Errorf("delete wing hallways: %w", err)
	}
	// A tunnel spans two wings, so this also removes links whose far endpoint is a
	// wing that survives. That is correct rather than collateral: a tunnel cannot
	// outlive either of the places it connects.
	if res.Tunnels, err = s.repo.DeleteWingTunnels(ctx, teamID, name); err != nil {
		return res, fmt.Errorf("delete wing tunnels: %w", err)
	}
	return res, nil
}

// FiledAwayResult is the memories_filed_away summary: how much a team has filed,
// when it last filed, and the breadth of its palace.
type FiledAwayResult struct {
	Count       int64  `json:"count"`
	Wings       int64  `json:"wings"`
	Rooms       int64  `json:"rooms"`
	LastFiledAt string `json:"last_filed_at,omitempty"`
	Message     string `json:"message"`
}

// MemoriesFiledAway summarises what a team has stored — total drawers, distinct
// wings and rooms, and the most recent filing. It is the SaaS reading of the
// frozen checkpoint-acknowledge tool: rather than a local hook-state file, it
// reports the team's actual filed memory at a glance.
func (s *Service) MemoriesFiledAway(ctx context.Context, teamID string) (FiledAwayResult, error) {
	count, lastFiledAt, wings, rooms, err := s.repo.FiledAwaySummary(ctx, teamID)
	if err != nil {
		return FiledAwayResult{}, err
	}
	msg := fmt.Sprintf("%d memories filed across %d wings and %d rooms", count, wings, rooms)
	if count == 0 {
		msg = "No memories filed yet"
	}
	return FiledAwayResult{Count: count, Wings: wings, Rooms: rooms, LastFiledAt: lastFiledAt, Message: msg}, nil
}

// RelabelDrawerWing moves every drawer in any of the source wings to the target
// wing for a team, returning how many rows changed. Ids are unchanged.
func (r *Repo) RelabelDrawerWing(ctx context.Context, teamID string, sources []string, target string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND wing IN ?", teamID, sources).
		Update("wing", target)
	return res.RowsAffected, res.Error
}

// RelabelClosetWing is the closet half of a wing merge.
func (r *Repo) RelabelClosetWing(ctx context.Context, teamID string, sources []string, target string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&closetRow{}).
		Where("team_id = ? AND wing IN ?", teamID, sources).
		Update("wing", target)
	return res.RowsAffected, res.Error
}

// CountWing reports what a wing holds: the same four numbers a delete returns, so
// a refused delete can state its blast radius without having removed anything.
func (r *Repo) CountWing(ctx context.Context, teamID, wing string) (DeleteWingResult, error) {
	res := DeleteWingResult{Wing: wing}
	inWing := func(model any, count *int64) error {
		return r.db.WithContext(ctx).Model(model).
			Where("team_id = ? AND wing = ?", teamID, wing).Count(count).Error
	}
	if err := inWing(&drawerRow{}, &res.Drawers); err != nil {
		return DeleteWingResult{}, err
	}
	if err := inWing(&closetRow{}, &res.Closets); err != nil {
		return DeleteWingResult{}, err
	}
	if err := inWing(&hallwayRow{}, &res.Hallways); err != nil {
		return DeleteWingResult{}, err
	}
	// Tunnels are the exception to inWing: they carry two wings, not one.
	err := r.db.WithContext(ctx).Model(&tunnelRow{}).
		Where("team_id = ? AND (source_wing = ? OR target_wing = ?)", teamID, wing, wing).
		Count(&res.Tunnels).Error
	return res, err
}

// DrawerIDsByWing returns up to limit ids of drawers filed in a wing. It is the
// read half of a batched purge, which re-reads the head of the wing after each
// delete rather than advancing an offset over a set that is shrinking beneath it.
func (r *Repo) DrawerIDsByWing(ctx context.Context, teamID, wing string, limit int) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND wing = ?", teamID, wing).
		Order("id ASC").Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteDrawersByIDs removes the named drawer rows within a team, returning how
// many were deleted. The caller drops the matching vectors.
func (r *Repo) DeleteDrawersByIDs(ctx context.Context, teamID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Delete(&drawerRow{})
	return res.RowsAffected, res.Error
}

// ClosetIDsByWing is the closet twin of DrawerIDsByWing.
func (r *Repo) ClosetIDsByWing(ctx context.Context, teamID, wing string, limit int) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&closetRow{}).
		Where("team_id = ? AND wing = ?", teamID, wing).
		Order("id ASC").Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteClosetsByIDs is the closet twin of DeleteDrawersByIDs.
func (r *Repo) DeleteClosetsByIDs(ctx context.Context, teamID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Delete(&closetRow{})
	return res.RowsAffected, res.Error
}

// DeleteWingHallways removes every hallway derived within a wing, returning how
// many rows went. A hallway lives inside one wing, so this is an exact purge.
func (r *Repo) DeleteWingHallways(ctx context.Context, teamID, wing string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("team_id = ? AND wing = ?", teamID, wing).
		Delete(&hallwayRow{})
	return res.RowsAffected, res.Error
}

// DeleteWingTunnels removes every tunnel with an endpoint in a wing — the same
// predicate ListTunnels uses to select them, so what a caller can see for a wing
// is exactly what a delete takes.
func (r *Repo) DeleteWingTunnels(ctx context.Context, teamID, wing string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("team_id = ? AND (source_wing = ? OR target_wing = ?)", teamID, wing, wing).
		Delete(&tunnelRow{})
	return res.RowsAffected, res.Error
}

// FiledAwaySummary returns a team's drawer count, most recent filing time, and the
// number of distinct wings and rooms — the numbers behind memories_filed_away.
func (r *Repo) FiledAwaySummary(ctx context.Context, teamID string) (count int64, lastFiledAt string, wings, rooms int64, err error) {
	base := func() *gorm.DB { return r.db.WithContext(ctx).Model(&drawerRow{}).Where("team_id = ?", teamID) }
	if err = base().Count(&count).Error; err != nil {
		return
	}
	if err = base().Select("COALESCE(MAX(filed_at), '')").Scan(&lastFiledAt).Error; err != nil {
		return
	}
	if err = base().Distinct("wing").Count(&wings).Error; err != nil {
		return
	}
	err = base().Distinct("room").Count(&rooms).Error
	return
}
