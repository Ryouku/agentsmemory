-- 00025_kg_valid_to_idx.sql
-- The endedness index. kg_triples was indexed on (team_id, subject),
-- (team_id, object) and (team_id, predicate); a query filtering on valid_to
-- narrowed to the tenant and then walked every row it owns.
--
-- Only an EQUALITY test on valid_to belongs here. valid_to holds whatever
-- kg_add was handed (00010 stores temporal values verbatim), so the column
-- mixes '2026-08-25' with '2026-08-25T09:00:00Z' and SQLite compares TEXT as
-- bytes. An equality test against '' — the not-yet-ended sentinel — never
-- compares two temporal values, so format cannot reach it.
--
-- A RANGE over this column is a different matter, and this index makes it
-- FAST WITHOUT MAKING IT CORRECT: a date-only value sitting on a window's
-- lower bound sorts before the datetime form of that same bound and is
-- silently dropped. Speed is what would make it look right. Ranges stay in
-- Go, where inEffectAt normalises both sides per row; see ADR-026 §2b for the
-- measurement and the order in which that could be fixed (normalise on write,
-- backfill, only then index).

-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_kg_triples_team_valid_to ON kg_triples (team_id, valid_to);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_kg_triples_team_valid_to;
-- +goose StatementEnd
