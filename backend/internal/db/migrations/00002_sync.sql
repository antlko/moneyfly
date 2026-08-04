-- +goose Up

-- Every synced entity gets the same shape: the (user_id, id) identity, the
-- last-write-wins version pair (lamport, device_id), a tombstone flag, and the
-- row itself as JSON.
--
-- Why JSON rather than typed columns: sync then has ONE code path instead of one
-- per entity, and adding a field is a client-side change. The columns the server
-- genuinely queries — for CSV export, imports, recurring generation, reports —
-- are pulled out as VIRTUAL generated columns, which cost no storage and cannot
-- drift from the data they are derived from.
--
-- Two invariants are baked into this schema:
--
--   * PRIMARY KEY (user_id, id) — scoping is structural, not a code check. Two
--     accounts can hold the same row id without ever seeing each other's data.
--
--   * NO foreign keys between synced tables, and NO unique constraints on
--     anything a client sends. Ops arrive in arbitrary order (a transaction can
--     land before the category it names) and the same logical row can be pushed
--     from two devices. A constraint failure in the sync write path would wedge
--     that device into retrying forever, so the write path must not be able to
--     fail on data. Referential integrity and de-duplication are the client's
--     and the importer's job.

CREATE TABLE account (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    lamport    INTEGER NOT NULL,
    device_id  TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    data       TEXT    NOT NULL,
    currency   TEXT GENERATED ALWAYS AS (json_extract(data, '$.currency')) VIRTUAL,
    archived   INTEGER GENERATED ALWAYS AS (json_extract(data, '$.archived')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

CREATE TABLE category (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    lamport    INTEGER NOT NULL,
    device_id  TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    data       TEXT    NOT NULL,
    kind       TEXT GENERATED ALWAYS AS (json_extract(data, '$.kind')) VIRTUAL,
    name       TEXT GENERATED ALWAYS AS (json_extract(data, '$.name')) VIRTUAL,
    archived   INTEGER GENERATED ALWAYS AS (json_extract(data, '$.archived')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

-- 'txn', not 'transaction', because the latter is a SQL keyword.
CREATE TABLE txn (
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id           TEXT    NOT NULL,
    lamport      INTEGER NOT NULL,
    device_id    TEXT    NOT NULL,
    updated_at   INTEGER NOT NULL,
    deleted      INTEGER NOT NULL DEFAULT 0,
    data         TEXT    NOT NULL,
    kind         TEXT    GENERATED ALWAYS AS (json_extract(data, '$.kind')) VIRTUAL,
    occurred_on  TEXT    GENERATED ALWAYS AS (json_extract(data, '$.occurredOn')) VIRTUAL,
    account_id   TEXT    GENERATED ALWAYS AS (json_extract(data, '$.accountId')) VIRTUAL,
    category_id  TEXT    GENERATED ALWAYS AS (json_extract(data, '$.categoryId')) VIRTUAL,
    amount_minor INTEGER GENERATED ALWAYS AS (json_extract(data, '$.amountMinor')) VIRTUAL,
    currency     TEXT    GENERATED ALWAYS AS (json_extract(data, '$.currency')) VIRTUAL,
    natural_key  TEXT    GENERATED ALWAYS AS (json_extract(data, '$.naturalKey')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

-- Deliberately NOT unique: the same CSV imported on two devices legitimately
-- produces two rows with one natural key, and a unique index here would reject
-- the second device's push forever. The importer de-duplicates by querying this
-- index, which is what it is for.
CREATE INDEX idx_txn_natural_key ON txn (user_id, natural_key)
    WHERE natural_key IS NOT NULL;

CREATE INDEX idx_txn_user_date ON txn (user_id, occurred_on) WHERE deleted = 0;
CREATE INDEX idx_txn_user_category ON txn (user_id, category_id, occurred_on) WHERE deleted = 0;

CREATE TABLE budget (
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id          TEXT    NOT NULL,
    lamport     INTEGER NOT NULL,
    device_id   TEXT    NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted     INTEGER NOT NULL DEFAULT 0,
    data        TEXT    NOT NULL,
    category_id TEXT GENERATED ALWAYS AS (json_extract(data, '$.categoryId')) VIRTUAL,
    period      TEXT GENERATED ALWAYS AS (json_extract(data, '$.period')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

CREATE INDEX idx_budget_user_period ON budget (user_id, period) WHERE deleted = 0;

CREATE TABLE recurring_rule (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    lamport    INTEGER NOT NULL,
    device_id  TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    data       TEXT    NOT NULL,
    next_on    TEXT GENERATED ALWAYS AS (json_extract(data, '$.nextOn')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

CREATE INDEX idx_recurring_due ON recurring_rule (next_on) WHERE deleted = 0;

-- Per-user preferences that follow the person between devices (base currency
-- display, first day of month, donut vs list). The row id IS the setting key,
-- so two devices editing the same setting collide on one row and LWW resolves
-- it — which is the behaviour you want.
CREATE TABLE user_setting (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    lamport    INTEGER NOT NULL,
    device_id  TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    data       TEXT    NOT NULL,
    PRIMARY KEY (user_id, id)
);

-- The journal every device replays. One row per accepted op, in server order.
CREATE TABLE change_log (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    entity    TEXT    NOT NULL,
    entity_id TEXT    NOT NULL,
    lamport   INTEGER NOT NULL,
    device_id TEXT    NOT NULL,
    deleted   INTEGER NOT NULL,
    data      TEXT    NOT NULL,
    server_ts INTEGER NOT NULL
);

CREATE INDEX idx_change_log_user_seq ON change_log (user_id, seq);

-- Per-user sync bookkeeping.
CREATE TABLE sync_state (
    user_id TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- High-water mark of the Lamport clock across this user's devices. Handed
    -- back on push so a freshly bootstrapped device does not start at 0 and
    -- lose every comparison it makes.
    lamport INTEGER NOT NULL DEFAULT 0,
    -- The oldest seq still present in change_log. A device whose cursor is
    -- older than this cannot replay and is told to re-bootstrap. Storing it
    -- makes that an exact decision rather than a guess.
    trimmed_before INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE sync_state;
DROP TABLE change_log;
DROP TABLE user_setting;
DROP TABLE recurring_rule;
DROP TABLE budget;
DROP TABLE txn;
DROP TABLE category;
DROP TABLE account;
