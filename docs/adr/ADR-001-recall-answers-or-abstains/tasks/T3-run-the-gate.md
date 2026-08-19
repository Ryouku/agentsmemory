# Task ADR-001-T3: Run the gate on the real corpus and decide whether the rest of the ADR is built

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** the go/no-go decision, the recorded risk–coverage evidence, and the calibration file the server will be pointed at
**Consumes:** `agentsmemory eval --calibrate` with `--gate` (T2)

## Goal

Find out, before any of the serving code exists, whether a usable operating point survives on identifier-preserving negatives — and stop the ADR here if it does not.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-001-recall-answers-or-abstains/evidence/abstain-gate-2026-08.md` | add | the curve, both boundaries, the counts, the Wilson bound and the gate's exit code, so the decision has a run behind it rather than a memory of one |

## Ordered Steps

1. Re-run T1's and T2's acceptance commands first and confirm both test sets are green. A curve taken while the population labels or the verification drop are still red is not evidence — it is the old easy-negative measurement wearing a new name.
2. Generate a fresh case set with the identifier-preserving generator and the depth-20 verification (`agentsmemory eval --style absent --n 25 --cases <file>`), keeping the file, and record how many candidates survived verification and how many were dropped for which reason.
3. Generate or reuse the answerable set at the same settings so both populations come from one corpus and one build, and run the eval so the production arm scores every case.
4. Run `agentsmemory eval --calibrate --gate --cases <files>` and capture its whole output — the curve, `answer_at`, `refuse_below`, the count of absent cases below `refuse_below`, the refusal rate with its Wilson bound, the sample sizes, and the exit code.
5. Run the same command against a case set from `--style absent-easy` and record both curves side by side. That run exits non-zero by construction and writes no calibration file — it exists only for the comparison. The gap between the two curves is the size of the error the old corpus was hiding, and it is the one number that tells a future reader why this task exists.
6. Write the evidence file, then sign off with `adr-verify --human` stating the exit code. On a non-zero exit, stop: T4, T5 and T6 are not started, and the ADR is marked Withdrawn with the table attached.

## Acceptance

Acceptance is human-observed: the gate needs a populated palace, a live reranker and a generator model, so no hermetic exit code can stand in for it. Sign-off step —

```text
~/.claude/bin/adr-verify docs/adr/ADR-001-recall-answers-or-abstains/tasks/T3-run-the-gate.md \
  --human "gate run on hard negatives: <n> verified-absent (<n> dropped), <n> reachable-answerable; answer_at=<x> refuse_below=<y>; refusal <k>/<n> = <r>, 90% Wilson lower bound <b> against the declared 0.30; absent cases below refuse_below = <n>; eval --calibrate --gate exit <0|1>; recorded in evidence/abstain-gate-2026-08.md; decision <ship|withdraw>"
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| — (human-observed) | `docs/adr/ADR-001-recall-answers-or-abstains/evidence/abstain-gate-2026-08.md` | the recorded run is the evidence; T1's and T2's tests are what prove the instrument was honest when it was taken | — |

## Invariants

- Both regimes are measured with the same binary, the same corpus and the same generator settings; a hard-negative curve compared against an easy-negative curve from another build is not a comparison.
- The decision is the exit code of `--gate`, not a reading of the curve. A failing gate is recorded and acted on; it is never re-run with a lower target to obtain a pass.

## Risks

- The gate fails and the ADR is withdrawn. That is the outcome this task exists to make cheap: three files of measurement code instead of a config key, a wire field, a migration and a verdict nobody could trust.
- The gate passes on a sample this small and the threshold still generalises badly. Mitigation: the Wilson bound is the comparison, the counts ship in the calibration file, and the ADR's Follow-up requires a re-calibration as the corpus grows.

## Stop Condition

Stop the ADR — not just this task — if `--gate` exits non-zero: no threshold reaching the declared answer-recall refuses enough unanswerable queries to be worth a knob, a wire field and a column. Stop and ask if the two regimes disagree in direction (the easy set passing while the hard set fails is expected and is a pass for the *method*; the reverse means something is wired wrong).

## Out of Scope

- Any serving code — T4, T5 and T6 own that, and none of them starts until this task's log holds a `ship` sign-off.
- Re-calibrating after ADR-002 or ADR-003 change which document reaches top-1 (deferred: docs/adr/ADR-001-recall-answers-or-abstains.md — Follow-ups)

## Verification Log
