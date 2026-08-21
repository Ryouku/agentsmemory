---
date: 2026-08-21
category: silent-failure
severity: critical
files_changed:
  - internal/palace/rank.go
  - internal/palace/snippethead_test.go
tags: [reachability, dead-branch, snippet, delivery, untested-fix, review-found]
---

## Symptom

None. That is the whole problem.

A search returned `"…a budget must be shor…"` — the right memory at rank 1 with the sentence the
agent needed cut out of the text. A fix for exactly that was written, committed with a message
naming that exact string, and shipped. The symptom continued to reproduce byte-for-byte, and
nothing anywhere said so.

## Context

Measurement against 32 real production queries on 2026-08-21 sorted the failures by mode. The
largest was not ranking: it was DELIVERY — retrieval put the right drawer at rank 1 and the snippet
did not contain the answer. Two mechanisms were found and both were fixed in one commit. The first,
a window-scoring loop that never reached the end of the content, was real and the fix works. The
second was this one.

`Snippet` chooses a window by how many query terms it contains, then renders it. The idea was: a
term sitting across the window's end boundary is delivered as a fragment, so shift the window right
until it completes.

## Root Cause

The repair looked for the clipped term INSIDE the chosen window.

```go
if end < len(runes) {
	for _, t := range terms {
		i := strings.Index(strings.ToLower(string(runes[best:end])), t)  // <- inside [best,end)
		if i < 0 {
			continue
		}
		termEnd := best + /* rune offset of i */ + len([]rune(t))
		if termEnd > end {                                              // <- unreachable
			…shift…
		}
	}
}
```

`strings.Index` returns a match only when the term is WHOLLY contained in the string searched. The
string searched is the window itself, so any match satisfies `i + len(t) <= len(window)`, which is
exactly `termEnd <= end`. The guarded branch cannot execute for any input.

The two halves of the sentence contradict each other and the contradiction is invisible: "find the
term that the window cuts" and "search within the window" cannot both be done. A term the window
cuts is, by definition, not in the window.

## Investigation

Found by an independent review dispatched at the diff. Confirming it took one command:

```
# delete the entire `if end < len(runes)` block, keeping the file compiling
go test ./internal/palace/ -count=1
ok  github.com/atvirokodosprendimai/agentsmemory/internal/palace  8.001s
```

The whole package suite stayed green with the fix removed. That is the finding, stated as an exit
code: no test in the tree distinguished the fixed code from the unfixed code, so nothing had ever
established that the fix did anything.

The symptom was then reproduced against the shipped code directly:

```
maxChars=30 -> "…must be shorter than any clien…"
maxChars=50 -> "the pool is fifty and the rerank budget must be sh…"
```

Two further defects in the same function surfaced while reading it with the same question, and both
also reproduced on the first probe: a fixed stride of 40 skipped every position between candidates
whenever the window was narrower than the stride, and `SnippetWithHead` prepended the memory's
opening even when the chosen body window already overlapped it, delivering the same runes twice.

## Fix

### Before

```go
if end < len(runes) {
	for _, t := range terms {
		i := strings.Index(strings.ToLower(string(runes[best:end])), t)
		if i < 0 {
			continue
		}
		termEnd := best + len([]rune(string(runes[best:end])[:i])) + len([]rune(t))
		if termEnd > end { // no input reaches this
			…shift…
		}
	}
}
```

### After

The rule is stated over WORDS rather than over query terms:

```go
if end < len(runes) && isWordRune(runes[end]) && isWordRune(runes[end-1]) {
	// the boundary is inside a word: measure how far the word runs on
	…
	// take the shift only if the match survives it
	if !windowHasTerm(lower, best, end, terms) || windowHasTerm(lower, shiftedStart, shiftedEnd, terms) {
		best, end = shiftedStart, shiftedEnd
	}
}
```

It needs no search — the condition is decidable from the two runes either side of the boundary — so
the contradiction that made the first version dead cannot be expressed. It also completes the
ordinary words a reader needs, not only the ones the query happened to name.

The guard on the last line is there because the first run of the new test caught the fix
reproducing the bug it fixes: the window shifts RIGHT, so a long word at the boundary pushed the
matched term off the left edge — "the answer is not in the returned text" with the opposite sign.

Four mutants, each confirmed to compile before being run: fixed stride, no shift at all, shift
without the match guard, head prepended unconditionally. All four red.

## Lesson

**A fix for a symptom is not verified by the symptom's absence in your head.** The commit message
quoted the exact failing string. Nobody, including the author, ran the input again afterwards.

**The cheapest test of whether a fix is reachable is to delete it.** If the suite stays green, the
code is not being tested — whatever it looks like, whatever its comment says. That check takes
under a minute and it is the only one that would have caught this, because every other signal
(compiles, vets, reviewed, has a careful comment explaining the measured symptom) was present and
green.

**Watch for a condition that contradicts its own search space.** "Find the thing the window
excludes" and "search the window" cannot both hold. The same shape recurs: looking for the dropped
record in the result set, the timed-out request in the completed list, the evicted key in the cache.
When a guard exists to catch an exceptional case, ask what input reaches it — and if you cannot name
one, it has none.
