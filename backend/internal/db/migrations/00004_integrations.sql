-- +goose Up

-- A named, long-lived credential for scripted access — curl, a phone
-- shortcut, a personal automation. Like sessions, only the hash is stored: a
-- database leak must not hand anyone a working token. Unlike a session it has
-- no expiry and no device — it is not tied to a browser profile at all, and a
-- person is expected to have a handful, named by what uses them, not one per
-- sign-in.
CREATE TABLE api_token (
    id           TEXT    PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX idx_api_token_hash ON api_token (token_hash);
CREATE INDEX idx_api_token_user ON api_token (user_id);

-- Where to POST a notification when a transaction is written. The secret
-- signs the payload (HMAC-SHA256, internal/api/webhooks.go) so the receiving
-- end can tell this instance sent it from anyone who found the URL.
--
-- Neither this table nor api_token above is synced — both are integration
-- secrets, the same category fx_rate and OIDC client secrets already sit in,
-- outside the op-log entirely (docs/ARCHITECTURE.md §1).
CREATE TABLE webhook (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    url        TEXT    NOT NULL,
    secret     TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_webhook_user ON webhook (user_id);

-- +goose Down
DROP TABLE webhook;
DROP TABLE api_token;
