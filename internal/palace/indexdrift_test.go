package palace

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// TestIndexDriftIsSilentOnACleanPalace: a check that fires on a healthy palace
// is one people learn to skip, so this pins the negative case first.
func TestIndexDriftIsSilentOnACleanPalace(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-clean"

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the rerank pool ships at ten"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_alpha", Room: "diary", Content: "a memory in another wing entirely"})

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Checked == 0 {
		t.Fatal("the check examined nothing, so it cannot have found nothing")
	}
	if !report.Clean() {
		t.Errorf("a freshly written palace reports drift: %+v", report.Drifted)
	}
}

// TestIndexDriftIsFound: a payload whose wing no longer matches its drawer is
// reported, and reported against the store it is wrong in.
//
// The relabel here is exactly what MergeWing does — the drawer row moves and the
// stored payload does not — which is how 13 of one live palace's 359 points came
// to be unreachable from the wing they were filed in.
func TestIndexDriftIsFound(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift"

	d := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme-legacy", Room: "decisions",
		Content: "a decision filed before the wings were merged"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a decision filed after"})

	// Relabel the ROW only, leaving every stored payload behind — the merge as it
	// behaves today.
	if _, err := svc.repo.RelabelDrawerWing(ctx, team, []string{"wing_acme-legacy"}, "wing_acme"); err != nil {
		t.Fatalf("relabel: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Clean() {
		t.Fatal("a drawer was relabelled and every stored payload still says the old wing, " +
			"and the check reports clean")
	}
	for _, dp := range report.Drifted {
		if dp.DrawerID != d.ID {
			t.Errorf("reported drift on %q, which was not relabelled", dp.DrawerID)
		}
		if dp.Indexed != "wing_acme-legacy" || dp.Actual != "wing_acme" {
			t.Errorf("drift reported as %q -> %q, want %q -> %q",
				dp.Indexed, dp.Actual, "wing_acme-legacy", "wing_acme")
		}
		if dp.Store == "" {
			t.Error("the drift does not name which store it is in; a repair that fixed only one " +
				"would look complete from the other side")
		}
	}
}

// TestIndexDriftReadsEveryStore: the test service is a single store, so this
// pins the two-store case explicitly — a Hybrid must be checked on BOTH halves,
// because the index is what a scoped search filters on and the source of truth
// is what the next sync will replay over it.
func TestIndexDriftReadsEveryStore(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-split"

	sot := &recordingStore{VectorStore: svc.vectors, name: "sot"}
	index := &recordingStore{VectorStore: svc.vectors, name: "index"}
	svc.vectors = &fakeSplit{VectorStore: svc.vectors, sot: sot, index: index}

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "one memory"})
	if _, err := svc.IndexDrift(ctx, team); err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if sot.reads == 0 {
		t.Error("the source of truth was never read; a drift it alone carries survives the check " +
			"and comes back on the next sync")
	}
	if index.reads == 0 {
		t.Error("the index was never read; that is the copy a scoped search actually filters on")
	}
}

// fakeSplit presents one store as two halves, so the split path is exercised
// without standing up a second backend.
type fakeSplit struct {
	store.VectorStore
	sot   store.SourceOfTruth
	index store.VectorStore
}

func (f *fakeSplit) Halves() (store.SourceOfTruth, store.VectorStore) { return f.sot, f.index }

// recordingStore counts PointsByIDs calls and otherwise delegates.
type recordingStore struct {
	store.VectorStore
	name  string
	reads int
}

func (r *recordingStore) PointsByIDs(ctx context.Context, ns string, ids []string) ([]store.Point, error) {
	r.reads++
	return r.VectorStore.PointsByIDs(ctx, ns, ids)
}

func (r *recordingStore) AllPoints(ctx context.Context, ns string) ([]store.Point, error) {
	if sot, ok := r.VectorStore.(store.SourceOfTruth); ok {
		return sot.AllPoints(ctx, ns)
	}
	return nil, nil
}

func (r *recordingStore) Namespaces(ctx context.Context) ([]string, error) {
	if sot, ok := r.VectorStore.(store.SourceOfTruth); ok {
		return sot.Namespaces(ctx)
	}
	return nil, nil
}
