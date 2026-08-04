-- +goose Up

-- Dated exchange rates.
--
-- This table breaks the pattern set by 00002 on purpose, and the reasons are
-- worth stating because everything else in the database follows the other rule:
--
--   * NO user_id. A rate is a fact about the world, not about a person, so it is
--     outside the per-user scoping invariant. Two users on one instance share the
--     same history and neither can see anything of the other's through it.
--
--   * NO data JSON, no lamport, no tombstone. Rates are not synced. They are
--     fetched by the server from providers and read by clients over plain REST;
--     they never enter the op-log, because a device has nothing to say about
--     them. Putting them in the log would replicate a public dataset through a
--     private, ordered channel for no gain.
--
--   * Typed columns and a real UNIQUE constraint are therefore fine here. The
--     "no constraint in the write path" rule exists because a client op must
--     never fail; nothing a client sends reaches this table.
--
-- Only EUR-based rates are stored. quote->EUR is the computed inverse and a
-- cross rate goes through EUR, which structurally removes the possibility of
-- holding EUR->USD 1.14 and USD->EUR 0.88 at the same time and quietly
-- disagreeing with yourself.
--
-- `rate` is decimal TEXT, never REAL: binary floating point cannot hold 1.13
-- exactly, and a rate is multiplied into every converted figure on the screen.
CREATE TABLE fx_rate (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    -- YYYY-MM-DD. Lookup takes the exact date, else the nearest EARLIER one —
    -- never a later one, or a figure from last March would change because a
    -- rate arrived in April.
    as_of_date TEXT NOT NULL,
    base       TEXT NOT NULL,
    quote      TEXT NOT NULL,
    rate       TEXT NOT NULL,
    -- The provider key that supplied it, so a bad feed can be identified after
    -- the fact rather than guessed at.
    source     TEXT NOT NULL,
    fetched_at INTEGER NOT NULL
);

-- Re-running a day's fetch overwrites rather than duplicates, so the refresh
-- loop is idempotent and safe to run twice after a restart.
CREATE UNIQUE INDEX idx_fx_unique ON fx_rate (as_of_date, base, quote, source);

-- The shape of every lookup: newest row for a pair at or before a date.
CREATE INDEX idx_fx_lookup ON fx_rate (base, quote, as_of_date DESC);

-- +goose Down
DROP TABLE fx_rate;
