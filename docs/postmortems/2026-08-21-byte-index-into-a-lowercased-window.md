---
date: 2026-08-21
category: logic-error
severity: critical
files_changed:
  - internal/palace/rank.go
  - internal/palace/snippethead_test.go
tags: [unicode, panic, hot-path, snippet, review-found, ascii-only-tests]
---

## Symptom

Any search whose result text contained certain non-ASCII letters, and whose query term matched late
in the chosen snippet window, panicked:

```
panic: runtime error: slice bounds out of range [:121] with length 100
    internal/palace.Snippet(…) rank.go:725
```

`Snippet` runs on every hit of every search, so this is the request path `am_search` and the
dashboard both take. Go's HTTP server recovers a handler panic and drops the connection, so the
symptom an agent sees is a search that fails rather than a server that dies — and it fails
deterministically, for the same memory, every time, while every other query works.

## Context

The day before, real-query measurement found the largest failure mode was not ranking but
DELIVERY: the right memory at rank 1 with the answer not in the returned text. Two fixes went into
`Snippet`. The second added a "complete a clipped term" pass — the window is chosen by how many
query terms START inside it, so a term beginning two characters before the boundary counts as found
and is then delivered as a fragment ("…a budget must be shor…"). The pass shifts the window right so
the clipped term completes.

To find the term it lowercases the window, because the tokens are lowercased and the content is not.

## Root Cause

The pass mixed two coordinate systems that agree on ASCII and nowhere else.

```go
i := strings.Index(strings.ToLower(string(runes[best:end])), t)   // BYTE index, lowercased window
…
termEnd := best + len([]rune(string(runes[best:end])[:i])) + len([]rune(t))
//                             ^^^^^^^^^^^^^^^^^^^^^^^^ ORIGINAL window, sliced by that byte index
```

`strings.ToLower` maps runes one-for-one — it is `strings.Map(unicode.ToLower, s)`, and
`unicode.ToLower` returns exactly one rune. It does **not** map bytes one-for-one. U+023A (Ⱥ) is two
bytes and lowercases to U+2C65 (ⱥ), which is three. So a window of such runes lowercases to a
LONGER string, `strings.Index` can return an index that exists in the lowered string and not in the
original, and slicing the original with it is out of range.

The failure needs both halves: content holding a rune whose case mapping changes its byte length,
and a match far enough into the window that the accumulated drift exceeds the remaining bytes. There
are letters in both directions — Ⱥ Ɫ Ᵽ Ɽ grow, the Kelvin sign U+212A and the Ohm sign U+2126
shrink — and the shrinking ones do not panic, they silently shift the window to the wrong place.

The aligned slice was already in scope. `lower := []rune(strings.ToLower(content))` is computed at
the top of the function for exactly this purpose and IS aligned with `runes`, one rune to one rune,
because the mapping is per-rune. The new pass built its own lowered copy instead of using it.

## Investigation

Not found by a test. `go vet` is clean, the full suite exited 0, and the snippet file had gained
several new tests the same day — all of which used ASCII, where a byte index and a rune index are
the same number. That is the whole blind spot in one sentence.

It was found by an independent review dispatched at the diff, with the instruction to ask whether
the rewritten loop was correct for *every* input, naming multibyte runes among the cases to try. The
reviewer went and read Go's `unicode/tables.go` to confirm the exact 2-byte → 3-byte mapping before
reporting it, which is why the report arrived as a reproducible input rather than a suspicion.

Confirming it took one probe:

```go
content := strings.Repeat("Ⱥ", 200) + " budget must be short and the tail continues here"
Snippet(content, "budget", 60)   // panic
```

Two further questions were then settled, because the same misalignment could exist elsewhere:

1. Is `lower[start:end]` in the window-scoring loop also unsafe? No — `strings.ToLower` preserves
   RUNE count, so `len(lower) == len(runes)` and the two index identically. Invalid UTF-8 is also
   safe: `[]rune` and `strings.Map` both yield one U+FFFD per bad byte.
2. Does the pattern appear anywhere else? `grep -rn "strings.Index(strings.ToLower"` over `internal/`
   returned exactly one hit, the line being fixed.

## Fix

### Before

```go
i := strings.Index(strings.ToLower(string(runes[best:end])), t)
if i < 0 {
	continue
}
termEnd := best + len([]rune(string(runes[best:end])[:i])) + len([]rune(t))
```

### After

```go
win := string(lower[best:end])
b := strings.Index(win, t)
if b < 0 {
	continue
}
termEnd := best + utf8.RuneCountInString(win[:b]) + utf8.RuneCountInString(t)
```

Both the search and the slice-back now happen inside `win`, one string, so the byte index is only
ever used against the string it was taken from and is converted to a rune offset before it meets
`runes`. `win` comes from `lower`, which is aligned with `runes` by construction.

`TestSnippetSurvivesRunesThatChangeLengthWhenLowercased` sweeps six such runes across six window
sizes and four queries through both `Snippet` and `SnippetWithHead`, asserting no panic and valid
UTF-8 out. Reverted against the previous code, it panics.

## Lesson

A byte index and a rune index are the same number in ASCII and different numbers everywhere else, so
a test suite written in ASCII cannot tell them apart. Any function that takes an index from one
string and applies it to another needs a case where the two strings have different byte lengths —
and case mapping is the everyday operation that produces exactly that, in both directions.

More narrowly: never take an index from `strings.ToLower(x)` and use it on `x`. Search and slice
inside the same string, or convert to a rune offset first.

And the reason this reached a commit at all: the fix that introduced it was itself found by
measurement against real queries, which made it feel verified. It was verified for the thing it was
measured on — English prose. A change made in response to evidence is not thereby correct outside
the evidence's alphabet.
