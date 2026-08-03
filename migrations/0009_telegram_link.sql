-- +goose Up
-- Stage 08. Transcribed from docs/03-data-model.md §3.5.
--
-- A link is minted as a code, then bound to a chat. The partial unique index is
-- what keeps one live link per chat while allowing any number of revoked ones:
-- history is kept, but a chat can only ever be speaking for one account.
CREATE TABLE telegram_link (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    chat_id     INTEGER,
    link_code   TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    linked_at   TEXT,
    revoked_at  TEXT,
    created_at  TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX idx_tg_code ON telegram_link(link_code);
CREATE UNIQUE INDEX idx_tg_chat ON telegram_link(chat_id)
    WHERE linked_at IS NOT NULL AND revoked_at IS NULL;

-- The poll offset, and anything else the process needs to remember across a
-- restart that is not a user's data. Telegram buffers updates while the app is
-- down, so the offset is what makes a file sent during a deploy arrive exactly
-- once — neither replayed nor skipped.
CREATE TABLE kv (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS kv;
DROP INDEX IF EXISTS idx_tg_chat;
DROP INDEX IF EXISTS idx_tg_code;
DROP TABLE IF EXISTS telegram_link;
