package palace

import "testing"

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
	t.Fatal("F-5 not implemented: a case set whose gold answers are kg_triples, and an arm reporting the fraction that reached the response. Baseline is 0% by construction")
}

func TestFactsOnThePageAreScoredByMRR(t *testing.T) {
	t.Fatal("F-6 not implemented: once facts share the page, ordering is scored on the same paired bootstrap as every other arm")
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
