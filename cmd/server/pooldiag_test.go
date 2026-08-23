package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestPoolDiagnosisCountsOnlyPooledArms.
//
// The diagnosis answers one question: how many golds were never in the shared
// candidate pool, so no reordering could reach them. It reads the worst NotFound
// across arms — but NotFound does not mean the same thing in every arm.
// supersessionScope already says so: ArmProduction is ScopePage, because it goes
// through Service.Search and is scored over the PAGE that returns, not over the
// pool. A gold at pool rank 12 is "not found" for production and found for every
// pooled arm, and it is not a retrieval failure at all.
//
// Folding it in inflates the count and prints advice that cannot work: raising
// --pool does not widen production's page, so following it changes nothing and
// the reader concludes the embedding is at fault. The same mistake was already
// fixed once for ArmContextual by name; production has the identical property
// and was never excluded. This pins the classification instead of the name.
func TestPoolDiagnosisCountsOnlyPooledArms(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmVector, Cases: 30, NotFound: 2},
		{Arm: palace.ArmRRFReranked, Cases: 30, NotFound: 2},
		// Scored over the page, not the pool: six golds sat below the page cut.
		{Arm: palace.ArmProduction, Cases: 30, NotFound: 8},
	}}

	var buf bytes.Buffer
	printPoolDiagnosis(&buf, report)
	got := buf.String()

	if strings.Contains(got, "8 of 30") {
		t.Errorf("reported production's page misses as pool misses:\n%s", got)
	}
	if !strings.Contains(got, "2 of 30") {
		t.Errorf("did not report the pooled arms' 2 misses:\n%s", got)
	}
	// The page miss is real information — it must not simply vanish — but it has
	// its own knob, and --pool is not it.
	if !strings.Contains(got, "page") {
		t.Errorf("production's page misses were dropped entirely; they are a real "+
			"finding with a different remedy:\n%s", got)
	}
}

// TestPoolDiagnosisStaysSilentWhenEveryPooledArmFoundEverything: a run where only
// the page-scoped arm missed anything has no retrieval failure to report, and
// printing one sends the reader after the embedding.
func TestPoolDiagnosisStaysSilentWhenEveryPooledArmFoundEverything(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmVector, Cases: 30, NotFound: 0},
		{Arm: palace.ArmProduction, Cases: 30, NotFound: 5},
	}}
	var buf bytes.Buffer
	printPoolDiagnosis(&buf, report)
	if strings.Contains(buf.String(), "OUTSIDE the candidate pool") {
		t.Errorf("claimed a retrieval failure when every pooled arm found every gold:\n%s", buf.String())
	}
}
