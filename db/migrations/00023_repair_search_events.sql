-- +goose Up

-- Migration 00021 was released twice from unrelated histories: v0.0.45 used it
-- for a Free-plan cap change, while v0.0.86+ uses it to create search_events.
-- Databases that applied the first body therefore skip the second forever.
-- Repeating the desired schema at a new version converges both cohorts without
-- rewriting applied migration history or guessing which body version 21 ran.
CREATE TABLE IF NOT EXISTS search_events (
    id         TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    wing       TEXT NOT NULL DEFAULT '',
    room       TEXT NOT NULL DEFAULT '',
    query      TEXT NOT NULL DEFAULT '',
    candidates INTEGER NOT NULL DEFAULT 0,
    hits       INTEGER NOT NULL DEFAULT 0,
    top_score  REAL    NOT NULL DEFAULT 0,
    reranked   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_search_events_team_time ON search_events(team_id, created_at);

-- +goose Down

-- Intentionally do not drop search_events here. On a fresh database migration
-- 00021 owns the table, while on the affected cohort this migration owns it;
-- SQLite records only the shared version numbers, not that provenance. A
-- destructive Down from 23 to 22 would therefore break the fresh cohort by
-- removing a table whose owning migration remains marked as applied. DownTo(0)
-- still removes it through 00021's Down.
SELECT 1;
