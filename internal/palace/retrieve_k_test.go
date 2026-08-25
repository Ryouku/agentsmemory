package palace

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestSearchRetrieveKWidensTheFetch is the SELECTION test for the retrieve-k
// floor, and it exists because driving productionRetrieveFloor alone would pass
// whether or not Search reads q.RetrieveK. The eval's shared pool already
// fetches k=50, so an Evaluate-based test would stay green if the new arm were
// unwired — the same trap TestProductionDeepArmActuallyFetchesDeeper names for
// the page-size arm.
//
// Search is called directly. The recording vector store sees the first k each
// call asks for, which is candidateKFor with the floor applied.
func TestSearchRetrieveKWidensTheFetch(t *testing.T) {
	svc := newTestService(t)
	const team = "team-retrieve-k"
	for i := range 20 {
		mustAddOne(t, svc, team, AddInput{
			Wing: "w", Room: "r",
			Content: fmt.Sprintf("memory number %d about retrieve width and paging", i),
		})
	}

	rec := &recordingVectors{VectorStore: svc.vectors}
	svc.vectors = rec
	ctx := t.Context()
	q := SearchQuery{Query: "retrieve width and paging", Wing: "w", Limit: DefaultSearchLimit, SkipTelemetry: true}

	formula := candidateKFor(DefaultSearchLimit, false, 0, 0)
	if formula != DefaultSearchLimit*hybridCandidateMultiplier {
		t.Fatalf("fixture: candidateKFor(%d, no reranker) = %d, want %d", DefaultSearchLimit, formula, DefaultSearchLimit*hybridCandidateMultiplier)
	}

	if _, err := svc.Search(ctx, team, q); err != nil {
		t.Fatalf("Search retrieve-k=0: %v", err)
	}
	if len(rec.ks) == 0 {
		t.Fatal("Search asked the vector store for nothing")
	}
	if rec.ks[0] != formula {
		t.Errorf("RetrieveK=0 first k = %d, want the formula %d; floors must not fire at the default", rec.ks[0], formula)
	}

	rec.ks = nil
	q.RetrieveK = ProductionRetrieveK
	if _, err := svc.Search(ctx, team, q); err != nil {
		t.Fatalf("Search retrieve-k=%d: %v", ProductionRetrieveK, err)
	}
	if len(rec.ks) == 0 {
		t.Fatal("Search with RetrieveK asked the vector store for nothing")
	}
	if rec.ks[0] != ProductionRetrieveK {
		t.Errorf("RetrieveK=%d first k = %d, want %d — Search is ignoring the floor", ProductionRetrieveK, rec.ks[0], ProductionRetrieveK)
	}

	rec.ks = nil
	q.RetrieveK = 3
	if _, err := svc.Search(ctx, team, q); err != nil {
		t.Fatalf("Search retrieve-k=3: %v", err)
	}
	if rec.ks[0] != formula {
		t.Errorf("RetrieveK=3 first k = %d, want formula %d — a floor must not shrink below candidateKFor", rec.ks[0], formula)
	}

	rec.ks = nil
	q.RetrieveK = 0
	svc.WithRetrieveK(ProductionRetrieveK)
	if _, err := svc.Search(ctx, team, q); err != nil {
		t.Fatalf("Search service retrieve-k=%d: %v", ProductionRetrieveK, err)
	}
	if rec.ks[0] != ProductionRetrieveK {
		t.Errorf("WithRetrieveK(%d) first k = %d, want %d — the process floor is unused", ProductionRetrieveK, rec.ks[0], ProductionRetrieveK)
	}
}

// TestRetrieveKArmKeepsDefaultPage pins the entire content of the difference
// between ArmProduction and ArmProductionRetrieve: same page, different fetch.
// An arm named retrieve-k=50 that asks for a page of 50 is a different question
// (page size, not retrieve width), and an arm that asks for retrieve-k=0 is a
// duplicate of ArmProduction under a misleading name.
func TestRetrieveKArmKeepsDefaultPage(t *testing.T) {
	if got := productionLimit(ArmProductionRetrieve); got != DefaultSearchLimit {
		t.Errorf("ArmProductionRetrieve asks for page %d, want DefaultSearchLimit (%d) — the page stays what agents get",
			got, DefaultSearchLimit)
	}
	if got := productionRetrieveFloor(ArmProduction); got != 0 {
		t.Errorf("ArmProduction retrieve floor = %d, want 0 (formula-only)", got)
	}
	if got := productionRetrieveFloor(ArmProductionDeep); got != 0 {
		t.Errorf("ArmProductionDeep retrieve floor = %d, want 0 (formula-only)", got)
	}
	if got := productionRetrieveFloor(ArmProductionRetrieve); got != ProductionRetrieveK {
		t.Errorf("ArmProductionRetrieve retrieve floor = %d, want %d", got, ProductionRetrieveK)
	}
	if productionRetrieveFloor(ArmProduction) == productionRetrieveFloor(ArmProductionRetrieve) {
		t.Fatal("both production arms ask for the same retrieve floor, so the retrieve-k row is a duplicate")
	}
	if want := "retrieve-k=" + strconv.Itoa(ProductionRetrieveK); !strings.Contains(string(ArmProductionRetrieve), want) {
		t.Errorf("%q does not name the floor it asks for (%s)", ArmProductionRetrieve, want)
	}
}

// TestWithRetrieveFloorsNeverShrinks drives the helper Search uses, so replacing
// `withRetrieveFloors(candidateKFor(...), ...)` with `candidateKFor(...)` fails
// the Search test above, and replacing the helper itself with `return k` fails
// here even if Search is later unwired.
func TestWithRetrieveFloorsNeverShrinks(t *testing.T) {
	if got := withRetrieveFloors(15, 0, 3, -1); got != 15 {
		t.Errorf("floors below the formula: got %d, want 15", got)
	}
	if got := withRetrieveFloors(15, 50); got != 50 {
		t.Errorf("floor above the formula: got %d, want 50", got)
	}
	if got := withRetrieveFloors(15); got != 15 {
		t.Errorf("no floors: got %d, want 15", got)
	}
}
