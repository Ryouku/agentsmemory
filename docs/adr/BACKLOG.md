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

## From ADR-002 (anchor the lexical score)

- **Growing the eval corpora past the cases the original tables used** — every ADR-002 table re-runs the
  questions its saved case file holds, which is what makes a re-run comparable and also what caps it:
  growing a corpus means asking questions those tables never asked, so it is a new experiment rather than
  a longer run, and nothing measured on the grown one is comparable to the committed evidence. Check first
  whether the corpus those tables ran against still exists to be grown — mining is retired and the palace
  has been reset since. More than one ADR punts this, so collect them before starting.
- **Corpus-wide term statistics, so the lexical score stops depending on who else was retrieved** —
  `bm25ScoresAndCeiling` derives N, document frequency, IDF and average document length from the candidate
  pool it is handed, so a candidate's raw BM25 and the anchored ceiling `C` both move when a sibling is
  added or dropped; ADR-002 buys independence from *which candidate won* and explicitly not this. Blocked
  on there being no term-statistics store, and on nobody having measured how far `raw` and `C` actually
  travel between pools, so the size of the defect is unknown. It is the store a lexical first stage needs too.
- **A lexical first stage, so BM25 can nominate candidates instead of only reordering them** — every arm
  re-orders one pool nominated by vector distance, so no lexical change can alter what is reachable, only
  what is on top. There is nothing to nominate from: BM25 is computed in memory over the pool's documents
  and nothing in the tree indexes terms. The measured headroom is small and stale — 1 gold of 40 never
  entered the pool — and it is the same retrieval-ceiling number the candidate-pool section turns on, so
  it is worth doing once that ceiling is re-measured and lexical-only misses are a named share of it.
- **Recalibrating the closet boost against the rescaled fused range** — `rankFused` adds the boost in
  absolute units on top of the fused score, so an anchored normaliser, which only shrinks the lexical term,
  inflates a fixed boost by exactly `1/s`, `s = 1 − w(1 − a)`; this palace has already lost recall@1 from
  92% to 17% to that class of scale mismatch. There is nothing to recalibrate from yet: after ADR-003 T1
  the arms that would measure it carry no prior, and the anchored tables have not been run. Note the
  capability table above is ahead of the tree — the flip to a default of 0 has not landed, `config.Default()`
  still ships `ClosetBoost: 1` — so the default path is still boosted.

## From ADR-003 (retire the closet prior)

- **A preselected contrast for any arm pair** — `ClosetDelta` is hard-wired to one comparison,
  `hybrid+closet` minus `hybrid` over one category; the arms table's `vs best` verdicts are still
  measured against a baseline chosen from the same table. Generalising it means carrying ADR-007's
  per-contrast reporting rules too — `not measured` on a vacuous contrast, no aggregate across arm
  scopes, a case-set id per run — or the framework prints the numbers ADR-007 forbids. The first
  real customer would be ADR-002's normaliser comparison, which has to be re-taken on post-T1 arms.
- **A corpus sized for the question, and a genuinely curated palace to measure against** — both
  corpora in every closet run are whatever happened to be filed, not a design. ADR-003's curated
  cells carry a floor of 10 admitted cases and a wing that may not clear it, and those floors were
  fixed against a pre-reset palace. Growing it by hand is labelling budget; growing it by mining
  produces the mined corpus, which is the side being contrasted against. Nothing here moves until a
  decision turns on the curated cell rather than on what the docs say about it.
- **A doc-vs-code gate for every configurable default's value** — the pattern exists for exactly one
  hand-picked number: `TestCatalogSizeIsWhatTheReadmeClaims` pins the README's tool count to the real
  catalogue, and ADR-003 T5 copies it for one knob. The gates nearby prove a different thing — that a
  setting is settable and read (`TestEveryConfigFieldIsPopulatedAndRead`, `TestEveryFlagIsRead`,
  `TestDocumentedEnvVarsAreRead`), never that the number printed beside it ships. Blocked on
  extraction: a default appears in README prose, a flag table, the landing doc and the web glossary.
- **Closet summary concatenated into the indexed text at mine time** — the published +9.4% recall
  variant ADR-003 cites as corroboration and deliberately never re-derives. It is a different
  mechanism at a different stage from the rank-time prior: it changes what gets embedded, so it
  costs a re-index of every mined source and cannot ride along with a default flip. Blocked on
  having closets at all — the count was 0 on the 2026-08-20 audit and `am_mine` is idle — and on
  whether a gain measured on someone else's indexing unit survives our chunking and our model.
- **Normalising the closet boost by source fan-out** — divide it by how many drawers share the mined
  source, so one closet hit cannot lift a fifty-part session at once. ADR-003 calls this the most
  direct answer to the amplification argument it rests on, and rejects it there only because it is a
  new ranking formula with no run behind it: it has to be measured against a settled default rather
  than folded into the flip. It cannot be measured at all while no closets are filed, and nothing
  says what the divisor should be — fan-out, its log, or a cap.
- **Choosing the closet prior automatically from corpus composition** — scale it by the share of
  curated versus mined drawers, or by closet coverage, instead of shipping one global default.
  ADR-003 rejects it as unmeasured and names the precedent: `BM25_WEIGHT=auto` was an adaptive rule
  invented without a table, and it measured worse than the fixed weight on paraphrase queries until
  IDF weighting was added. It needs both corpus types measured first, and nobody has defined
  composition — drawer provenance, closet coverage: neither is a quantity the server reports today.

## From ADR-005 (deliverable handoffs)

- **The new-wing refusal on `am_diary_write`** — `am_add_drawer` refuses a first write into an empty
  wing when the room is `inbox`; the diary path has no equivalent check. A diary entry goes to the
  session's OWN wing, so the mis-naming the refusal exists for has no route in: of the 217 drawers
  measured 2026-08-20, both malformed wings were first written through the inbox path, not the diary
  one. Unknown is whether a registration carrying the wrong default wing produces the same orphan by
  another route — one observed case is what would make this worth building.
- **Mid-session inbox delivery** — `am_status` names a waiting inbox at wake-up only, so an item
  filed while a session runs stays invisible to it. Tagged `permanent` on a false premise and
  corrected: the server serves streamable HTTP and the MCP library exposes
  `SendNotificationToClient`, which nothing in this tree calls, so the transport can carry a push.
  What is genuinely unknown is the client half — whether a given harness surfaces an unsolicited
  notification to the model mid-turn — and that is answered by testing a harness, not by reasoning.
- **An inbox count for wings other than the session's own** — the `am_status` `inbox` block counts
  only the registration's default wing, deliberately: every extra count dilutes the one that
  matters, and nobody has asked for the others. It is not a purely additive change if it is ever
  wanted — ADR-008 T4 falsifies its isolation check by making one party's inbox count include
  another's, so a cross-wing count would need that test restated as scoping rather than absence.
  Worth revisiting when a surface exists that has to watch several wings at once.
- **Marking an inbox item read or closed** — the convention closes an item out by filing what was
  found, so a handled lead and an untouched one look identical to the next session and stale items
  get rediscovered. It needs per-drawer state ADR-005 does not introduce. ADR-010 (Proposed) is the
  nearest mechanism — a validity window plus a required reason — but it names no inbox, and
  read-versus-open is a different axis from current-versus-ended: an item can be read, still true,
  and still waiting. Whether one mechanism serves both is undecided.
- **A repository gate over centralised skill text** — ADR-005 T3 put the handoff naming rule into
  the two centralised skills, and no exit code here can prove it is still there: skill bodies live
  in the palace, not the tree, so the edit was accepted on a human sign-off recording each skill's
  version before and after. Blocked on the seed path described under unused core capabilities
  above; once skill bodies ship in the tree, a gate can read them the way
  `TestProtocolTextTeachesTheInboxConvention` reads the shipped protocol files.

## From ADR-007 (no number without its population)

- **The `measured` / `no effect` / `not measured` status over every preselected contrast** — ADR-007
  T2 gives the closet row a status derived from whether the corpus holds any closets at all. Nothing
  generalises it yet, because the closet pair is the only preselected contrast the eval computes;
  every other verdict it prints is against the table's own best arm. Nor is the generalisation
  mechanical: the input check is mechanism-specific — one corpus count for the closet prior, and no
  other arm pair has an equivalent single question. Revisit when a second preselected pair exists.
- **Comparing two eval runs by case-set id** — once a run stamps a content-derived case-set id into
  its record, a command could place two of them against each other: same id, same questions;
  different ids, and it refuses. Most of the value is the refusal, which is why this is not urgent —
  the id in the table header already makes a mismatch visible to whoever reads it. What a cross-run
  comparison should then compute is undecided: the paired bootstrap pairs arms inside one run over
  shared cases, and two runs over one case set still differ by corpus, configuration and code.
- **The same rule over `am_recall_stats`** — ADR-007 governs what an eval table may claim; the
  production statistic makes the same kind of claim with no population attached. Its rows span
  configurations, corpus sizes and code versions — `SearchEvent.Reranked` changes meaning at ADR-006
  T4's fix, so a rate averaged over that cutover counts two different things. Blocked on events
  carrying an identity to partition on, the profile-identity primitive this file already names.
  Whether the honest form is partitioning, refusing, or reporting `not measured` per stage is open.
- **Populating closets, so the closet contrast has an input** — `closets` is empty on both palaces
  the 2026-08-20 eval tables were taken on, so `hybrid` and `hybrid+closet` are the same arm and
  ADR-003's truth table is read off a comparison that never ran. Closets are built by `am_mine`
  alone, mining is retired, and ADR-003 tags closet mining and curation permanent out of its own
  scope, so nothing owns getting one populated. Which corpus would count is the open part: the prior
  measured harmful on mined transcripts, so a curated palace is needed and none has been defined.

## From ADR-008 (exercise the palace end to end)

- **Cross-WORKSPACE isolation is one scenario, not a class gate** — `TestScenarioAnotherWorkspaceSeesNothing`
  stands two workspaces on one database and proves four read tools do not cross the tenancy boundary, and one
  further test does the same for `am_kg_query`. Five routes out of 41 registered tools: no mutation is asked,
  nor any by-id route where the caller already holds another workspace's drawer id, nor anything that makes a
  tool added tomorrow answer at all. The wing boundary has that gate — `TestEveryEnumerationHonoursTheRegistrationWing`
  — and the outer boundary, which is tenancy, does not. Deferred out of ADR-008 T4 as deserving its own scenarios.
- **Concurrent mutation by two parties is untested, and the harness cannot honestly test it yet** — every
  multi-party scenario in `internal/mcptest` acts in sequence, so nothing covers two registrations updating or
  deleting one memory at once. Two things are missing and the second is the blocker: there is no statement of
  what a race should do — last write wins, refuse, or supersede, which is ADR-010's question — and
  `internal/mcptest/harness.go` opens its SQLite without the server's `dbPragmas`, so it runs with no WAL and
  `busy_timeout` at 0 where the server waits five seconds. A test written there measures a database we do not ship.
- **Real-time multi-agent collaboration — two sessions mutating concurrently and observing each other live**
  — deferred out of ADR-008, whose three parties act in sequence. The shipped protocol states today's behaviour
  plainly (`clients/claude-code/bootstrap.md`: an inbox count is taken at wake-up, and "an item filed while you
  are running will not appear, because nothing pushes it"), and ADR-005 punted mid-session inbox delivery for
  the same reason. ADR-008 calls this the continuity spec's subject; no such spec is in the tree, and `WAVE.md`
  puts it outside wave 2 as `/spec-write` work whose requirements are openly undecided.
- **The CLI `mcp` adapter has no parity gate against the HTTP one** — the single divergence found by hand is
  closed (`parseArgsWithWing`, `TestCLIWingDefaultsLikeARegistration`), by giving the operator a wing default
  rather than by reading `SEARCH_SCOPE` as planned. The class is untouched: `readOnlyTools` mirrors 23 of the 41
  registered tools and nothing checks that any of the 23 answers as its HTTP twin does. ADR-008 pointed the gate
  at ADR-006, whose T4 fixed the instance and forwarded the general case here — so it was pointed twice and
  filed nowhere. Cheap while `internal/mcptest` is fresh: drive its scenarios through the CLI dispatch as well.

## From ADR-009 (tune against your own corpus)

- **The crosslingual eval style has never been run, and no ADR owns it** — `--style crosslingual` is
  implemented (`cmd/server/eval.go`, `CatCrossLingual` in `internal/palace/eval.go`) and appears in
  no table. ADR-009 T1 punted it beside `temporal` and `absent`, but only those two have homes:
  ADR-004 runs `temporal`, ADR-001's tasks run `absent`. ADR-003 excluded crosslingual from its
  deltas as dominated by the lexical weight — an assumption about a mode nobody has measured. Worth
  one run on a corpus large enough for arms to separate, to see whether ADR-002's knobs cover it.
- **A surface for a tuning result, once there is more than one** — `agentsmemory tune` (ADR-009 T3)
  prints its record and writes a file; nothing displays it, and there is nowhere obvious to put it.
  The dashboard in `internal/web` belongs to the multi-tenant path, while a self-hosted server mounts
  only `/mcp`, `/import`, `/stats` and `/healthz` (`serveLocal`, `cmd/server/main.go`) — so the
  operator who runs `tune` is exactly the one with no web surface. Blocked on `tune` existing at all,
  and worth building only once an operator has several runs to compare.
- **Tuning per wing rather than per install** — ADR-009 tunes one configuration for a whole server,
  yet wings hold corpora that differ as much as two installs do: a craft wing of short lessons and a
  project wing of long incident notes are not the same retrieval problem. Whether the optimum
  actually differs by wing is unmeasured, and that measurement comes first — a per-wing knob that
  lands on the same values everywhere is cost with no return. It also needs an answer for wings too
  small to hold any cases out.

## From ADR-010 (supersede, do not overwrite)

- **Ordering a supersession chain when history is asked for** — ADR-010 T3 returns the chain newest-first behind `include_history`, and stops there: nothing decides whether a history response should be RANKED by relevance, or by what, once a chain runs past a handful of records. Filed because T3's Out of Scope pointed at ADR-004 as "it owns ordering" and that ADR holds nothing of the kind — it is Accepted, it measures where a stale drawer lands in DEFAULT recall as the gate on populating the graph, states "No MCP surface change" and "production ranking unchanged", and never mentions history at all. `include_history` does not exist until T3 creates it, so no ADR owns this yet and the pointer resolved to a real file that could not have received it.
- **Full event sourcing of the whole store** — an append-only log as the source of truth with current state as a projection: the stronger form of the validity window ADR-010 chose instead. Rejected there on risk rather than on merit — drawer identity already carries vectors, chunking and anchors hanging off it, and rebuilding that as a projection is a rewrite the window's benefit does not pay for. The stated trigger is a SECOND consumer of history; today the only one is the explicit history flag on recall, and nobody has written down what else would read the log. Revisit when that second consumer exists, not on principle.
- **Validity windows on diary entries** — ADR-010 gives drawers `valid_to`, `superseded_by` and a required reason; diary entries get none of them, deferred on the ground that a diary is append-only by construction so nothing overwrites an entry. This file already records the counter-evidence: `DrawerID` drops agent and topic, so two byte-identical entries in one wing collapse to a single row on import, which the portability section above calls a silent violation of the append-only guarantee `diaryEntryID`'s own doc comment states. Append-only-by-construction is therefore the premise to check first, not the reason to skip the work. The retraction half is untouched either way — an entry whose decision later reversed stays current and competes with its correction, and since there is no way to mark one ended, no instance has ever been recorded.
- **Structured reasons — a taxonomy of why something ended** — ADR-010 makes `reason` required free text on every retraction and on `am_kg_invalidate`, deliberately uncategorised, because a taxonomy chosen before there are reasons to classify is a guess. What would settle it is the corpus that field produces — median reason length plus a human reading a sample, which ADR-010 measures and which does not exist yet. The risk it would address is recorded there already: a required field an agent fills with "obsolete" buys nothing. Better tool prompting is the first remedy; a closed set only if the writing stays uninformative once there is writing to read.

## From ADR-011 (anchor prompting — withdrawn)

- **Retroactive Class-A classification of existing memories** — 179 of 270 sampled drawers (66%) make a
  claim the repository could settle and 165 of those carry no anchor: the coverage gap ADR-011 measured
  and left open. The labelled sample is the training data and it exists. Blocked on having no consumer —
  nothing reads a classification today, and building the classifier first is the unreachable-capability
  defect in its usual shape. What the sample cannot say is how far labellers agree, since four of them
  took disjoint slices and no drawer was labelled twice, or whether the 66% holds outside this palace.
- **`verified` should mean less when only a declaration line matched** — the cheapest compliant snippet is
  the symbol's declaration, the line a behavioural change never touches, so it reports `verified` on every
  recall while the behaviour it pins moves underneath — worse than no anchor, because it destroys the
  reader's calibration rather than leaving it absent. ADR-011 found it, and it is the one carve-out from
  that ADR's permanent "no change to how anchors are checked or reported". Not known whether a declaration
  is cheaply distinguishable across languages, nor whether the fix is a weaker verdict or a fifth status.

## From ADR-006 review (findings filed rather than fixed, 2026-08-20)

- **The mode-scope sweep notices an empty pair set, not a short one** — `TestDiscoveredPairsAdmitTheirCondition`
  fails when the sweep discovers zero pairs, and `TestModeScopedKnobsAreDiscovered` pins the one known
  bm25/rrf pair. Any OTHER pair going missing is unnoticed. Four concrete ways it can shorten silently:
  the fixture's rerank factory returns nil so the three rerank knobs are inert in every cell and never
  produce a pair; `RerankTimeout` is not in `sweptKnobs` at all; `values[0]` is assumed to be the
  effective default and never checked against `config.Default()`; and only pairwise cells are run, so a
  three-way interaction cannot appear. Worth doing when a knob's inertness matters more than the two
  already found: give each knob an enabling baseline plus an observable fake, and assert the expected
  inventory rather than a non-zero count.
- **Nothing stops `unobservableKnobs` from excusing an observable knob** — the exemption list requires a
  non-empty reason, no simultaneous sweep entry, and no stale field name. It does not require the knob to
  actually be unobservable. Removing `ClosetBoost` from `sweptKnobs` and adding it to the exemption list
  with any sentence passes. Two of the three current entries are questionable on the same ground:
  `RerankURL` is observable through the injected factory (`configureranking_test.go` already observes it)
  and `RerankTimeout` reaches the factory as an argument, so neither needs a live backend. The fix that
  would hold is mechanical rather than editorial: reject an exemption when varying its field changes the
  returned lines, the factory calls, or the ordering — the same test `TestFlagAliasesAreNecessary` applies
  to the alias table, where an alias is admissible only where no mechanical counterpart exists.
- **`fieldsReadBy` sees direct selectors only** — `cfg.RerankPool` is found; `c := cfg; c.RerankPool` and
  a field read inside a helper the config is passed to are not, so the universe under-reports and the
  gate goes quiet for that field. Type-checking the receiver, or failing when the Config parameter
  escapes direct field access, would close it. Not urgent while `configureRanking` reads every field
  directly, and that is exactly the condition that will change without anyone noticing.

## From ADR-012 (the agent surface enforces the role it reports)

- **The read/write split is spelled in three places and nothing compares them** — `registrar.add` vs
  `addWrite` in `internal/mcpserver`, `readOnlyTools()` in `cmd/server/mcp.go`, and
  `readOnlyRemoteTools` in `clients/claude-code/mcpcall.go`. Each is a hand-kept mirror of the same
  classification, and a tool added to one is not added to the others. ADR-012 rejected deriving the
  server's guard from the CLI list because it points the dependency the wrong way; the honest fix is
  the reverse — export the classification from the catalogue (`CatalogEntry.Write` now carries it) and
  have both adapters read it instead of restating it. Cheap now that the field exists.
- **A third privilege level, finer than read/write** — `delete_wing` is already gated by deployment
  mode rather than by role, which is a proxy for "is this a shared workspace". A real admin-only tier
  would replace that proxy. Blocked on evidence: nobody knows how the three roles are actually used,
  and a tier designed against a guess is a tier that gets granted to everyone.
- **Writes and refusals are unlogged** — a refused write returns a message to the caller and leaves no
  record, so an operator cannot see that an agent has been failing its write-back for a week, and a
  successful write names no actor beyond the drawer's own row. The audit question is larger than
  authorization and should be taken as its own ADR, not bolted onto this one.

