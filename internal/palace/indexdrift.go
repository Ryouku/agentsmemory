package palace

import (
	"context"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// driftBatch bounds how many ids are looked up per round, keeping the `IN (...)`
// list inside SQLite's parameter limit and the Qdrant request body small — the
// same reason deleteBatch exists.
const driftBatch = 500

// DriftedPoint is one stored point whose payload wing disagrees with the drawer
// it indexes.
//
// It names the STORE because there are two and they fail differently: a stale
// index makes a memory unreachable now, and a stale source of truth makes it
// unreachable again after the next sync. A repair that fixed one and not the
// other would look complete from either side alone.
type DriftedPoint struct {
	Store    string `json:"store"`
	DrawerID string `json:"drawer_id"`
	Indexed  string `json:"indexed_wing"`
	Actual   string `json:"actual_wing"`
}

// DriftReport is what IndexDrift found. Drifted is sorted for a stable report.
type DriftReport struct {
	Checked int            `json:"checked"`
	Drifted []DriftedPoint `json:"drifted"`
}

// Clean reports whether every point agrees with its drawer.
func (r DriftReport) Clean() bool { return len(r.Drifted) == 0 }

// splitStore is a VectorStore that pairs a durable store with a search index.
// Both copies of a payload must agree with the rows, so a check that could see
// only one of them would report clean while the other was wrong.
type splitStore interface {
	Halves() (store.SourceOfTruth, store.VectorStore)
}

// IndexDrift reports every stored point whose payload wing no longer matches the
// wing its drawer is filed in.
//
// This is not a hypothetical consistency check. A scoped search filters at the
// INDEX, on the payload — Search passes the wing to the vector store, and the
// drawer-row comparison that follows can only remove candidates, never add one
// back — so a point whose payload says the wrong wing is unreachable from the
// wing it actually lives in. Measured 2026-08-21 on a live palace: 13 of 359
// points had drifted that way after wing merges, in BOTH stores, and the
// memories were returned only by an unscoped search.
//
// It reads and never writes. The repair is a separate operation on purpose: a
// checker that also fixes cannot be trusted to report honestly about its own
// fixes, and this report is the acceptance for the code that does the fixing.
func (s *Service) IndexDrift(ctx context.Context, teamID string) (DriftReport, error) {
	var report DriftReport

	// Name each store so a reader can tell which half is wrong.
	stores := []struct {
		name string
		vs   store.VectorStore
	}{}
	if split, ok := s.vectors.(splitStore); ok {
		sot, index := split.Halves()
		stores = append(stores, struct {
			name string
			vs   store.VectorStore
		}{"source of truth", sot}, struct {
			name string
			vs   store.VectorStore
		}{"index", index})
	} else {
		stores = append(stores, struct {
			name string
			vs   store.VectorStore
		}{"index", s.vectors})
	}

	wings, err := s.repo.DrawerWings(ctx, teamID)
	if err != nil {
		return DriftReport{}, fmt.Errorf("load drawer wings: %w", err)
	}
	report.Checked = len(wings)

	ids := make([]string, 0, len(wings))
	for id := range wings {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic batching, so a truncated run is repeatable

	for _, st := range stores {
		for start := 0; start < len(ids); start += driftBatch {
			end := start + driftBatch
			if end > len(ids) {
				end = len(ids)
			}
			points, err := st.vs.PointsByIDs(ctx, teamID, ids[start:end])
			if err != nil {
				return DriftReport{}, fmt.Errorf("read points from the %s: %w", st.name, err)
			}
			for _, p := range points {
				indexed, _ := p.Payload["wing"].(string)
				actual := wings[p.ID]
				if indexed != actual {
					report.Drifted = append(report.Drifted, DriftedPoint{
						Store: st.name, DrawerID: p.ID, Indexed: indexed, Actual: actual,
					})
				}
			}
		}
	}
	sort.Slice(report.Drifted, func(a, b int) bool {
		x, y := report.Drifted[a], report.Drifted[b]
		if x.Store != y.Store {
			return x.Store < y.Store
		}
		return x.DrawerID < y.DrawerID
	})
	return report, nil
}
