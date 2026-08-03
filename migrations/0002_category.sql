-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5.
--
-- The canonical categories and their aliases are NOT inserted here: category rows
-- carry user_id, and at migration time there is no user to own them. They are
-- installed per user at creation, from internal/domain/seed. See the note in
-- docs/03-data-model.md §3.7.

CREATE TABLE category (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('expense','income')),
    is_essential  INTEGER NOT NULL DEFAULT 0 CHECK (is_essential IN (0,1)),
    icon          TEXT,
    color         TEXT,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    parent_id     INTEGER REFERENCES category(id),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    archived_at   TEXT
) STRICT;
-- Non-archived names are unique; an archived name becomes reusable.
CREATE UNIQUE INDEX idx_category_name ON category(user_id, name) WHERE archived_at IS NULL;

-- The fix for the 14% silent loss: source names map explicitly to canonical ones.
CREATE TABLE category_alias (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    category_id  INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
    source       TEXT NOT NULL DEFAULT 'monefy',
    source_name  TEXT NOT NULL,
    created_at   TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX idx_category_alias ON category_alias(user_id, source, source_name);

-- +goose Down
DROP INDEX IF EXISTS idx_category_alias;
DROP TABLE IF EXISTS category_alias;
DROP INDEX IF EXISTS idx_category_name;
DROP TABLE IF EXISTS category;
