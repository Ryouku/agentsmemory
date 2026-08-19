package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// This file answers the question drawer counts cannot: is the memory being USED,
// and does it answer? A palace that grows while every recall comes back empty is
// a filing cabinet nobody opens, and from the inside those two states look
// identical — both show more drawers every day.
//
// Recording is best-effort by design (see recordSearch): a statistics write must
// never be able to fail a recall.

// searchEventRow is the gorm view of one recorded recall.
type searchEventRow struct {
	ID         string  `gorm:"column:id;primaryKey"`
	TeamID     string  `gorm:"column:team_id"`
	Wing       string  `gorm:"column:wing"`
	Room       string  `gorm:"column:room"`
	Query      string  `gorm:"column:query"`
	Candidates int     `gorm:"column:candidates"`
	Hits       int     `gorm:"column:hits"`
	TopScore   float64 `gorm:"column:top_score"`
	Reranked   int     `gorm:"column:reranked"`
	CreatedAt  string  `gorm:"column:created_at"`
}

// TableName pins the table name so gorm does not pluralise the struct name.
func (searchEventRow) TableName() string { return "search_events" }

// WingRecall is one wing's recall record over a window: how often it was asked,
// how often it answered, and how much it holds.
//
// Answered is the number with at least one hit. It is deliberately the headline
// rather than an average score: a score is only comparable within one query,
// while "did this wing have anything to say" compares across all of them.
type WingRecall struct {
	Wing      string
	Searches  int
	Answered  int
	AvgTop    float64 // mean top-hit score across ANSWERED searches
	Drawers   int     // drawers currently filed in this wing
	LastUsed  string  // most recent search against this wing, RFC3339 ("" if none)
	LastFiled string  // most recent drawer filed into it ("" if none)
}

// AnsweredPct is the share of searches that returned something, 0 when the wing
// was never searched. This is the number to watch over weeks: it should climb as
// a wing accumulates the memories its questions are actually about.
func (w WingRecall) AnsweredPct() int {
	if w.Searches == 0 {
		return 0
	}
	return int(float64(w.Answered)/float64(w.Searches)*100 + 0.5)
}

// RecallStats is the whole picture for a window: per-wing recall, plus the
// queries that found nothing.
type RecallStats struct {
	Since    string
	Searches int
	Answered int
	Writes   int // drawers filed in the window, all wings
	Wings    []WingRecall
	// Unanswered are recent queries that returned no hits, newest first. These are
	// the actionable half of the report: each one names a memory the team looked
	// for and does not have.
	Unanswered []string
}

// AnsweredPct is the share of all searches in the window that returned something.
func (r RecallStats) AnsweredPct() int {
	if r.Searches == 0 {
		return 0
	}
	return int(float64(r.Answered)/float64(r.Searches)*100 + 0.5)
}

// recordSearch files one recall event. Errors are swallowed on purpose: this is
// measurement, and measurement that can break the thing it measures is worse than
// no measurement. The caller therefore ignores the return.
func (r *Repo) recordSearch(ctx context.Context, e searchEventRow) {
	if e.ID == "" {
		e.ID = randomID()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_ = r.db.WithContext(ctx).Create(&e).Error
}

// randomID returns a short unique id for an event row.
func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A clock-based fallback keeps recording alive if the entropy source is
		// unavailable; a duplicate id here costs one lost statistic, nothing more.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// RecallStats aggregates a team's recall over the last `since` duration.
//
// Wings with no searches still appear when they hold drawers: a wing that is
// written to and never read is exactly the pattern worth seeing, and it would be
// invisible in a search-only report.
func (s *Service) RecallStats(ctx context.Context, teamID string, since time.Duration, unansweredLimit int) (RecallStats, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	if unansweredLimit <= 0 {
		unansweredLimit = 10
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	out := RecallStats{Since: cutoff}

	type agg struct {
		Wing     string
		Searches int
		Answered int
		SumTop   float64
		LastUsed string
	}
	var rows []agg
	if err := s.repo.db.WithContext(ctx).
		Model(&searchEventRow{}).
		Select("wing, COUNT(*) AS searches, SUM(CASE WHEN hits > 0 THEN 1 ELSE 0 END) AS answered, "+
			"SUM(CASE WHEN hits > 0 THEN top_score ELSE 0 END) AS sum_top, MAX(created_at) AS last_used").
		Where("team_id = ? AND created_at >= ?", teamID, cutoff).
		Group("wing").
		Scan(&rows).Error; err != nil {
		return RecallStats{}, fmt.Errorf("aggregate search events: %w", err)
	}

	byWing := map[string]*WingRecall{}
	for _, a := range rows {
		wing := a.Wing
		if wing == "" {
			// An unscoped search belongs to no wing; it is still counted in the
			// totals below, and named so the report does not silently drop it.
			wing = "(unscoped)"
		}
		w := &WingRecall{Wing: wing, Searches: a.Searches, Answered: a.Answered, LastUsed: a.LastUsed}
		if a.Answered > 0 {
			w.AvgTop = a.SumTop / float64(a.Answered)
		}
		byWing[wing] = w
		out.Searches += a.Searches
		out.Answered += a.Answered
	}

	// Drawer counts and last-filed per wing, so a wing that is only written to
	// still shows up.
	type wingCount struct {
		Wing      string
		Drawers   int
		LastFiled string
	}
	var counts []wingCount
	if err := s.repo.db.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, COUNT(*) AS drawers, MAX(filed_at) AS last_filed").
		Where("team_id = ?", teamID).
		Group("wing").
		Scan(&counts).Error; err != nil {
		return RecallStats{}, fmt.Errorf("count drawers per wing: %w", err)
	}
	for _, c := range counts {
		w, ok := byWing[c.Wing]
		if !ok {
			w = &WingRecall{Wing: c.Wing}
			byWing[c.Wing] = w
		}
		w.Drawers, w.LastFiled = c.Drawers, c.LastFiled
	}

	// Writes in the window, across all wings.
	var writes int64
	if err := s.repo.db.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND filed_at >= ?", teamID, cutoff).
		Count(&writes).Error; err != nil {
		return RecallStats{}, fmt.Errorf("count writes: %w", err)
	}
	out.Writes = int(writes)

	for _, w := range byWing {
		out.Wings = append(out.Wings, *w)
	}
	// Busiest wings first, then the merely-large ones — the report is read top
	// down and the first lines should be where the work happened.
	sortWings(out.Wings)

	var unanswered []searchEventRow
	if err := s.repo.db.WithContext(ctx).
		Model(&searchEventRow{}).
		Where("team_id = ? AND created_at >= ? AND hits = 0 AND query <> ''", teamID, cutoff).
		Order("created_at DESC").
		Limit(unansweredLimit).
		Find(&unanswered).Error; err != nil {
		return RecallStats{}, fmt.Errorf("list unanswered searches: %w", err)
	}
	for _, u := range unanswered {
		out.Unanswered = append(out.Unanswered, strings.TrimSpace(u.Query))
	}
	return out, nil
}

// sortWings orders wings by searches, then drawers, then name — descending on the
// two numbers so the busiest wing leads, with the name as a stable tiebreak.
func sortWings(ws []WingRecall) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && less(ws[j], ws[j-1]); j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}

func less(a, b WingRecall) bool {
	if a.Searches != b.Searches {
		return a.Searches > b.Searches
	}
	if a.Drawers != b.Drawers {
		return a.Drawers > b.Drawers
	}
	return a.Wing < b.Wing
}

// compile-time guard that the stats queries keep using the gorm handle they were
// written against.
var _ = (*gorm.DB)(nil)
