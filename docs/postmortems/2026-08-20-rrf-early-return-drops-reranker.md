---
date: 2026-08-20
category: logic-error
severity: critical
files_changed:
  - cmd/server/main.go
  - cmd/server/configureranking_test.go
tags: [reachability, wiring, early-return, ranking, reranker, gate-blind-spot]
---

## Symptom

With `--fusion=rrf` and a rerank URL configured, the server wired no reranker at all: the factory
was never called and the candidate pool was never widened. Startup printed
`fusion: reciprocal-rank (bm25 weight does not apply)` and then nothing — no `reranker: …` line —
and search returned the fused order unreranked. Nothing failed; the configuration simply did less
than it said.

## Context

`configureRanking` in `cmd/server/main.go` turns a `config.Config` into a configured
`*palace.Service` and returns the startup lines describing what it did. It handles, in order: the
closet prior, the fusion mode, the BM25 weight, and the reranker. `rrf` and reranking compose —
`Service.Search` fuses first and reranks the fused order, and `rrf+rerank` is a declared eval arm an
operator reads before choosing this configuration.

The commit immediately before this one fixed a genuine defect: under `rrf` the wiring announced that
the bm25 weight does not apply and then reported one, two adjacent lines disagreeing.

## Root Cause

The contradiction was suppressed with an early return rather than a condition. The `return` sat
above both the BM25 block and the reranker block, so it exited past wiring that `rrf` still wanted:

```go
if rrf {
	return drawers, lines
}
if strings.EqualFold(strings.TrimSpace(cfg.BM25Weight), "auto-idf") {
	// … bm25 weight wiring: correctly skipped under rrf
}
if cfg.RerankURL != "" {
	// … reranker wiring: WRONGLY skipped under rrf
}
```

The intent — "do not configure or announce a bm25 weight under rrf" — is a property of one block.
The mechanism chosen — leave the function — is a property of everything after it. The two coincide
only while the skipped block happens to be last, and it was not.

## Investigation

The defect was not found by a test. Every test in the package passed, including the two gates
written in the same commit to catch exactly this class of problem, and the full suite exited 0.

It was found by dispatching an independent reviewer at the diff with one question: *can either gate
pass while the property it names is false?* The reviewer read what came AFTER the early return, which
is the step the author had not taken — the author had verified that the contradiction was gone, and
a check that confirms the intended effect never looks at the unintended one.

Two things were then checked to establish it was a real defect and not a design choice:

1. Does anything downstream expect rrf and rerank together? `Service.Search` applies fusion and then
   reranking as separate stages, and `ArmRRFReranked = "rrf+rerank"` is a declared eval arm — so the
   combination is not merely permitted, it is measured and recommended.
2. Why did the mode-scope sweep, whose entire purpose is discovering knobs that do nothing under
   other knobs, not discover this? Because of what it observes: it varies a knob and watches the
   RESULT ORDERING change. Its fixture's reranker factory returns `nil`, so under rrf there is no
   reranker to lose and no ordering to move. The gate is structurally blind to the loss of a
   component its fixture never had.

That second answer is the reusable one, and it generalises past this bug: a gate that observes an
EFFECT cannot see the removal of a component the fixture does not supply. Only observing the CALL —
did the wiring reach the factory at all — separates "no reranker was configured" from "a reranker
was configured and had nothing to do".

## Fix

### Before

```go
if rrf {
	return drawers, lines
}
if strings.EqualFold(strings.TrimSpace(cfg.BM25Weight), "auto-idf") {
	drawers = drawers.WithLexicalIDF(true)
	say("bm25 weight: auto (IDF-weighted coverage)")
} else if w := cfg.BM25Weight; w != "" && !strings.EqualFold(w, "auto") {
	// … fixed-weight parsing
}
```

### After

```go
// This guards the BM25 block ONLY. The first version returned here instead,
// which silently took the reranker with it: rrf and reranking COMPOSE.
if !rrf {
	if strings.EqualFold(strings.TrimSpace(cfg.BM25Weight), "auto-idf") {
		drawers = drawers.WithLexicalIDF(true)
		say("bm25 weight: auto (IDF-weighted coverage)")
	} else if w := cfg.BM25Weight; w != "" && !strings.EqualFold(w, "auto") {
		// … fixed-weight parsing
	}
}
```

The condition now scopes exactly the block whose behaviour is conditional, so adding a fifth block
below cannot silently inherit the exclusion. `TestRerankSurvivesEveryFusionMode` observes the factory
CALL COUNT across `linear`, `rrf`, `RRF` and `""` — the reachability question, not the effect — and
goes red on the original code for every rrf spelling.

## Lesson

Never suppress an unwanted branch with an early return when other work follows it — scope the
condition to the block whose behaviour is actually conditional. An early return's blast radius is
everything below it, which grows silently every time someone appends a block, and the author who
adds that block has no reason to read a `return` fifty lines above it.

And when testing that a component is still wired, assert the CALL, not the effect: a fixture that
supplies a no-op double has no effect to lose, so an effect-based check passes identically whether
the component was configured or skipped entirely.
