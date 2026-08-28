-- ADR-042, PR #96 review finding B1. Record which provider orders this server has
-- already acted on, so reconciliation stops re-applying a decision that has not
-- changed.
--
-- Why the webhook design did not need this and polling does: a webhook fires ONCE
-- per event, so "apply whatever the event says" is idempotent in practice. A poll
-- fires every interval and sees the SAME order forever. Without a record of what was
-- already applied, the only idempotence left is "the provider's state has not
-- changed" — which is exactly the wrong invariant, because it re-asserts a past
-- decision over any local change made since.
--
-- The concrete defect this closes, reproduced before the fix: an operator runs
-- `set-plan --plan personal` to downgrade a workspace; `set-plan` writes only
-- teams.plan_id, so the subscriptions row still reads 'active' and applyActivated's
-- canceled-guard does not fire; the order is still PAID upstream; the next pass puts
-- the workspace back on Pro with a routine "1 activated" in the log. The operator's
-- rollback path and the reconciler fought, and the reconciler won 15 minutes later.
--
-- Keyed on the order's publicId and carrying the status that was applied, so a
-- genuine transition (ACTIVE -> CANCELLED) is still acted on while a repeat of the
-- same state is skipped.

-- +goose Up
CREATE TABLE IF NOT EXISTS billing_applied_orders (
    order_id   TEXT PRIMARY KEY,
    status     TEXT NOT NULL,
    team_id    TEXT NOT NULL,
    applied_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS billing_applied_orders;
