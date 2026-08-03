-- +goose Up
-- Stage 05. `exclude_from_average` keeps a genuine one-off out of the mean while
-- leaving it in the spend and the total, which the workbook had no way to say.
--
-- The motivating row is `Appliances` in January: 10,248.28 against a plan of 150,
-- which makes the twelve-month average 1,067 and therefore useless as a
-- forecast. The money was spent and must stay in the total; it simply is not a
-- monthly habit.
--
-- Nullable rather than NOT NULL DEFAULT 0: NULL means nobody has expressed an
-- opinion, 0 means someone decided this row does belong in the average
-- (conventions §3). Both read as "include" — the distinction is for the UI, which
-- shows an explicit decision differently from an untouched row.
ALTER TABLE transaction_entry ADD COLUMN exclude_from_average INTEGER;

CREATE INDEX idx_txn_average ON transaction_entry(user_id, occurred_on)
    WHERE deleted_at IS NULL AND exclude_from_average = 1;

-- +goose Down
DROP INDEX IF EXISTS idx_txn_average;
ALTER TABLE transaction_entry DROP COLUMN exclude_from_average;
