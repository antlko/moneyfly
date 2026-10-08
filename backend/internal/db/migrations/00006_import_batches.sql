-- +goose Up

-- Which CSV import wrote a transaction, so one import can be taken back as a
-- unit. The id lives in the row's own JSON (`importId`), like every other
-- synced field; this is only the generated column the server queries it by.
ALTER TABLE txn ADD COLUMN import_id TEXT GENERATED ALWAYS AS (json_extract(data, '$.importId')) VIRTUAL;

CREATE INDEX idx_txn_import ON txn (user_id, import_id)
    WHERE import_id IS NOT NULL;

-- One committed CSV import: the name of the file and when. Never synced — it
-- is bookkeeping about a server-side action, not a domain row, so no device
-- needs a replica of it, and it may carry an ordinary foreign key. How many
-- rows it still has is never stored here: it is counted from txn, so deleting
-- a single imported record on a phone is reflected without anything to update.
CREATE TABLE import_batch (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_name  TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_import_batch_user ON import_batch (user_id, created_at);

-- +goose Down
DROP INDEX idx_import_batch_user;
DROP TABLE import_batch;
DROP INDEX idx_txn_import;
ALTER TABLE txn DROP COLUMN import_id;
