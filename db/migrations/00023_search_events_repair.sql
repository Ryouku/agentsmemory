-- +goose Up

-- Recreate 00021's table on a database that recorded that version without its
-- effect.
--
-- THIS IS NOT HYPOTHETICAL. On 2026-08-23 the hosted palace answered
-- am_recall_stats with "SQL logic error: no such table: search_events (1)"
-- while am_list_anchors -- backed by 00022, the migration AFTER the missing one
-- -- returned rows written minutes earlier. So the version counter had moved
-- past 00021 while the table it creates was absent. Every explanation available
-- from the repository was checked and eliminated: the file ships in every tag
-- from v0.0.88, db/embed.go globs the directory so it cannot be left out, the
-- CREATE is unconditional, AutoMigrate is never called, no commit ever carried
-- 00022 without 00021, and goose panics on duplicate versions rather than
-- skipping one. How the database reached that state was never established. A
-- redeploy restored the table.
--
-- This migration exists so that the repair does not depend on the diagnosis.
-- CREATE TABLE IF NOT EXISTS is a no-op on every healthy database and fixes a
-- drifted one, which is the only shape of fix that is safe to ship to every
-- deployment when you cannot inspect them all.
--
-- WHY IT IS WORTH A MIGRATION RATHER THAN A RUNBOOK NOTE: palace.recordSearch
-- swallows its write errors deliberately -- "measurement that can break the
-- thing it measures is worse than no measurement" -- so a missing table costs
-- every recall statistic in silence while search itself keeps working
-- perfectly. Nothing announces the loss, and cmd/server/eval.go replays this
-- same table for realistic eval cases. A defect that reports nothing has to be
-- repaired blind or not at all.
--
-- The column list is 00021's, verbatim. If the two ever disagree, 00021 is the
-- source of truth and this file is the one that is wrong.
CREATE TABLE IF NOT EXISTS search_events (
    id         TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    wing       TEXT NOT NULL DEFAULT '',   -- the wing filter, '' when unscoped
    room       TEXT NOT NULL DEFAULT '',
    query      TEXT NOT NULL DEFAULT '',
    candidates INTEGER NOT NULL DEFAULT 0, -- neighbours the index returned
    hits       INTEGER NOT NULL DEFAULT 0, -- rows on the page the caller got
    top_score  REAL    NOT NULL DEFAULT 0, -- fused score of the best hit, 0 when none
    reranked   INTEGER NOT NULL DEFAULT 0, -- 1 when a cross-encoder ordered the page
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_search_events_team_time ON search_events(team_id, created_at);

-- +goose Down

-- Deliberately empty. 00021 owns this table and its Down drops it; dropping it
-- here as well would mean a DownTo(0) that destroys the table twice, and the
-- second attempt is the one that fails. A repair migration reverts to "the
-- state 00021 should have left", which is exactly what doing nothing gives.
SELECT 1;
