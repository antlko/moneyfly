-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5.
--
-- Accounts and their aliases are user-scoped and installed per user at creation,
-- from internal/domain/seed (workbook rows 36-69, appendix §A.4).

CREATE TABLE account (
    id                        INTEGER PRIMARY KEY,
    user_id                   INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    name                      TEXT NOT NULL,
    asset_class               TEXT NOT NULL CHECK (asset_class IN
                                  ('cash','bank','deposit','investment','metal','crypto','other')),
    currency                  TEXT NOT NULL REFERENCES currency(code),
    is_liquid                 INTEGER NOT NULL DEFAULT 1 CHECK (is_liquid IN (0,1)),
    counts_toward_net_worth   INTEGER NOT NULL DEFAULT 1
                                  CHECK (counts_toward_net_worth IN (0,1)),
    price_ticker              TEXT,
    cost_basis_minor          INTEGER,
    parent_id                 INTEGER REFERENCES account(id),
    sort_order                INTEGER NOT NULL DEFAULT 0,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    archived_at               TEXT
) STRICT;
CREATE UNIQUE INDEX idx_account_name ON account(user_id, name) WHERE archived_at IS NULL;

CREATE TABLE account_alias (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    account_id  INTEGER NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    source      TEXT NOT NULL DEFAULT 'monefy',
    source_name TEXT NOT NULL,
    created_at  TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX idx_account_alias ON account_alias(user_id, source, source_name);

-- +goose Down
DROP INDEX IF EXISTS idx_account_alias;
DROP TABLE IF EXISTS account_alias;
DROP INDEX IF EXISTS idx_account_name;
DROP TABLE IF EXISTS account;
