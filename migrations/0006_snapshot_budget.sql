-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5.
--
-- balance_snapshot is created here to keep the numbering aligned with §3.7. The
-- snapshot and net-worth features are stage 06; only `budget` is used before then.
-- account.HasValues already reads the table, so a parent account cannot acquire a
-- snapshot behind the rollup's back.

-- Either an asserted value, or a quantity to be priced. Never both, never neither.
CREATE TABLE balance_snapshot (
    id                 INTEGER PRIMARY KEY,
    user_id            INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    account_id         INTEGER NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    period_month       TEXT NOT NULL,
    amount_minor       INTEGER,
    currency           TEXT REFERENCES currency(code),
    quantity_nano      INTEGER,
    base_amount_minor  INTEGER,
    base_currency      TEXT REFERENCES currency(code),
    fx_rate_id         INTEGER REFERENCES fx_rate(id),
    note               TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    CHECK ((amount_minor IS NULL) <> (quantity_nano IS NULL)),
    CHECK (period_month LIKE '____-__')
) STRICT;
CREATE UNIQUE INDEX idx_snapshot_unique ON balance_snapshot(user_id, account_id, period_month);
CREATE INDEX idx_snapshot_period ON balance_snapshot(user_id, period_month);

-- One row per category per month, so a seasonal budget works. A year is seeded
-- from one figure per category; the UI never asks for 216 numbers.
CREATE TABLE budget (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    category_id   INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
    period_month  TEXT NOT NULL,
    planned_minor INTEGER NOT NULL,
    currency      TEXT NOT NULL REFERENCES currency(code),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    CHECK (period_month LIKE '____-__')
) STRICT;
CREATE UNIQUE INDEX idx_budget_unique ON budget(user_id, category_id, period_month);

-- +goose Down
DROP INDEX IF EXISTS idx_budget_unique;
DROP TABLE IF EXISTS budget;
DROP INDEX IF EXISTS idx_snapshot_period;
DROP INDEX IF EXISTS idx_snapshot_unique;
DROP TABLE IF EXISTS balance_snapshot;
