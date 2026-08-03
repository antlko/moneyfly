-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5.
--
-- `import_batch` and `import_row` are created here because transaction_entry
-- references import_batch, and because §3.7 groups them in this migration. The
-- import pipeline itself is stage 04; until then import_batch_id stays NULL.
--
-- `idempotency_key` is an addition to §3.5: POST /transactions honours an
-- Idempotency-Key (docs/09-api.md §9.1), and replaying one has to return the
-- original response. Content-based dedup cannot serve that purpose, because two
-- identical coffees on one day are two real transactions.

CREATE TABLE import_batch (
    id              INTEGER PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    source          TEXT NOT NULL DEFAULT 'monefy',
    origin          TEXT NOT NULL CHECK (origin IN ('web','telegram','cli')),
    filename        TEXT,
    file_sha256     TEXT,
    stored_path     TEXT,
    status          TEXT NOT NULL CHECK (status IN
                        ('received','parsed','needs_mapping','previewed',
                         'committed','reverted','failed')),
    rows_total      INTEGER NOT NULL DEFAULT 0,
    rows_new        INTEGER NOT NULL DEFAULT 0,
    rows_duplicate  INTEGER NOT NULL DEFAULT 0,
    rows_unmapped   INTEGER NOT NULL DEFAULT 0,
    rows_rejected   INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      TEXT NOT NULL,
    committed_at    TEXT,
    reverted_at     TEXT
) STRICT;
CREATE INDEX idx_batch_user ON import_batch(user_id, created_at DESC);

CREATE TABLE transaction_entry (
    id                 INTEGER PRIMARY KEY,
    user_id            INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    account_id         INTEGER NOT NULL REFERENCES account(id),
    category_id        INTEGER REFERENCES category(id),
    occurred_on        TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN
                           ('expense','income','transfer_in','transfer_out')),
    -- The sign is carried by kind, never by the amount.
    amount_minor       INTEGER NOT NULL CHECK (amount_minor >= 0),
    currency           TEXT NOT NULL REFERENCES currency(code),
    base_amount_minor  INTEGER,
    base_currency      TEXT REFERENCES currency(code),
    fx_rate_id         INTEGER REFERENCES fx_rate(id),
    description        TEXT,
    merchant           TEXT,
    natural_key        TEXT NOT NULL,
    occurrence         INTEGER NOT NULL DEFAULT 1,
    transfer_group_id  INTEGER,
    import_batch_id    INTEGER REFERENCES import_batch(id),
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    deleted_at         TEXT,
    -- A transfer has no category; anything else must have one.
    CHECK ((kind IN ('transfer_in','transfer_out')) = (category_id IS NULL))
) STRICT;
-- Idempotency as a database guarantee rather than importer logic: the same row
-- re-imported matches, while a second identical coffee gets occurrence 2.
CREATE UNIQUE INDEX idx_txn_natural
    ON transaction_entry(user_id, natural_key, occurrence)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_txn_report ON transaction_entry(user_id, occurred_on, category_id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_txn_account ON transaction_entry(user_id, account_id, occurred_on)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_txn_batch ON transaction_entry(import_batch_id);

CREATE TABLE import_row (
    id                  INTEGER PRIMARY KEY,
    batch_id            INTEGER NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
    line_no             INTEGER NOT NULL,
    raw_line            TEXT NOT NULL,
    parsed_date         TEXT,
    parsed_account      TEXT,
    parsed_category     TEXT,
    parsed_amount_minor INTEGER,
    parsed_currency     TEXT,
    parsed_description  TEXT,
    status              TEXT NOT NULL CHECK (status IN
                            ('new','duplicate','unmapped','rejected','committed')),
    reason              TEXT,
    transaction_id      INTEGER REFERENCES transaction_entry(id),
    UNIQUE (batch_id, line_no)
) STRICT;
CREATE INDEX idx_import_row_status ON import_row(batch_id, status);

CREATE TABLE idempotency_key (
    id               INTEGER PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    key              TEXT NOT NULL,
    method           TEXT NOT NULL,
    path             TEXT NOT NULL,
    request_hash     TEXT NOT NULL,
    response_status  INTEGER NOT NULL,
    response_body    BLOB NOT NULL,
    created_at       TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX idx_idempotency_key ON idempotency_key(user_id, key);

-- +goose Down
DROP INDEX IF EXISTS idx_idempotency_key;
DROP TABLE IF EXISTS idempotency_key;
DROP INDEX IF EXISTS idx_import_row_status;
DROP TABLE IF EXISTS import_row;
DROP INDEX IF EXISTS idx_txn_batch;
DROP INDEX IF EXISTS idx_txn_account;
DROP INDEX IF EXISTS idx_txn_report;
DROP INDEX IF EXISTS idx_txn_natural;
DROP TABLE IF EXISTS transaction_entry;
DROP INDEX IF EXISTS idx_batch_user;
DROP TABLE IF EXISTS import_batch;
