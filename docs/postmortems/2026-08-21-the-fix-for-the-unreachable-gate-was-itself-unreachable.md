---
date: 2026-08-21
category: silent-failure
severity: critical
files_changed:
  - internal/palace/evalstats.go
  - internal/palace/service.go
  - cmd/server/eval.go
  - cmd/server/wiring_test.go
  - internal/palace/gatedarm_test.go
tags: [reachability, wiring, eval, gate, no-caller, review-found]
---

## Symptom

An eval run on a server with no `RERANK_URL` — the ordinary self-hosted shape — refused its
supersession verdict with "the run was degraded, fix the reranker and re-run". The reranker was not
broken; there was never supposed to be one. The opposite deployment was worse and quieter: a server
configured for linear fusion WITH a working reranker printed a verdict headed `arm rrf+rerank`, a
pipeline it does not run, and printed it without complaint.

## Context

The supersession gate answers one question — does the palace surface a correction above the memory
it supersedes — and it must answer it about the ranking the server actually serves. The arm it
judges is pre-registered, never picked by score, because picking the best-looking arm from the same
data is the winner's curse the MRR table already warns about.

That pre-registration used to be a constant with a comment saying it "must change in the same commit
that changes production ranking". ADR-014 changed production on two dimensions and the constant did
not move, so the gate judged a configuration nobody ran. The fix, committed the same day, added
`Service.SupersessionGatedArmFor()`: ask the running service which arm reconstructs it.

## Root Cause

Nothing called it.

`printSupersessionGate`, `gatedArmCell` and `gatedArmRanks` all still read the package-level
`palace.SupersessionGatedArm()` — the SHIPPED default's arm, hardcoded to rrf with a reranker. The
new function existed, was correct, was documented and was tested, and no code path reached it.

The test is the instructive part:

```go
func TestServiceReportsItsOwnGatedArm(t *testing.T) {
	svc := NewService(nil, nil, nil, 0)
	if got := svc.SupersessionGatedArmFor(); got != ArmHybridCloset { … }
}
```

It calls the selector and checks the answer. It passes identically whether or not anything else in
the program ever calls it. This is the repo's own recorded lesson — *assert the CALL, not the
effect* — written into a postmortem the day before, and not applied to the very next fix.

A second defect sat underneath: the mapping itself named arms that do not rank the way production
ranks. An eval arm is a FIXED pipeline. `ArmHybrid` is the fixed 0.4 lexical weight at the page-max
normaliser, and `armBoosts` hands the closet prior only to arms whose NAME says closet — while
`Service.Search` passes its boosts into whichever ranker it selected. So `rrf + a closet prior`
mapped to `ArmRRF`, which carries no prior; `linear + auto weight` (the default shape) mapped to
`ArmHybrid`, which is not adaptive; and a non-default normaliser was ignored entirely.

## Investigation

Both halves came from an independent review of the diff, and both were confirmed mechanically
rather than by reading.

For the missing caller, `grep -rn "SupersessionGatedArmFor" --include="*.go"` returned two lines:
the definition, and one test. Then the mutation that matters — revert the call site to the global
and run the tests:

```
go test ./cmd/server/ -run Supersession   ok
```

Green. The mutant survives, which names the gap exactly: nothing in the tree distinguishes wired
from unwired, because `runEval` needs a database and no unit test drives it.

For the mapping, the claim was checkable in one line each: `internal/palace/service.go:877` passes
`boosts` into `rankRRF`, and `internal/palace/eval.go` gives `ArmRRF` `armBoosts(...) == nil`.

## Fix

### Before

```go
// three call sites, none of which can see the running service's configuration
want := palace.SupersessionGatedArm()

func gatedArmFor(fusionRRF, closetOn, reranked bool) EvalArm {
	switch {
	case fusionRRF && reranked: return ArmRRFReranked
	case fusionRRF:             return ArmRRF   // even with a closet prior in force
	…
	default:                    return ArmHybrid // even when production fuses adaptively
	}
}
```

### After

```go
// the served arm is threaded in from the service that will do the ranking
printSupersessionGate(out, report, runMeta, svc.drawers.SupersessionGatedArmFor())

func (s *Service) gatedArm(reranked bool) EvalArm {
	if s.fusionRRF {
		switch {
		case s.closetBoostScale > 0:
			return "" // no RRF arm carries the closet prior production applies
		…
		}
	}
	// linear: only the exact fixed-weight, page-max shape reaches the closet and
	// reranked arms; the swept weights, both adaptive features and the anchored
	// normalisers cover the rest; everything else is ""
}
```

`""` means *no arm reconstructs this ranking*, and the gate refuses rather than judging the nearest
one. That is ADR-007's rule — a measurement whose mechanism was not the served one reports that it
was not measured — applied to the gate.

Two gates now hold it:

- `TestGatedArmReconstructsTheServedRanking` runs BOTH rankers over a seven-fixture battery and
  compares the resulting ORDER. Three fixtures exist only because the first battery let adaptive and
  fixed-weight fusion look identical and a mutant survived it.
- `TestTheGateAsksTheServiceForItsArm` reads `cmd/server/eval.go` and fails when the call site stops
  passing the service's own arm, or starts reading the package-level default again.

The shipped default's arm is now derived from the same mapping a running service uses, so there is
one mapping instead of two that can drift apart — which is how the original constant went stale.

## Lesson

**A fix for an unreachability defect is the most likely thing to be unreachable.** The author is
concentrating on the new component being right, and "does anything select it" is the question that
was already answered wrongly once in this file. Ask it about the fix, immediately, before the commit.

**The mutation for wiring is: cut the wire and run the tests.** Not "does the new function work" —
delete the call site, restore the old one, and see whether anything goes red. It takes a minute and
it is the only check that separates a wired component from a well-tested orphan.

**When a test needs a database to reach the wiring, read the source.** A regex over the call site is
crude and it fails when the wire is cut, which is the only property required. A gate that can only
be written crudely still beats intending to remember.

**And when a mapping claims two things are equivalent, compare what they DO.** Names agree by
construction; orderings do not. The check that finally held here runs both rankers and diffs the
result, and it needed a deliberately adversarial fixture set — the first, comfortable one passed
against a mapping that was wrong.
