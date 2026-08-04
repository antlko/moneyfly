-- +goose Up

-- A person. password_hash is NULL for an account that only ever signs in through
-- an identity provider.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT    NOT NULL,
    password_hash TEXT,
    display_name  TEXT    NOT NULL DEFAULT '',
    base_currency TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

-- Email is matched case-insensitively, so uniqueness has to be too — otherwise
-- Bob@x and bob@x would be two accounts that both "already exist" on sign-in.
CREATE UNIQUE INDEX idx_users_email ON users (lower(email));

-- One row per way of signing in to an account: 'password' plus one per OIDC
-- provider. Existing from the start is what makes "link Google later" a new row
-- rather than a migration.
CREATE TABLE auth_identity (
    id         TEXT PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    email      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE (provider, subject)
);

CREATE INDEX idx_auth_identity_user ON auth_identity (user_id);

-- A browser profile. The id is minted client-side (UUIDv7) and is also the
-- sync identity, so it must survive sign-out: sync cursors are keyed on it.
CREATE TABLE devices (
    id           TEXT PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL DEFAULT '',
    platform     TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    last_seq     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_devices_user ON devices (user_id);

-- Sessions store only a SHA-256 of the token: a database leak must not hand
-- anyone a working cookie. device_id has no foreign key because the client sends
-- its device id on sign-in, before the device row necessarily exists.
CREATE TABLE sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id    TEXT,
    user_agent   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);

CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- In-flight OIDC authorisations: CSRF state, replay nonce and the PKCE verifier.
-- Rows are consumed on callback and swept by retention, so this table is
-- normally empty.
CREATE TABLE oidc_state (
    state         TEXT PRIMARY KEY,
    provider      TEXT    NOT NULL,
    nonce         TEXT    NOT NULL,
    code_verifier TEXT    NOT NULL,
    link_user_id  TEXT,
    redirect_to   TEXT    NOT NULL DEFAULT '/',
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
);

CREATE INDEX idx_oidc_state_expires ON oidc_state (expires_at);

-- +goose Down
DROP TABLE oidc_state;
DROP TABLE sessions;
DROP TABLE devices;
DROP TABLE auth_identity;
DROP TABLE users;
