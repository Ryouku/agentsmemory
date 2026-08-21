# Pre-registration: what the real-query measurement would change

**Written:** 2026-08-21, BEFORE the results were available.
**Measurement:** 32 real queries pulled from `search_events`, replayed through the live `am_search`
MCP endpoint, judged hard for whether an agent could act on the result, each failure classified by
mode.
**Why this file exists:** every pending ADR was designed against a model of why recall falls short.
If that model is wrong, the ADRs are wrong, and the temptation once numbers exist is to read them as
confirming whatever is already written. Committing the interpretation first makes that impossible to
do quietly.

## The failure modes being measured

| Mode | Meaning |
|------|---------|
| `not-stored` | No memory answering this was ever filed |
| `ranked-below` | The answer exists and did not reach the page |
| `synthesis` | No single memory answers it; the answer is spread across several ("what is the current state of X") |
| `snippet-cut` | The right memory was returned and the window omitted the answer |
| `duplicates` | Chunks of one memory crowded the page |

## What each pending ADR is betting on

| ADR | Status | Its implicit bet | Confirmed if the dominant mode is | UNDERMINED if the dominant mode is |
|-----|--------|------------------|-----------------------------------|-------------------------------------|
| **001** recall answers or abstains | Accepted, 0/6 | The system retrieves adequately but cannot say when it has failed, so agents act on bad top-1 | `ranked-below` or `not-stored` — abstention converts a silent wrong answer into an honest one | `synthesis` — abstaining on a question no single memory can answer just adds a refusal to a page that was never going to answer it |
| **002** anchor the lexical score | Accepted, 2/4 | The lexical channel is mis-scaled, so the wrong candidate reaches the top | `ranked-below` | anything else — normaliser choice cannot move a memory that was never stored, and does nothing under the rrf default anyway |
| **003** retire the closet prior | Accepted, 2/5 | The curation prior displaces correct answers | `ranked-below` | anything else. Already shipped by ADR-014, so this is now a check on a live default rather than a proposal |
| **007** no number without its population | Accepted, 0/3 | The eval's own output misleads whoever reads it | ANY mode — it is about the instrument, not the retrieval, and an instrument that misreports its population misroutes every decision below | nothing measured here undermines it; it is the one ADR this measurement cannot argue against |
| **009** tune against your own corpus | Proposed, 0/3 | The right ranking parameters differ per corpus and nobody tunes by hand | `ranked-below` — tuning moves ranking | `synthesis` or `not-stored` — no parameter sweep fixes a question the corpus cannot answer, and auto-tuning against an eval that measures the wrong thing optimises confidently in the wrong direction |
| **010** supersede, do not overwrite | Proposed, 0/3 | Recall surfaces retracted memories as if current | `synthesis` — a "current state of X" question is exactly where stale and superseded records produce contradictory answers, and validity windows are what make "current" mean something | `ranked-below` alone — then it is a correctness nicety rather than the thing agents feel |

## The prediction

Stated before looking, so it can be wrong: **`synthesis` will be the largest mode.** The query list is
visibly full of "current state of X", "open threads", "what is still pending" — questions whose answer
is spread across a diary, several decisions and an inbox. Nothing in the corpus is written to answer
them as one memory.

If that holds, the ranking ADRs (002, 003, 009) drop in priority regardless of their individual
merit, 010 rises because "current" is the word doing the work in those queries, and the gap is a
missing CAPABILITY — assembling an answer from several memories — that no ranking change reaches.

If it does not hold and `ranked-below` dominates, the reverse: the ranking work is the work, and this
file says so.

## What would make this measurement worthless

- Judging generously. "Related to the subject" scored as "answers" would make any corpus look good.
- Conflating a timeout with an empty result. A search here costs ~20 s; a 30 s client timeout would
  manufacture `not-stored` verdicts out of a working system.
- Reading n=32 as precision. It is enough to rank failure modes, not to put an interval on any of
  them, and no decision below should be taken as if it were.
