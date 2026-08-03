-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5. STRICT tables: a wrong type is an
-- error at write time rather than silent affinity coercion.

CREATE TABLE currency (
    code        TEXT PRIMARY KEY,
    exponent    INTEGER NOT NULL,
    symbol      TEXT,
    name        TEXT NOT NULL
) STRICT;

-- HUF exponent 0 is load-bearing: getting it wrong scales every Hungarian
-- figure by 100 (docs/adr/0004-integer-money.md).
INSERT INTO currency (code, exponent, symbol, name) VALUES
    ('EUR', 2, '€',  'Euro'),
    ('USD', 2, '$',  'US Dollar'),
    ('HUF', 0, 'Ft', 'Hungarian Forint'),
    ('UAH', 2, '₴',  'Ukrainian Hryvnia');

CREATE TABLE user (
    id                       INTEGER PRIMARY KEY,
    email                    TEXT NOT NULL UNIQUE,
    password_hash            TEXT NOT NULL,
    display_name             TEXT,
    role                     TEXT NOT NULL DEFAULT 'user'
                                 CHECK (role IN ('admin','user','readonly')),
    base_currency            TEXT NOT NULL DEFAULT 'EUR' REFERENCES currency(code),
    timezone                 TEXT NOT NULL DEFAULT 'UTC',
    fiscal_year_start_month  INTEGER NOT NULL DEFAULT 8
                                 CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
    -- Added to §3.5: the bootstrap admin is created from an environment password
    -- and every endpoint except password-change is refused until it is changed
    -- (docs/implementation-plan/02-auth-and-taxonomy.md task 9).
    must_change_password     INTEGER NOT NULL DEFAULT 0
                                 CHECK (must_change_password IN (0,1)),
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    deleted_at               TEXT
) STRICT;

CREATE TABLE session (
    token_hash  TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    user_agent  TEXT,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    revoked_at  TEXT
) STRICT;
CREATE INDEX idx_session_user ON session(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_session_user;
DROP TABLE IF EXISTS session;
DROP TABLE IF EXISTS user;
DROP TABLE IF EXISTS currency;
