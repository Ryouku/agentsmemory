-- +goose Up

-- Lower the Free plan's monthly request cap from 10,000 (set in 00003) to 1,000.
-- Product decision: the free tier is a trial-sized allowance, not a production
-- quota — 1,000 metered requests/month is enough to evaluate agent memory, and
-- teams running agents in production upgrade to Pro. Migrations are append-only,
-- so rather than rewrite 00003 this migration adjusts the same column: a fresh
-- database applies 00003 (10,000) then this (1,000) and converges to 1,000, and
-- an already-migrated database is brought to the new value in place. Only the
-- Free plan is touched (code = 'personal'); Pro (1,000,000) and the operator-only
-- Unlimited tier (-1) keep their caps.
UPDATE plans SET monthly_request_cap = 1000 WHERE code = 'personal';

-- +goose Down

-- Restore the original 10,000 cap so Up -> Down returns 00003's state exactly
-- (migrations_test cycles Up -> DownTo(0) -> Up and expects reversibility).
UPDATE plans SET monthly_request_cap = 10000 WHERE code = 'personal';
