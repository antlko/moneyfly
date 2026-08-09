-- +goose Up

-- One registered passkey. `data` is the opaque JSON encoding of a go-webauthn
-- Credential — public key, sign count, transports, attestation — so this table
-- never has to grow a column for every field that library's own struct happens
-- to carry. credential_id is broken out as its own indexed column because a
-- sign-in has to find the row before there is anything to decode: the same
-- "indexed lookup column plus opaque blob" shape api_token uses for its hash.
--
-- Never synced, like api_token and webhook beside it: a credential is a way
-- into an account, the same category OIDC client secrets and fx_rate already
-- sit in, outside the op-log entirely (docs/ARCHITECTURE.md §1).
CREATE TABLE webauthn_credential (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id TEXT    NOT NULL,
    name          TEXT    NOT NULL DEFAULT '',
    data          TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX idx_webauthn_credential_credential_id ON webauthn_credential (credential_id);
CREATE INDEX idx_webauthn_credential_user ON webauthn_credential (user_id);

-- An in-flight registration or sign-in ceremony: the challenge that was issued
-- and everything needed to verify the browser's answer to it, stored opaque
-- (the JSON encoding of webauthn.SessionData) because nothing here inspects
-- it, only round-trips it.
--
-- user_id is set for a registration — always started by someone already signed
-- in — and NULL for a sign-in, where the account is not known until the
-- assertion names it. `purpose` is what keeps one endpoint from consuming the
-- other's row.
--
-- Modelled directly on oidc_state: the row is deleted the moment it is looked
-- up, expired or not, so a challenge can never be replayed.
CREATE TABLE webauthn_session (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT    NOT NULL,
    data       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_webauthn_session_expires ON webauthn_session (expires_at);

-- +goose Down
DROP TABLE webauthn_session;
DROP TABLE webauthn_credential;
