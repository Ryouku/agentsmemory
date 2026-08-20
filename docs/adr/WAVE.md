# Wave 2 — order of work, and why this order

Set 2026-08-20. The order is the owner's, and it inverts what the evidence alone would have
suggested: prove the palace is exercised before trusting anything measured about it.

| # | ADR | The question it answers | Blocked by |
|---|-----|-------------------------|------------|
| 1 | [ADR-008](ADR-008-exercise-the-palace-end-to-end.md) | Does every tool actually work, end to end, for one party, two, and three? | nothing |
| 2 | [ADR-006](ADR-006-knobs-that-do-nothing.md) | Does a setting an operator changes reach an active code path and change behaviour? | nothing |
| 3 | [ADR-007](ADR-007-no-number-without-its-population.md) | Does the eval print numbers that mean what they say? | nothing technically; third by priority |
| 4 | [ADR-009](ADR-009-tune-against-your-own-corpus.md) | Is the configuration an operator actually runs the right one for their corpus? | ADR-007 T3 (a tuner must not read a number it cannot trust) |

**Why this order.** The standard we are held to is not "a test exists that names this" but "there is
an active code path, it is exercised, and here is the evidence". Measured 2026-08-20 against that
standard, the tool surface fails it hardest: 41 tools registered, 39 named in no test, and zero
tests that drive a handler at all. Everything the eval measures runs on top of that surface, so a
number taken before ADR-008 lands is a number about an unproven substrate. ADR-006 sits between them
because a setting that never reaches an active code path makes both the tool surface and the eval
report behaviour nobody configured.

**The evidence bar, stated once and applying to all three.** A gate may not conclude a capability
exists from a variable being read, a name appearing in a list, or a comment describing behaviour.
It concludes from an observable effect: a second call that sees what the first did, a mutation that
turns a test red, or an exit code. Every task in this wave carries a `## Mutants` table naming the
edit that reintroduces the defect, whether that edit compiled, and which test caught it — because on
a task measured this week, three of five mutants survived the tests written for them and all three
read as covered until the mutation was actually run.

**Why ADR-009 exists, in one line.** The maintainer, who wrote the system, runs it on defaults —
and the default is measurably the worst arm on every corpus anyone has run: `fusion bm25=auto`
scores 0.226 and 0.279 on the two n=100 tables where turning the lexical leg off scores 0.367 and
0.445. The default is the product, and nobody tunes it. It sits fourth rather than first because a
tuner that reads a table it cannot trust (ADR-007) automates the wrong answer at scale.

**Not in this wave.** Session-to-session and agent-to-agent continuity — the handoff and real-time
collaboration question — is `/spec-write`, not an ADR: the requirements are undecided, and there is
a measured tradeoff to open on (the diary has fragmented into 11 threads over 89 entries, while
`am_search wing:"*"` has no thread to fragment but degrades as the corpus grows). Writing an ADR
first would be the "Wiring: TBD" anti-pattern.

**Debt.** `adr-debt docs/adr` reports 47 deferred entries and 8 open follow-ups as this wave opens.
That count is the baseline; it is expected to fall as ADR-006 T4 and ADR-007 T2 close items that are
currently blocked rather than punted, and to rise where this wave defers honestly. Re-run it at each
`/adr-write` and record the delta rather than the absolute number.
