package palace

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Spec: docs/specs/2026-08-26-a-recall-that-answers.md
//
// These are DELIBERATELY RED stubs bound to @spec facts and scenarios. They
// compile and fail, which is the TDD red state the spec gate requires — a test
// that does not compile is not collectable, and an uncollectable test binds
// nothing. /quality-harness:adr-execute turns them green one task at a time.

func TestAWingScopedRecallNeverReturnsAnotherWingsFact(t *testing.T) {
	t.Fatal("F-1 not implemented: a wing-scoped recall must not return the content of a fact belonging to another wing")
}

func TestARecallNamesTheWingsThatHoldTheAnswer(t *testing.T) {
	t.Fatal("F-2 not implemented: when matching facts exist in other wings, the response must name them and say they can be queried; silence is indistinguishable from 'nothing is filed'")
}

func TestACorrectedRecordArrivesCarryingItsCorrection(t *testing.T) {
	t.Fatal("F-3 not implemented: a hit that is the object of retracts/supersedes/qualifies must carry that edge and the replacement id — marked, never hidden")
}

func TestFactLookupMatchesBothEntityVocabularies(t *testing.T) {
	t.Fatal("F-4 not implemented: fact lookup must match a query against kg_entities AND drawers.entities, read-only")
}

func TestFactAnswerableRateIsMeasured(t *testing.T) {
	// Rung 2, driven rather than parsed: TestEveryDeclaredArmIsRegistered proves
	// the identifier is MENTIONED in evalArms, which a comparison would satisfy.
	// This calls the function and looks in its output, so deleting the append
	// fails here even if the name survives elsewhere in the body.
	t.Run("the arm is registered", func(t *testing.T) {
		for _, rerank := range []bool{false, true} {
			if !slices.Contains(evalArms(EvalOptions{}, rerank), ArmFactRetrieval) {
				t.Errorf("rerank=%v: ArmFactRetrieval is declared but evalArms never returns it, so it appears in no table", rerank)
			}
		}
	})

	// The case set exists, is loadable, and every case has a gold triple. A
	// corpus that loads to zero cases would make every rate 0/0 and satisfy any
	// assertion about the rate vacuously.
	cases, err := LoadFactCases(filepath.Join("testdata", "factcases-synthetic.jsonl"))
	if err != nil {
		t.Fatalf("load fact cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no fact cases loaded")
	}
	for i, c := range cases {
		if c.ExpectTriple == "" {
			t.Errorf("case %d has no gold triple; its answerable-rate contribution is unfalsifiable", i)
		}
		if c.Category != CatFact {
			t.Errorf("case %d is category %q, not %q — it would be averaged into single-hop and hidden", i, c.Category, CatFact)
		}
	}

	// The rate always carries its denominator. This is the assertion that stops
	// "40%" being quoted a month later over a corpus nobody can reconstruct.
	t.Run("the rate carries its denominator", func(t *testing.T) {
		r := FactAnswerRateFrom([]int{0, 3, 0, 1, 0})
		if r.Answered != 2 || r.Cases != 5 {
			t.Fatalf("got %d/%d, want 2/5", r.Answered, r.Cases)
		}
		if !strings.Contains(r.String(), "2/5") {
			t.Errorf("rate renders as %q, which does not carry its denominator", r.String())
		}
	})

	// The baseline is 0% BY CONSTRUCTION: nothing returns facts yet. Stating it
	// as a test rather than as prose is what makes a later non-zero result mean
	// something — the alternative is zero, so it cannot be noise.
	t.Run("the baseline is zero", func(t *testing.T) {
		ranks := make([]int, len(cases))
		base := FactAnswerRateFrom(ranks)
		if base.Fraction() != 0 {
			t.Fatalf("baseline is %s, want 0 — nothing on the search path returns facts yet", base)
		}
		if base.Cases != len(cases) {
			t.Errorf("baseline denominator is %d, want %d", base.Cases, len(cases))
		}
	})

	// The manifest is the redacted record of the real run. Its absence would mean
	// a rate with no auditable provenance at all.
	m, err := LoadFactCorpusManifest(filepath.Join("testdata", "factcases-manifest-2026-08-26.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Cases <= 0 || m.Date == "" {
		t.Errorf("manifest carries cases=%d date=%q; a rate without a denominator and a date is unfalsifiable later", m.Cases, m.Date)
	}
}

func TestFactsOnThePageAreScoredByMRR(t *testing.T) {
	// F-6 is a property of the INSTRUMENT, provable before facts reach the page:
	// the fact arm produces the same Ranks slice every other arm produces, so the
	// existing statistics consume it unchanged. If it needed its own MRR path,
	// its numbers would not be comparable with any other row in the table.
	ranks := []int{1, 0, 2, 5, 0, 1}

	iv := BootstrapMRR(ranks)
	if iv.Hi <= 0 {
		t.Fatalf("BootstrapMRR over the fact arm's ranks gave %s; the fact arm is not scored on the shared statistic", iv)
	}
	if iv.Lo > iv.Hi {
		t.Errorf("interval %s is inverted", iv)
	}
	// The observed MRR must sit inside its own interval, which is what makes the
	// interval a statement about this arm rather than a decoration beside it.
	var mrr float64
	for _, r := range ranks {
		mrr += reciprocal(r)
	}
	mrr /= float64(len(ranks))
	if !iv.Contains(mrr) {
		t.Errorf("observed MRR %.3f lies outside its own interval %s", mrr, iv)
	}

	// The paired comparison is what makes a fact-arm change readable against a
	// control rather than against a remembered number.
	other := []int{2, 0, 3, 4, 0, 1}
	d := PairedDelta(ranks, other)
	if d.Lo > d.Hi {
		t.Fatalf("paired delta interval is inverted: [%v,%v]", d.Lo, d.Hi)
	}

	// A miss is rank 0 and must not be scored as a hit at rank 1 — the arithmetic
	// that would quietly turn every miss into a perfect answer.
	allMissed := BootstrapMRR([]int{0, 0, 0})
	if allMissed.Hi != 0 {
		t.Errorf("an all-miss arm scored %s, want an interval pinned at 0", allMissed)
	}

	// And the answerable-rate agrees with the ranks it was derived from, so the
	// two numbers the arm reports cannot drift apart.
	if got := FactAnswerRateFrom(ranks); got.Answered != 4 || got.Cases != 6 {
		t.Errorf("answerable rate %s disagrees with the ranks it came from; want 4/6", got)
	}
}

func TestAnEndedFactIsNeverPresentedAsCurrent(t *testing.T) {
	t.Fatal("F-7 not implemented: a fact with a non-empty valid_to must not be presented as current")
}

func TestAFactsWingComesFromItsProvenance(t *testing.T) {
	t.Fatal("F-8 not implemented: wing membership derives from kg_triples.source_drawer_id in three states — LOCAL, FOREIGN (wing derivable, named per F-2), UNLOCATABLE (not derivable, counted per F-18). Unresolvable provenance is never LOCAL")
}

func TestReturningFactsDoesNotChangeDrawerRanking(t *testing.T) {
	t.Fatal("F-9 not implemented: the fact block is additive — drawer selection and order must be unchanged, so this cannot be confounded with a ranking change")
}

func TestAWingReportsItsOwnEntryPoint(t *testing.T) {
	t.Fatal("F-10 not implemented: a wing must report its entry record and outgoing taxonomy edges, so reaching a taxonomy never needs an id the server did not supply")
}

func TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked(t *testing.T) {
	t.Fatal("F-11 not implemented: every drawer gets an edge at write time; a derived edge is marked as derived and never overwrites an authored one")
}

func TestAFactLookupDistinguishesAbsenceFromFailure(t *testing.T) {
	t.Fatal("F-12 not implemented: observed 2026-08-26 — am_kg_query returns count:0 with no error for a nonexistent entity AND a nonexistent predicate, so F-2's pointer cannot be trusted until absence and failure differ")
}

// UC-6 — the bootstrap. One call replaces a client-side protocol that currently
// costs ~25k tokens of instructions plus a hardcoded root id.

func TestOneCallBootstrapsAWing(t *testing.T) {
	t.Fatal("F-13 not implemented: one call must return entry point, eager content, on-demand pointers, swept corrections, resolved wing and a truncation report — no second call, no id from a skill file")
}

func TestATruncatedBootstrapSaysWhatItDropped(t *testing.T) {
	t.Fatal("F-14 not implemented: the bootstrap must be bounded AND report what it omitted. Silent spill is the failure it exists to remove — the protocol it replaces lost 74% of a prescribed tier to an unreported cap")
}

func TestCorrectionsAreSweptServerSideAcrossAllThreePredicates(t *testing.T) {
	t.Fatal("F-15 not implemented: retracts, supersedes AND qualifies, read INCOMING. Outgoing-only traversal cannot see a correction; running only retracts once shipped a pointer to an ADR not on main")
}

func TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces(t *testing.T) {
	t.Fatal("F-16 not implemented: assert SEMANTIC PARITY with the redacted baseline manifest FIRST, then that it costs fewer output tokens under the tokenizer that manifest names. Without parity the cheapest conformant bootstrap returns nothing")
}

func TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk(t *testing.T) {
	t.Fatal("F-17 not implemented: am_traverse's max_hops is provably inert (via is an intersection carried forward, so hop>=2 adds nothing), so a bootstrap built on multi-hop traversal would silently return only hop 1")
}

// TestAQuestionReachesTheFactThatAnswersIt binds UC1-S1, the happy path of
// reaching a fact by question. It exists because the scenario was bound to
// TestAWingScopedRecallNeverReturnsAnotherWingsFact, whose assertion is
// satisfied by returning no facts at all — an unfalsifiable gate on the one
// path that has to prove a question ARRIVES somewhere.
func TestAQuestionReachesTheFactThatAnswersIt(t *testing.T) {
	t.Fatal("UC1-S1 not implemented: a wing holding a current fact whose subject is semantically close to the question returns that fact in a distinct block beside the drawer hits, without the question naming the entity")
}

// TestAnUnlocatableFactIsCountedNotDropped binds F-18. Of 196 triples measured
// 2026-08-26 only 90 resolve to a drawer, so a fact that matches but cannot be
// placed in any wing is the majority case. Dropping it silently would recreate
// the exact failure this spec removes: silence that reads as "nothing is filed".
func TestAnUnlocatableFactIsCountedNotDropped(t *testing.T) {
	t.Fatal("F-18 not implemented: a matching fact whose wing cannot be derived is reported as a count and attributed to no wing")
}

// TestOneWingRuleGovernsEveryNewResponsePath binds F-19. The fact block, the
// sibling pointer, the entry point's edges and the bootstrap's inline content
// are four ways out of the server, and a rule re-implemented four times is a
// rule that will disagree with itself on the path nobody tested.
func TestOneWingRuleGovernsEveryNewResponsePath(t *testing.T) {
	t.Fatal("F-19 not implemented: one wing-authorization rule governs the fact block, the sibling pointer, EntryPoint's edges and the bootstrap's inline content")
}
