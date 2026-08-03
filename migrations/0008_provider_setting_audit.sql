-- +goose Up
-- Stage 07. `provider` and `setting` already exist: migration 0004 created them
-- with the two FX providers, because §3.7 groups them there. This migration adds
-- what stage 07 actually needs on top.
--
-- The metal and crypto rows point at the same fawazahmed0 endpoint — it serves
-- 338 tickers including XAU and USDT from one document — but they are separate
-- provider rows because `kind` is what a setting selects on, and `price.XAU`
-- must be able to fail independently of the FX chain.
--
-- `kind` still has exactly three values. Trading212 was dropped, not deferred
-- (docs/06-fx-and-providers.md §6.7), and the CHECK from 0004 pins that at the
-- schema level so it cannot creep back as a data-only change.
INSERT INTO provider (key, kind, endpoint, priority, enabled) VALUES
    ('fawazahmed0-metal', 'metal',
     'https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/eur.json', 10, 1),
    ('fawazahmed0-crypto', 'crypto',
     'https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/eur.json', 10, 1);

-- Who changed the amber threshold, and when.
CREATE TABLE audit_log (
    id             INTEGER PRIMARY KEY,
    user_id        INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    actor_user_id  INTEGER REFERENCES user(id),
    entity         TEXT NOT NULL,
    entity_id      INTEGER,
    action         TEXT NOT NULL,
    before_json    TEXT,
    after_json     TEXT,
    at             TEXT NOT NULL
) STRICT;
CREATE INDEX idx_audit_entity ON audit_log(user_id, entity, entity_id, at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_entity;
DROP TABLE IF EXISTS audit_log;
DELETE FROM provider WHERE key IN ('fawazahmed0-metal', 'fawazahmed0-crypto');
