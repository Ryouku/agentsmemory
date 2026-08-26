-- +goose Up

-- top_score cannot answer the question it looks like it answers.
--
-- It stores the FUSED score of the best hit, and under the shipped `FUSION=rrf`
-- the fused score is reciprocal rank fusion: 1/(60+rank) summed over the vector
-- and lexical orderings. That is a RANK ENCODING. Magnitude is discarded at
-- retrieval, so a page whose best hit is a perfect answer and one whose best hit
-- is unrelated produce nearly the same number. Measured 2026-08-25: the
-- achievable range for a top-1 hit is 0.0275..0.0328, and a real served page
-- carried 0.03252 / 0.03227 / 0.03200 — every candidate within 1.6%.
--
-- This matters because ADR-001 already measured which signal DOES separate a
-- recall that answered from one that did not, over 61 cases:
--
--     top-1 cosine distance     answerable 0.401  unanswerable 0.423
--     top-1 cross-encoder score answerable 0.891  unanswerable -3.832
--
-- The distance distributions overlap so completely that "no threshold on cosine
-- distance separates them at any value". The cross-encoder's medians are ~4.7
-- apart, which is real signal — and it is the one number the row did not keep.
--
-- top_rerank_score is therefore ADDED rather than top_score being redefined.
-- top_score has a live reader: RecallStats averages it into avg_top_score, which
-- am_recall_stats reports. Redefining a column mid-history makes every row before
-- the change silently incomparable with every row after it, and nothing in the
-- report would say so. A new column means old rows are honestly absent (NULL-ish
-- 0 with reranked=0 to distinguish) rather than quietly wrong.
--
-- It is 0 when no cross-encoder ran, which the existing `reranked` flag already
-- distinguishes from "ran and scored 0" — a genuine possibility, since these are
-- logits and 0 is mid-range, not "no match".
ALTER TABLE search_events ADD COLUMN top_rerank_score REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE search_events DROP COLUMN top_rerank_score;
