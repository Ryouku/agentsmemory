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
	// Missing marks a drawer the store holds NO point for. It is a different and
	// worse fault than a wrong label: a mislabelled memory answers the wrong wing,
	// an absent one answers nothing at all.
	Missing bool `json:"missing,omitempty"`
}

// DriftReport is what IndexDrift found. Drifted is sorted for a stable report and
// bounded: a fully drifted palace must produce a report an operator can read and
// a process can hold in memory, so the count is exact and the listing is a sample.
type DriftReport struct {
	Checked int            `json:"checked"`
	Total   int            `json:"total_drifted"`
	Drifted []DriftedPoint `json:"drifted"`
	// Pending is how many drawers are legitimately awaiting their first embedding
	// — a row exists, no vector does yet, and that is a queue rather than a fault.
	// Counted rather than reported, so a busy palace does not look broken.
	Pending int `json:"pending_embedding"`
}

// driftSample bounds the listing. The COUNT is always exact; only the listing is
// capped, because a palace whose index was rebuilt into the wrong shape would
// otherwise print a line per memory.
const driftSample = 50

// Clean reports whether every point agrees with its drawer.
func (r DriftReport) Clean() bool { return r.Total == 0 }

// Truncated reports whether the listing is a sample of a larger set.
func (r DriftReport) Truncated() bool { return r.Total > len(r.Drifted) }

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

	wings, pending, err := s.repo.DrawerWings(ctx, teamID)
	if err != nil {
		return DriftReport{}, fmt.Errorf("load drawer wings: %w", err)
	}
	report.Checked = len(wings)
	report.Pending = len(pending)

	ids := make([]string, 0, len(wings))
	for id := range wings {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic batching, so a truncated run is repeatable

	record := func(d DriftedPoint) {
		report.Total++
		if len(report.Drifted) < driftSample {
			report.Drifted = append(report.Drifted, d)
		}
	}

	for _, st := range stores {
		for start := 0; start < len(ids); start += driftBatch {
			end := start + driftBatch
			if end > len(ids) {
				end = len(ids)
			}
			batch := ids[start:end]
			points, err := st.vs.PointsByIDs(ctx, teamID, batch)
			if err != nil {
				return DriftReport{}, fmt.Errorf("read points from the %s: %w", st.name, err)
			}
			// Index what came back, so a point the store did NOT return can be
			// noticed. Reading only the returned points made an omission read as
			// agreement: a memory the index had lost entirely — unreachable by any
			// search, not merely by a scoped one — reported clean.
			seen := make(map[string]string, len(points))
			for _, p := range points {
				if _, asked := wings[p.ID]; !asked {
					// A point the caller did not ask for. Comparing it against an
					// absent row would invent drift out of a driver's own bug.
					continue
				}
				indexed, _ := p.Payload["wing"].(string)
				seen[p.ID] = indexed
			}
			for _, id := range batch {
				indexed, ok := seen[id]
				if !ok {
					record(DriftedPoint{Store: st.name, DrawerID: id, Actual: wings[id], Missing: true})
					continue
				}
				if indexed != wings[id] {
					record(DriftedPoint{Store: st.name, DrawerID: id, Indexed: indexed, Actual: wings[id]})
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
