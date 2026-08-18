-- +goose Up

-- A code anchor pins a memory to the code it is ABOUT, so the system can notice
-- when that code moves on without it.
--
-- This is the failure that makes a memory palace worse than no memory: a drawer
-- says "installer.go:950 pins CLAUDE_CONFIG_DIR", the line is fixed, and the next
-- session recalls the sentence with total confidence and acts on it. The memory
-- did not decay — the world moved, and nothing was watching.
--
-- What is pinned is a VERBATIM SNIPPET, deliberately not a line number: line
-- numbers shift on every edit above them, so anchoring to one would flag every
-- memory in the file on every commit and teach everyone to ignore the flag. The
-- snippet either still exists in the file or it does not, and `line` below is the
-- ANSWER (where it is now), never part of the question.
--
-- Verification happens on the client, because the server usually runs in a
-- container and cannot see the repository at all: `aiagentmemory verify` reads
-- the working tree and posts the verdicts back.
CREATE TABLE drawer_anchors (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    drawer_id   TEXT NOT NULL,
    repo        TEXT NOT NULL DEFAULT '',  -- optional label: which checkout this path is in
    path        TEXT NOT NULL,             -- repo-relative file path
    snippet     TEXT NOT NULL,             -- the verbatim code this memory is about
    snippet_sha TEXT NOT NULL,             -- sha256 of the normalized snippet, for dedupe
    -- unchecked: never verified. verified: the snippet is still there.
    -- drifted: the file exists, the snippet does not. missing: the file is gone.
    status      TEXT NOT NULL DEFAULT 'unchecked',
    line        INTEGER NOT NULL DEFAULT 0,  -- where the snippet was last seen (output, not input)
    checked_at  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

-- Search enriches a page of hits with their anchors (team + drawer), and the
-- verify command pulls a wing's anchors by status.
CREATE INDEX idx_drawer_anchors_drawer ON drawer_anchors(team_id, drawer_id);
CREATE INDEX idx_drawer_anchors_status ON drawer_anchors(team_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_drawer_anchors_status;
DROP INDEX IF EXISTS idx_drawer_anchors_drawer;
DROP TABLE drawer_anchors;
