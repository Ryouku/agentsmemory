package palace

import (
	"strings"
	"testing"
)

// TestRankingProfileReportsTheArmThatActuallyRan is the gate for finding A.
//
// ADR-024 calls this string "the authority for which arm ran", and am_status is
// the only place a running deployment states it — the startup lines are gated,
// but nothing gated the served value. Emptying RankingProfile to "" previously
// left the ENTIRE suite green, which means the measurement protocol's one
// runtime authority could be removed without a single test objecting.
//
// The assertion is per-key and against a service configured AWAY from every
// default, because a profile that reports defaults correctly and ignores
// configuration is the failure that matters: it would read as authoritative
// while describing an arm nobody ran.
func TestRankingProfileReportsTheArmThatActuallyRan(t *testing.T) {
	base := newTestService(t)

	t.Run("the served unit is memory", func(t *testing.T) {
		got := base.RankingProfile()
		for key, want := range map[string]string{
			"unit":     "unit=memory",
			"evidence": "evidence=lexical",
			"rerank":   "rerank=off",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: profile does not report %q, so am_status cannot say which arm ran: %s", key, want, got)
			}
		}
	})

	t.Run("a Clone does not mutate the base", func(t *testing.T) {
		cloned := base.Clone().WithMemoryEvidenceSelector("semantic")
		got := cloned.RankingProfile()
		if !strings.Contains(got, "evidence=semantic") {
			t.Errorf("memory evidence selector is ON and the profile still reports lexical: %s", got)
		}
		if strings.Contains(base.RankingProfile(), "evidence=semantic") {
			t.Errorf("configuring a Clone changed the base service's reported arm: %s", base.RankingProfile())
		}
	})

	t.Run("every field is present", func(t *testing.T) {
		got := base.RankingProfile()
		// ADR-024's Wiring table promises these keys by name. A profile missing one
		// is not a cosmetic problem — a run recorded without it cannot be attributed
		// to an arm afterwards, and the comparison it was gathered for is void.
		for _, key := range []string{"fusion=", "lex-weight=", "lex-norm=", "closet-boost=", "rerank=", "unit=", "evidence="} {
			if !strings.Contains(got, key) {
				t.Errorf("profile is missing the %q field ADR-024 promises: %s", key, got)
			}
		}
		if strings.TrimSpace(got) == "" {
			t.Fatal("profile is empty; am_status would report no arm at all")
		}
	})

	t.Run("rrf does not claim lexical knobs Search never reads", func(t *testing.T) {
		got := base.Clone().WithFusion("rrf").RankingProfile()
		if !strings.Contains(got, "lex-weight=n/a") || !strings.Contains(got, "lex-norm=n/a") {
			t.Errorf("rrf profile still names a lexical magnitude: %s", got)
		}
		if strings.Contains(got, "lex-weight=auto") || strings.Contains(got, "lex-norm=page-max") {
			t.Errorf("rrf profile reports unused defaults as if they ranked: %s", got)
		}
	})

	t.Run("retrieve-k is absent at the default", func(t *testing.T) {
		got := base.RankingProfile()
		if strings.Contains(got, "retrieve-k=") {
			t.Errorf("default profile names retrieve-k, so am_status would claim a floor Search does not use: %s", got)
		}
		on := base.Clone().WithRetrieveK(50).RankingProfile()
		if !strings.Contains(on, "retrieve-k=50") {
			t.Errorf("retrieve-k is ON and the profile still omits it: %s", on)
		}
		if strings.Contains(base.RankingProfile(), "retrieve-k=") {
			t.Errorf("configuring a Clone changed the base service's reported arm: %s", base.RankingProfile())
		}
	})
}
