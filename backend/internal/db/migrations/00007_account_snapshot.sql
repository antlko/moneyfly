-- +goose Up

-- What an account held at the end of a month, entered by hand — the monthly
-- capital check-in (docs/ARCHITECTURE.md §"Capital"). The same synced shape as
-- every other entity: the body is JSON, and only what the server might query
-- is pulled out as generated columns.
--
-- The row id is `<accountId>:<YYYY-MM>`, chosen by the client, so two devices
-- filling in the same month for the same account collide on one row and
-- last-write-wins settles it — the user_setting pattern. That is the
-- de-duplication; there is deliberately no unique constraint, for the reason
-- 00002_sync.sql gives for every synced table.
--
-- A snapshot is a fact someone typed, like an opening balance — not a running
-- total that each transaction updates — so it does not break the rule that
-- balances are derived (docs/SYNC.md §2).
CREATE TABLE account_snapshot (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    id         TEXT    NOT NULL,
    lamport    INTEGER NOT NULL,
    device_id  TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    data       TEXT    NOT NULL,
    account_id TEXT GENERATED ALWAYS AS (json_extract(data, '$.accountId')) VIRTUAL,
    month      TEXT GENERATED ALWAYS AS (json_extract(data, '$.month')) VIRTUAL,
    PRIMARY KEY (user_id, id)
);

CREATE INDEX idx_account_snapshot_user_month ON account_snapshot (user_id, month)
    WHERE deleted = 0;

-- +goose Down
DROP INDEX idx_account_snapshot_user_month;
DROP TABLE account_snapshot;
