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

---

## RESULT, 2026-08-21 — the prediction was wrong

32 queries: 12 answers (37.5%), 16 partial (50%), 4 hard failures (12.5%). The right drawer reached
the page in 59% of queries; only 63% of those pages carried the answer in the text the agent
receives.

| mode | primary cause count |
|------|--------------------|
| **Delivery — retrieval worked, the payload lost the answer** | **6** |
| Synthesis | 5 |
| Wing scoping (4 of the 4 hard failures) | 5 |
| Memory genuinely absent | 3 |
| Exists but ranked below / under a stale top hit | 1 |

**I predicted synthesis would be the largest mode. It was not.** It came joint-second, and the
judge's own note is that 6/5/5 on n=32 is a near-tie rather than a clear winner — so the honest
reading is that delivery, synthesis and wing scoping are of comparable size, and the prediction was
wrong about which leads rather than wrong about synthesis mattering.

**What nobody predicted, and it is the finding:** the mode MRR measures — the answer exists and did
not reach the page — is **last, at one query**. The eval optimises the one thing that is almost never
the problem.

### What this does to the pre-registered table

- **001 abstain** — the bet was `ranked-below` or `not-stored` (3 combined). Weak support. An
  abstention verdict would fire on the wing-scoping failures, where it would be actively wrong: the
  memory exists and the filter excluded it, so "I don't know" is a confident false statement.
- **002 lexical normaliser / 003 closet prior / 009 auto-tune** — all bet on `ranked-below`, which is
  the smallest mode. **Undermined as pre-registered.** 009 is the sharpest case: auto-tuning ranking
  parameters against an eval whose metric is blind to the top three modes optimises confidently in a
  direction that cannot help.
- **010 supersede** — pre-registered to rise if synthesis dominated. It did not, but 010 gets support
  from a different direction than predicted: the one `ranked-below` failure IS a stale top hit, a
  superseded 10:50 snapshot at rank 1 with its own 11:16 correction at rank 3. Small n, real
  mechanism.
- **007 populations** — unaffected, as pre-registered. Nothing here can argue against it.

### The work this actually points at

Delivery was the cheapest to fix and two mechanisms were found and fixed the same day: the snippet
window never scored the FINAL window, so a memory's conclusions were the least reachable text in it;
and the window could end inside a matched term. Neither moves MRR by a point, because rank 1 was
already correct — which is the whole point.

Still open from the same measurement: wing scoping produced 4 of the 4 hard failures (queries scoped
to wings that have never existed return an empty page in under a second, indistinguishable from a
genuine miss), and 5 of 32 responses carried no rerank at all with nothing in the payload saying so.

