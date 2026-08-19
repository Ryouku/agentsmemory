# Task ADR-002-T2: Cross the anchored normalisers with the existing weight sweep in the eval

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `evalArms` registry, anchored arm names `fusion bm25=<w> anchored:<norm>`, the `no-closet` anchored family, `fusionRankerFor` (the arm → ranker seam)
**Consumes:** `lexNorm`, `lexNormCeiling`, `lexNormSaturating` (T1)

## Goal

Make old and new normalisation comparable within one run on one shared candidate pool, by registering an anchored counterpart for every nonzero lexical arm plus an unboosted anchored family the deletion trigger can be read from — through a dispatch seam that can be tested behaviourally.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | the arm list is built inline in `EvaluateWith` (`eval.go:293-311`) and the fusion dispatch is a `switch` inside `evalCase` (`eval.go:546-694`, fusion cases at 554-560 and 657-679); extract both as `evalArms` and `fusionRankerFor`, then add the anchored arms |
| `internal/palace/eval_test.go` | edit | pin the registry's shape AND pin that an anchored arm actually ranks differently, without needing a live corpus |

## Ordered Steps

1. Write the failing tests first (TDD red), in `internal/palace/eval_test.go`:
   - `TestAnchoredArmsRankDifferentlyFromPageMax` — **the behavioural one, and the one that matters.** Take the ranker `fusionRankerFor` returns for `fusion bm25=0.40 anchored:ceiling` and the one it returns for `fusion bm25=0.40`, run both over a fixture built so they must disagree (one weak-but-winning lexical match against a strong one, per T1's page-max fixture), and assert the fused scores differ. A registry test cannot fail when an anchored arm falls through to the page-max branch; this one can. It follows `TestLexicalIDFChangesWhatSearchReturns` (`service_test.go:583`), whose predecessor asserted only that both modes returned results and passed while the flag was read by nothing at all.
   - `TestAnchoredArmsNoClosetFamilyIgnoresBoosts` — behavioural: the ranker for a `no-closet` arm returns identical fused scores whether it is handed a zero `boosts` slice or a nonzero one, and its boosted counterpart does not.
   - `TestAnchoredArmsCoverEveryNonzeroWeight` — `evalArms` contains a `ceiling` and a `saturating` counterpart for each nonzero entry of `bm25Sweep` and for both adaptive arms, plus one `no-closet` counterpart per anchored arm the deletion trigger reads.
   - `TestAnchoredArmsSkipWeightZero` — no anchored arm exists at `w=0`, because with the lexical term multiplied by zero the normaliser cannot matter and the row would be a duplicate reading as a finding.
   - `TestEvalArmsKeepProductionLast` and `TestEvalArmNamesAreUnique` — the registry's order and name uniqueness.

   Commit them red.
2. Extract the fusion dispatch from `evalCase`'s `switch` into `fusionRankerFor(arm EvalArm, base float64) func(query string, docs []string, dists, boosts []float64) []HybridScore`, returning nil for arms that are not score fusion (vector, RRF, contextual, production, reranked). `evalCase` calls it and keeps its remaining cases. This is what makes step 1's first two tests possible at all: today the only way to reach the dispatch is through a live corpus, an embedder and a reranker.
3. Extract the inline arm list into `evalArms(o EvalOptions, rerankReady bool) []EvalArm` and have `EvaluateWith` call it, so the registry is testable and one list feeds both the run and T4's disposition check. **Preserve the existing order**: `eval.go:298-301` states the production arm "runs LAST and always", and a reordering would falsify that invariant while every test stayed green.
4. Add the boosted anchored arms, reusing the same `docs`, `dists` and `boosts` the existing arms get — those mirror production, which boosts, and they are what the shipping rule is read from.
5. Add the `no-closet` anchored family: the three fixed swept weights and both adaptive arms under `ceiling` and `saturating`, ranked with `boosts` nil. The deletion trigger must fire in both boost regimes, and the boosted regime alone cannot separate a lexical-weighting effect from a boost-strength one — anchoring inflates the additive boost by `1/s`, `s = 1 − w(1 − a)`, and `s` differs between a fixed arm and an adaptive arm whose effective weight moves per query. `ArmHybrid` passing nil while `ArmHybridCloset` passes boosts (`eval.go:554-560`) is the existing precedent for a paired regime.
6. Confirm no extra retrieval or cross-encoder call is added: arms share one pool per case and the rerank scores are fetched once (`eval.go:507-511`).
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestAnchoredArms|TestEvalArm|TestLexNorm|TestEveryDeclaredArmIsRegistered|TestSweptArmsAreReachable" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnchoredArmsRankDifferentlyFromPageMax` | `internal/palace/eval_test.go` | an anchored arm produces different fused scores through the shared ranking seam — the dispatch is wired, not merely named | — |
| `TestAnchoredArmsNoClosetFamilyIgnoresBoosts` | `internal/palace/eval_test.go` | the `no-closet` arms really drop the boost, and their boosted counterparts really carry it | — |
| `TestAnchoredArmsCoverEveryNonzeroWeight` | `internal/palace/eval_test.go` | every nonzero fixed weight and both adaptive arms get both anchored counterparts, plus the `no-closet` family the trigger reads | — |
| `TestAnchoredArmsSkipWeightZero` | `internal/palace/eval_test.go` | no anchored arm at `w=0`, where the normaliser cannot change the order | — |
| `TestEvalArmsKeepProductionLast` | `internal/palace/eval_test.go` | the extracted registry returns the same order as the inline list for both `rerankReady` values, with `ArmProduction` after every non-reranked arm | — |
| `TestEvalArmNamesAreUnique` | `internal/palace/eval_test.go` | two arms never collide on a name, which would silently overwrite a row | — |

## Invariants

- Every pre-existing arm scores exactly as before: this task adds rows to the table and changes none of them, in the order they already ran.
- One vector search and one cross-encoder call per case, unchanged — arms are cheap because they share the pool, and that is why measuring both normalisers in both boost regimes costs nothing but table width.
- The boosted anchored arms carry the closet boost wherever their page-max counterparts do; the `no-closet` arms carry it nowhere.
- `fusionRankerFor` is the only place an arm name is turned into a ranker, so an arm cannot be scored by a ranker no test can reach.

## Risks

- Table width: the anchored arms roughly double the fusion rows and the `no-closet` family adds ten more, and a wide table is easier to misread than a wrong one is to spot. Mitigation: the arm name carries the normaliser and the regime, so `fusion bm25=0.40` and `fusion bm25=0.40 anchored:ceiling` sort next to each other and the comparison a reader wants is the adjacent pair.
- An anchored arm silently falling through to the page-max dispatch would produce two identical rows that read as "the normaliser makes no difference". The existing `armreach_test.go` checks are syntactic by design and cannot catch this; `TestAnchoredArmsRankDifferentlyFromPageMax` is the behavioural check that can, and T3's pairing gate catches it again on real ranks.
- Extracting `fusionRankerFor` touches the hottest loop in the eval. Mitigation: it is a pure refactor with the existing arms' rows as the regression test — any change in a pre-existing arm's numbers is a bug in the extraction, not a finding.

## Stop Condition

Stop if adding the arms makes a run's wall-clock time grow more than marginally — that would mean an arm is retrieving or re-scoring rather than re-ordering, which breaks the shared-pool premise the whole comparison rests on.

## Out of Scope

- Running the eval and committing the numbers — that is T3's job.
- Changing any default or config key — that is T4's job.

## Verification Log
