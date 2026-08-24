-- 00024_drawers_team_parent_idx.sql
-- ADR-024 made the logical memory a retrieval unit, which put a new lookup on
-- EVERY recall: resolve a set of memory roots to all of their stored chunks
-- (Repo.MemoryChunksByRoots). It runs twice per search in BOTH arms — once to
-- hydrate result memories (or to collapse candidates, under the treatment) and
-- once to resolve anchors at memory granularity — so this is default-path cost,
-- not experiment cost.
--
-- That lookup reaches rows two ways: a root by its own id, and a child by
-- parent_id. Only the first was indexed (the primary key). parent_id had no
-- index at all, so the planner fell back to the team_id prefix of an unrelated
-- index and examined every drawer the tenant owns — on the measured hosted
-- workspace, ~7,361 rows per call, twice per recall, to return ten memories.
--
-- The index alone does NOT fix that, and this migration ships with the query
-- rewrite for that reason: `id IN (...) OR parent_id IN (...)` cannot seek both
-- sides of the OR in one pass, so it degrades to a scan whatever indexes exist.
-- Repo.MemoryChunksByRoots is a UNION ALL of two lookups so each branch can use
-- its own index; this one serves the parent_id branch. Additive and
-- non-destructive: no data moves and no query depends on it for correctness.

-- +goose Up
CREATE INDEX idx_drawers_team_parent ON drawers (team_id, parent_id);

-- +goose Down
DROP INDEX idx_drawers_team_parent;
