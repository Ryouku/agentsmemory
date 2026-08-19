-- +goose Up

-- One row per recall. The palace can already say how much it REMEMBERS; this is
-- what lets it say whether remembering is WORKING — which is a different
-- question, and the only one an operator actually cares about.
--
-- The useful signal is not the count of searches. It is the share that came back
-- empty, per wing: a wing that answers most of its queries is earning its keep,
-- while a wing that keeps returning nothing is either mis-scoped or has never
-- been written to. Both are actionable, and neither is visible from drawer counts.
--
-- The query text is kept (truncated by the caller) because "which questions found
-- nothing" is the single most useful output here: it names the memories the team
-- should have written and did not. It is the team's own text, in the team's own
-- database, deleted with the team.
CREATE TABLE search_events (
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

-- Every read is "this team, this window, grouped by wing", so the index matches
-- the access pattern rather than being a reflex on the primary key.
CREATE INDEX idx_search_events_team_time ON search_events(team_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_search_events_team_time;
DROP TABLE search_events;
