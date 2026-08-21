package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestGraphReportCountsWhatItClaims: the columns must mean what their headers
// say, on a projection whose answer is known by hand.
//
// A projection is read once and acted on. If "drawers with two or more entities"
// silently counts drawers with one, the decision it feeds is taken on a number
// nobody can check.
func TestGraphReportCountsWhatItClaims(t *testing.T) {
	report := palace.GraphReport{
		Drawers: 10, WithAny: 6, WithTwo: 2, Hallways: 3,
		Wings: []palace.WingGraphPotential{
			{Wing: "wing_acme", Drawers: 7, WithAny: 5, WithTwo: 2, Hallways: 3, TopEntities: []string{"Qdrant", "Ollama"}},
			{Wing: "wing_alpha", Drawers: 3, WithAny: 1, WithTwo: 0},
		},
	}
	var buf bytes.Buffer
	if err := reportGraph(&buf, report); err != nil {
		t.Fatalf("reportGraph: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"wing_acme", "wing_alpha", "TOTAL", "Qdrant", "Ollama"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report never mentions %q:\n%s", want, out)
		}
	}
	// 2 of 10 is 20.0%, which is exactly the bar — and exactly the bar CLEARS it,
	// because the bar is stated as a minimum. A boundary that silently fails is a
	// different decision procedure from the one that was pre-registered.
	if !strings.Contains(out, "20.0%") {
		t.Errorf("the report does not state the measured share as 20.0%%:\n%s", out)
	}
	if !report.Viable() {
		t.Error("2 of 10 is exactly the 20% bar and was read as below it — the bar is a minimum")
	}
	if !strings.Contains(out, "CLEARS") {
		t.Errorf("the report states a share at the bar without saying what it decides:\n%s", out)
	}
}

// TestGraphReportStatesTheBarBesideTheNumber: the share alone reads as a result.
// It is only a result against a threshold somebody committed to beforehand, so
// the two are printed together and cannot be quoted apart.
func TestGraphReportStatesTheBarBesideTheNumber(t *testing.T) {
	var buf bytes.Buffer
	if err := reportGraph(&buf, palace.GraphReport{Drawers: 100, WithAny: 30, WithTwo: 5}); err != nil {
		t.Fatalf("reportGraph: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "5.0%") {
		t.Errorf("the measured share is missing:\n%s", out)
	}
	if !strings.Contains(out, "20%") {
		t.Errorf("the pre-registered bar is not printed beside the share, so the number can be "+
			"quoted without the criterion it was judged against:\n%s", out)
	}
	if !strings.Contains(out, "BELOW") {
		t.Errorf("5%% against a 20%% bar and the report does not say what it decides:\n%s", out)
	}
}

// TestGraphReportIsReadOnly pins the one property that makes a measurement
// re-runnable: an empty palace must produce a report, not an error and not a
// write.
func TestGraphReportIsReadOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := reportGraph(&buf, palace.GraphReport{}); err != nil {
		t.Fatalf("an empty palace failed the projection: %v", err)
	}
	if !strings.Contains(buf.String(), "nothing was written") {
		t.Errorf("the report does not say it changed nothing:\n%s", buf.String())
	}
}
