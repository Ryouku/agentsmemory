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

	t.Run("the control", func(t *testing.T) {
		got := base.RankingProfile()
		for key, want := range map[string]string{
			"unit":     "unit=chunk",
			"evidence": "evidence=lexical",
			"rerank":   "rerank=off",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: profile does not report %q, so am_status cannot say which arm ran: %s", key, want, got)
			}
		}
	})

	t.Run("the treatment", func(t *testing.T) {
		treated := base.Clone().WithMemoryLevelRanking(true)
		got := treated.RankingProfile()
		if !strings.Contains(got, "unit=memory") {
			t.Errorf("memory-level ranking is ON and the profile still reports a chunk unit: %s", got)
		}
		// The control must not have changed underneath: Clone exists so an arm can
		// be configured without disturbing the served service, and a profile read
		// off shared state would report the last arm constructed rather than the
		// one that ran.
		if strings.Contains(base.RankingProfile(), "unit=memory") {
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
}
