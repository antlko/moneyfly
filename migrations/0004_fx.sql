-- +goose Up
-- Transcribed from docs/03-data-model.md §3.5.
--
-- `provider` and `setting` are created here to keep the numbering aligned with
-- §3.7. The machinery that uses them — the provider chain and the settings screen
-- — is stage 07; only fx_rate is used before then.

CREATE TABLE provider (
    id         INTEGER PRIMARY KEY,
    key        TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('fx','metal','crypto')),
    endpoint   TEXT NOT NULL,
    priority   INTEGER NOT NULL DEFAULT 100,
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1))
) STRICT;

-- Tested live on 2026-07-29 (docs/06-fx-and-providers.md §6.2). Lower priority
-- wins. Both are free and keyless, so there is no secret to manage. fawazahmed0
-- serves XAU and USDT from the same endpoint, so it needs no separate row.
INSERT INTO provider (key, kind, endpoint, priority, enabled) VALUES
    ('open-er-api', 'fx', 'https://open.er-api.com/v6/latest/EUR', 10, 1),
    ('fawazahmed0', 'fx', 'https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/eur.json', 20, 1);

-- rate is TEXT decimal to avoid binary-float drift; parsed into big.Rat.
CREATE TABLE fx_rate (
    id          INTEGER PRIMARY KEY,
    as_of_date  TEXT NOT NULL,
    base        TEXT NOT NULL,
    quote       TEXT NOT NULL,
    rate        TEXT NOT NULL,
    source      TEXT NOT NULL,
    fetched_at  TEXT NOT NULL
) STRICT;
-- Two providers may hold an opinion about the same day; provider.priority resolves.
CREATE UNIQUE INDEX idx_fx_unique ON fx_rate(as_of_date, base, quote, source);
CREATE INDEX idx_fx_lookup ON fx_rate(base, quote, as_of_date DESC);

-- The workbook's four hand-typed rates, given the date they must have applied
-- from so that nearest-earlier lookup covers the whole history in the Monefy
-- export (earliest row 19.07.2021).
--
-- Only EUR->X is stored: the sheet's USD/EUR 0.88 is the inverse of EUR/USD 1.14
-- and disagrees with it (1/1.14 = 0.8772). Storing one direction removes the
-- contradiction by construction (docs/adr/0009-dated-fx-provider-chain.md).
--
--   E3 HUF/EUR 0.0028 -> EUR->HUF 357.142857142857
--   E5 UAH/EUR 0.02   -> EUR->UAH 50
INSERT INTO fx_rate (as_of_date, base, quote, rate, source, fetched_at) VALUES
    ('2021-07-01', 'EUR', 'USD', '1.14',               'workbook-seed', '2026-07-30T00:00:00Z'),
    ('2021-07-01', 'EUR', 'HUF', '357.142857142857',   'workbook-seed', '2026-07-30T00:00:00Z'),
    ('2021-07-01', 'EUR', 'UAH', '50',                 'workbook-seed', '2026-07-30T00:00:00Z');

CREATE TABLE setting (
    id                       INTEGER PRIMARY KEY,
    user_id                  INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    key                      TEXT NOT NULL,
    mode                     TEXT NOT NULL DEFAULT 'manual'
                                 CHECK (mode IN ('manual','auto')),
    manual_value             TEXT,
    provider_key             TEXT REFERENCES provider(key),
    refresh_interval_seconds INTEGER,
    last_value               TEXT,
    last_fetched_at          TEXT,
    last_error               TEXT,
    updated_at               TEXT NOT NULL,
    CHECK (mode = 'manual' OR provider_key IS NOT NULL)
) STRICT;
CREATE UNIQUE INDEX idx_setting_key ON setting(user_id, key);

-- +goose Down
DROP INDEX IF EXISTS idx_setting_key;
DROP TABLE IF EXISTS setting;
DROP INDEX IF EXISTS idx_fx_lookup;
DROP INDEX IF EXISTS idx_fx_unique;
DROP TABLE IF EXISTS fx_rate;
DROP TABLE IF EXISTS provider;
