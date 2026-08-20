# ADR backlog

Work deliberately punted out of an accepted or proposed ADR, kept here so it resurfaces at the
next `/adr-write` instead of dying in a scope section. `adr-debt docs/adr` sweeps the `(deferred:)`
pointers that lead here.

An entry leaves this file in one of two ways: it becomes an ADR, or it is re-tagged
`(permanent: <why>)` in its originating ADR because we decided it should never happen.

## From ADR-001 (recall answers or abstains)

- **Contradiction reporting** — recall says "this changed on `<date>`: it was X, it is now Y".
  Blocked on a populated temporal knowledge graph: measured 2026-08-18 on the pre-reset palace, ~65
  triples against ~5,020 drawers, so the mechanism existed and was unfed. Post-reset (2026-08-20)
  the ratio inverted — 41 triples against 80 drawers — so the blocker is now corpus size, not
  extraction coverage. Revisit once `kg-extract` has run at corpus scale.
- **Write-time findability gate** — when a memory is filed, generate the question it answers and
  try to retrieve it; report at write time when a memory is unfindable at birth. Reuses ADR-001's
  calibration, so it is drafted after ADR-001 ships rather than beside it.
- **Continuous evaluation with automatic promotion** — shadow-run competing retrieval
  configurations against real traffic and promote the winner when a paired test clears. Blocked on
  real-query telemetry volume: `search_events` held ~10 rows on the pre-reset palace, which is why
  the `--style real` eval arm produced n=4; it holds 25 as of 2026-08-20.
- **Learned multi-feature abstention** — a classifier over score, margin, distance and lexical
  coverage rather than one threshold. Blocked on labels: the 21 verified-absent cases the pre-reset corpus
  produced cannot fit and hold out. Revisit above ~200, and only if it beats the one-float-per-backend baseline on the same
  risk–coverage curve.
- **Growing the verified-absent corpus** beyond what a single `--n` run produces, including whether
  hard negatives can be mined from real queries instead of generated.
- **Reading recorded verdicts back for production calibration** — ADR-001 records the verdict in
  `search_events`; nothing consumes it yet. That consumption is the same loop as continuous
  evaluation above and should land with it.

## Standing: the instrument is not allowed to decide the hypothesis space

The eval scores ranked lists by MRR, which is IR's framing — retrieve documents, rank them, score
the rank. That framing has already acted as a filter on what we consider worth building: an idea
was counted DOWN in a design review for being "unmeasurable by an eval that scores ranked lists",
which is the instrument choosing the experiments rather than the other way round.

It is also why a published "raw chunked storage beats summarisation" result read as a verdict on
consolidation when it is a recall result — and raw text is a superset of any summary of it, so a
superset cannot lose that metric. We built our measuring stick from the same tradition whose limits
we are trying to get past.

The rule is therefore NOT "measure before you default" — that one earns its keep every week. It is:
**when a claim does not fit the instrument, extend the instrument.** Never read "we cannot measure
it" as "it is not worth building"; read it as a gap in the harness.

Metrics the harness still cannot express, each blocking a class of idea:

- **Answer-support / tokens-to-answer** — a metric a superset cannot automatically win, which is
  the precondition for evaluating any consolidation or compression idea honestly.
- **Findability-at-write** — whether a memory can be retrieved by the question it answers, measured
  when it is filed rather than in an eval weeks later.
- **Retrieval-conditioned value** — which memories actually get used, from `search_events`, so
  consolidation can be driven by what is ASKED FOR rather than by what was written. No published
  memory benchmark can express this: a benchmark runs once and has no usage history. We are a
  service and do.
- **Non-ranking outcomes generally** — abstention quality (in progress, ADR-001) and supersession
  correctness (in progress, ADR-004) are the first two; they should not be the last.

## Candidate pool should be a measured ceiling, not a constant

`DefaultRerankPool = 50`, `DefaultSearchLimit = 5`, `MaxSearchLimit = 100` and
`hybridCandidateMultiplier = 3` are the same numbers on a 5,000-drawer palace and on one
orders of magnitude larger. The retrieval reach they buy is not the same:

Measured 2026-08-18, before the reset:

- large corpus, `--pool 50`: 3 of 30 answers outside the pool (~10% unreachable)
- large corpus, `--pool 128`: 1 of 30 (~3%)
- our corpus then (45x smaller, ~5,020 drawers), `--pool 20`: 1 of 40 (~2.5%)

A small palace reaches ~97% of its answers with a pool of 20; the large one needs ~128 for the
same reach. One constant is wrong for one of them by roughly a factor of six.

Three quantities are currently conflated under one idea of a "limit", and they scale differently:

- **candidate pool** — bounds what is reachable at all; should scale with corpus.
- **rerank pool** — bounded by cross-encoder inference cost, which is linear in pool size, NOT by
  corpus. Scaling it with the corpus makes latency scale with the corpus, which is the thing a
  vector index exists to avoid.
- **page returned to the agent** — bounded by the consumer's context budget. Should NOT scale with
  corpus at all: more results from a bigger palace is more to be wrong about.

The proposal is deliberately not `pool = f(N)`, which would be a new inherited constant with an
exponent bolted on. It is a **target retrieval ceiling** — declare that some share of answers must
be in the pool, and let the pool be whatever achieves it on this corpus, measured by the retrieval
ceiling the eval now reports. Same cure as `max_distance`, the BM25 normaliser and `rerankWeight`:
replace a number somebody typed once with an operating point somebody measured.

Note the coupling before changing either: when the candidate pool exceeds the rerank pool, fusion
decides which candidates the cross-encoder ever sees. Growing one without the other silently hands
more of the decision to the weaker signal.

## The product is a runtime quality control plane, not an eval score

Forty generated cases are a release guardrail. They caught real wiring defects this week — a dead
eval arm, chunk-level gold, a production arm measuring a limit nobody uses — and they cannot
establish production quality, because the thing that degrades in production is not the ranking
function. It is everything around it as the index, the traffic, the tenants and the models change.

What `search_events` records today: wing, room, query, candidate count, hit count, top score,
whether a reranker was configured, and a timestamp. That answers almost none of the questions a
running memory service has to answer:

- is the index fresh and complete, and what fraction is pending embedding?
- is candidate recall degrading as the corpus grows? (measurable without labels — see below)
- which stages actually ran, which failed OPEN, which were bypassed?
- what are the embed / vector-search / rerank / total latencies, per stage?
- are the score, margin and no-answer distributions drifting?
- which tenant, backend, ranking profile, index size and model version produced this behaviour?

Three primitives unlock all of it, in dependency order.

**1. Profile identity on every event.** A `profile_id` covering candidate-pool configuration,
fusion mode, lexical normaliser and weight, closet scale, rerank model/backend/blend, and index
version. Without it no drift signal is interpretable and no calibration can state what it is valid
for — an abstention threshold should say "valid for profile X", never "valid for TEI".

**2. Stage outcomes, so failing open is visible.** Every stage records ran / bypassed / failed-open
with its latency. Reranking currently falls back to the fused order on error and says so only in a
log line — the exact defect class that shipped an inert reranker in a release and printed a full
table of "reranked" numbers that were the hybrid order.

**3. Implicit relevance feedback — the one that scales.** Return a `search_id` with every recall
and accept it on `am_get_drawer`. Then an agent fetching a memory in full after a search is a
click; an immediate reformulation is a miss; abandonment is a miss. Web search has run on this
signal for twenty-five years. No agent-memory benchmark can produce it, because a benchmark has no
users — and it is the only source of relevance judgement that grows with usage instead of with our
labelling budget. It also measures the thing that actually matters: whether agents keep using
recall because it earns its place in their context.

Pool-recall degradation is measurable without labels too. If the cross-encoder frequently promotes
a candidate from deep in the fused order, the pool boundary is binding and should grow; if it never
promotes below rank ten, the pool is oversized and is being paid for in latency. That is a
self-tuning signal for the candidate pool, from production traffic, with no gold anywhere.

The loop the product actually needs is serve → observe → detect drift → shadow alternatives →
canary → promote or roll back. Offline eval sits inside that loop; it does not own it.

And "every capability exercised" should not mean equal traffic — `am_status` should outrank
`am_delete_wing` by orders of magnitude. It should mean every enabled component proves it ran,
exposes its cost and its effect, and can be turned off when it adds neither.

## Unused core capabilities — what the palace offers and nobody calls

Audited 2026-08-20 against a live palace of 80 drawers across 8 wings, one day after a full reset.
The drawer count moves by tens per day while sessions refile, so read it as a snapshot; the zeros
below were re-confirmed against the same palace at 80 drawers.
The server registers 41 tools; roughly eight are in regular use. What is built, working, and idle:

| capability | live count | why it is idle |
|---|---|---|
| closets | **0** | Built by `am_mine` only, and mining is retired for now — the prior it feeds measured harmful on mined corpora (~0.10 MRR) and `CLOSET_BOOST` defaults to 0. The summary index itself is untested against a curated corpus, which is a different question from the ranking prior and has never been asked. |
| hallways | **0**, and structurally so | Not "nobody ran the build step" — `am_recompute_graph` was run across all 8 wings on 2026-08-20 and returned `hallways: 0, entity_tunnels: 0`. Hallways are entity co-occurrence, and 82 of 82 drawers have an empty `entities` column: `Service.Add` (`internal/palace/service.go:305`) builds its `Drawer` literal without one, and the only code that ever calls `extractEntities` is `internal/palace/mine.go`. Mining is retired, so nothing writes the input. |
| tunnels | **0** | Explicit tunnels have never been created by a session, and derived ones cannot exist: `entityTunnelsForWing` (`internal/palace/tunnel.go:180`) takes hallways as its input, so it inherits the zero above. The craft/project wing split is exactly what explicit tunnels are for, and that half is available today. |
| skills (centralised) | 2 | Was **0** for the project's whole life: every session reported `am_list_skills` empty and fell back to generic conventions while the bootstrap called loading them a hard gate, so the gate passed vacuously. `memory-orchestration` and `writing-memories` were published 2026-08-20 and sessions began loading them the same hour. `effective-go` and `cqrs` — the two this repo's protocol names by name — were published the same day, so the catalogue holds 4 and the promise in `AGENTS.md` and `CLAUDE.md` is true for the first time. |
| anchors | 5 | Used, and the cross-repo verdict bug that deleted memories is fixed. Adoption is still incidental rather than routine. |
| knowledge graph | 41 triples | Genuinely in use by sessions since the reset, but its job is undecided — ADR-004 exists to make supersession its acceptance criterion rather than recall. |
| `am_merge_wing` | first use 2026-08-20 | Folded two derived wings into one after registrations corrected. Worked exactly as documented; simply nobody had needed it before. |

Three of these are worth acting on, in order:

1. **Make the catalogue reachable on a fresh install.** The four skills exist in *this* palace
   because they were pushed by hand. A fresh `aiagentmemory install` seeds no skills at all, so
   `AGENTS.md`'s claim that `effective-go` lives in the centralised catalogue is true here and false
   everywhere else — the reachability defect one level up: the capability is finished and nothing
   selects it for a new workspace. `update-skill` is not this; it refreshes local markdown. What is
   missing is a seed path (skill bodies in the repo tree, pushed at install) plus the gate that
   naturally follows: a test failing when the protocol names a skill the tree does not carry.

2. **Decide the entity graph: feed it or retire it.** This is the repository's own named defect,
   and the largest instance of it yet. Hallways, derived entity tunnels and the entity half of
   `am_traverse` are written, tested and reachable by tool call — three MCP tools and a rebuild
   command — and all of them return nothing, because their single input is written by one retired
   code path. `am_mine` calls `extractEntities`; `Service.Add` does not, so every drawer filed by
   `am_add_drawer` or `am_diary_write` carries an empty `entities` column, 82 of 82 today. The tests
   pass because they exercise the component (given entities, compute hallways) rather than the
   selection (does anything ever produce entities), which is the same shape as the eval arm that
   won four tables while being unreachable from production.

   Two honest options, and the measurement should pick between them. **Feed it:** call the existing
   entity extractor on the normal write path, so hallways and derived tunnels describe the curated
   corpus rather than a mined one — cheap, since `closetEntities` already exists and runs on
   content we already hold. **Retire it:** delete the hallway/entity-tunnel derivation and the two
   tools that expose it, and keep explicit tunnels only. What is not an option is leaving three
   tools in a catalogue of 41 that answer every call with an empty list, because an agent reading
   the catalogue cannot tell that apart from a palace that simply has no links yet.

   Whichever way it goes needs a gate that fails when the input dries up again — a test asserting
   that a drawer written through the normal path carries entities, which fails today and is
   therefore the right red test to open the ADR with.

3. **Use explicit tunnels for the craft/project split.** Independent of the entity graph above and
   available now: a craft lesson learned in a project incident should carry a tunnel back to the
   incident that taught it, so a rule that gets challenged can be traced to its evidence. The
   protocol tells agents tunnels exist and never says when to weave one, which is why the count is
   zero on the explicit side too.

## Verified defects in the portability paths (found 2026-08-20, not yet fixed)

Found while asking a plainer question — *where does the palace's content actually live, and could we
get it back?* Both were reproduced, not inferred.

**A wing bundle restored beside its original duplicates every diary entry.** `cmd/server/wing.go`
states the feature as "a bundle is contents, not a place, so the same file can be restored beside its
original". It cannot. A diary drawer's id comes from `diaryEntryID`, which mixes in a per-write seed;
export drops the id and import re-mints it with `DrawerID`, a different hash over different inputs,
so the restored row never matches the original. Reproduced on a scratch wing: one entry written
normally, exported, imported back into the same wing — two rows, distinct ids, one distinct content.
Against the live palace that is 52 diary drawers doubling. Re-importing the same bundle into a
*fresh* wing is idempotent, which is why this was never noticed.

A second edge sits behind the same seam: `DrawerID` drops agent and topic, so two diary entries with
byte-identical content in one wing collapse to a single row on import — the opposite failure, and it
silently violates the append-only journal guarantee `diaryEntryID`'s own doc comment states.

**On a self-hosted server, no export path reaches skills, the knowledge graph, anchors, or
cross-wing tunnels.** `wing export` structurally cannot carry them — they are not bundle record
kinds. The one path that does, the data-subject archive, is mounted only on the multi-tenant
dashboard route; `serveLocal` mounts `/mcp`, `/import`, `/stats` and `/healthz` and nothing else. So
the four centralised skills, which are user-authored and seeded by no repo file, are reachable by no
backup the operator can run. `~/.claude/bin/palace-backup` works around it by copying the database
directly, which is a workaround and not the fix.

Related, and the repo's own named defect: `internal/importer` already handles a `kg` record kind,
preserving the validity window — and `wingbundle` has no such kind and never emits one. Half of KG
portability is finished and unreachable.

## The per-task acceptance guard has a false-positive mode

The guard added to every task's Acceptance fence — `! grep -qE "no tests to run|^FAIL|^--- FAIL"` —
fires when ANY package in a multi-package run reports no matching tests, even though another package
ran the task's tests perfectly well. ADR-004 T3 hit it: `./internal/palace/` ran all four,
`./cmd/server/` had none matching, and the gate called the run a failure.

`adr-verify` implements the same rule correctly and centrally: it fails only when a "nothing ran"
signature appears AND no evidence of a real run appears anywhere in the output. The per-task guards
predate that and are now both redundant and stricter than the thing they duplicate.

Removing all nineteen would invalidate every Verification Log entry taken under them (adr-lint
rejects a `done` whose logged command no longer matches), so it is a deliberate sweep rather than a
drive-by: strip the guards, re-run adr-verify on every completed task, commit between runs. Until
then, scope a multi-package acceptance to the package that holds the tests.

## A memory is several rows and most operations treat it as one

Found in production 2026-08-20 by a session correcting one of its own memories, and reproduced here
against the running server.

`am_update_drawer` rewrote chunk 0 of a three-chunk memory and reported success. Chunks 1 and 2
stayed live with the old text, individually embedded — and a search for the subject returned the
stale chunks ABOVE the correction, with nothing marking them retracted. A memory store whose
correction competes with the text it corrects on equal footing is worse than one that refuses the
edit, so `Update` now refuses when the drawer belongs to a multi-chunk memory and says what to do
instead.

Refusing is the safe half of the fix, not the whole one. Two things are still open:

- **Re-chunking on update.** The right behaviour is to replace the whole memory, but that changes
  how many rows exist and which ids they carry, which silently invalidates every anchor, tunnel and
  knowledge-graph fact pointing at the old ones. Doing it properly means deciding what happens to
  those references, which is an ADR rather than a bug fix.
- **A wing or room MOVE split the memory** instead of contradicting it — one chunk leaves and the
  rest stay. Fixed in the same place as the content case: every patchable field is one the chunks
  must agree on. Worth recording because this release sharpened the consequence — recall now
  defaults to the registration's wing, so after a split neither wing returns the whole memory and
  nothing marks what you get as a fragment.
- ~~**`Delete` has the same shape.**~~ **Fixed.** Reproduced — deleting the parent of a two-chunk
  memory left chunk 1 live, embedded, searchable and pointing at a parent that no longer existed —
  then fixed to remove the whole memory from either end, parent or child. A delete has no reference
  ambiguity to weigh, unlike an update: the caller is removing the memory, so removing all of it is
  what they asked for. The tool now reports how many chunks went.
- ~~**`am_update_drawer` cannot set `code_anchors`.**~~ **Fixed.** `ReplaceAnchors` swaps rather than
  appends, because the case it exists for is a memory being corrected: the old anchor pins the OLD
  text, so the staleness check meant to protect the memory is what marks the correction out of date.
  An empty array clears them, which is the honest option when a rewrite no longer points at any
  particular code.

Still open from this cluster: re-chunking on update (above), which stays an ADR rather than a bug
fix because it changes which ids exist.

## The ADR evidence chain depends on a tool outside the repository

Raised by review, and worth stating plainly rather than leaving implicit.

`adr-verify` lives in a personal harness directory, not in this tree. It is what runs each task's
Acceptance fence, writes the Verification Log entry, and — since the per-task guards were removed —
it is the only thing that fails a run whose `-run` filter matched no tests. CI cannot run it, and a
reviewer checking out this PR cannot read it.

So the acceptance commands recorded in the task files are reproducible by anyone, but the RULE that
makes a passing one meaningful is not in the artifact it certifies. Two ways out, neither taken yet
because both are a decision rather than a fix: vendor the checker into the repo so CI and reviewers
share it, or put the nothing-ran assertion back into the fences in a form that does not misfire on
multi-package runs — `go test -v` plus a check that at least one `=== RUN` appeared would do it
without the exit-code trap the first version had.


## From ADR-006 T3 (a knob that does nothing must say when)

- **A conditional-documentation gate over the compose files and `.env.example`** — `--bm25-weight`
  now names `--fusion=rrf` in its Usage, and `TestDiscoveredPairsAdmitTheirCondition` holds that
  line for every pair the sweep discovers. The operator-facing files are not covered.
  `TestDocumentedEnvVarsAreRead` already runs the READ direction — a variable a compose file
  advertises must be read by the server, which on its first run found a shipped rerank pool of 20
  the server had never read. The conditional direction is the unwritten half: `BM25_WEIGHT` can sit
  in a compose file beside `FUSION=rrf` with nothing saying it is inert there, and every existing
  gate passes. Wider than T3 because it needs a parser for three file formats rather than one flag
  table.

  Filed 2026-08-20 because T3's Out of Scope pointed here and this file did not hold it — the
  pointer resolved to a real file and the item was in neither, which is a punt that reports fine
  forever. `adr-debt` follows the pointer; it does not check that the destination received anything.
