# Cutover

The runbook for replacing the workbook. Every command here has been run; where a
step depends on something outside this repository — a real Telegram token, the old
bot's credentials — that is said explicitly rather than implied.

## 1. Deploy and bootstrap

```bash
export MONEYAPP_BOOTSTRAP_PASSWORD='pick-something-long'
docker compose up -d
docker compose run --rm moneyapp migrate up
docker compose restart moneyapp
```

Migrations are not run at boot, deliberately: `/readyz` stays unhealthy until the
schema is current, so a container started against an unmigrated database refuses
traffic rather than writing against a schema it does not understand
([adr/0011](adr/0011-explicit-migrations.md)).

`migrate up` takes a verified backup first whenever there is a schema to back up.

Log in as the bootstrap admin and change the password — the API refuses everything
else until you do.

## 2. Load the workbook's history

The workbook is read through the extract in `testdata/parity/`, produced from the
`.xlsx` by `testdata/parity/generate.py`. That is the same file the parity suite
asserts against, so what the migration loads is what the tests check. Regenerate it
if the workbook has changed:

```bash
python3 testdata/parity/generate.py "~/Downloads/[2025-2026] Budget_ Capital Grow.xlsx"
```

Then, always dry-run first:

```bash
docker compose run --rm moneyapp migrate-excel --dry-run
# would write 216 budget rows, 154 snapshots, 126 aggregate expenses, 11 income rows, 3 rates

docker compose run --rm moneyapp migrate-excel --commit
```

What it does, and does not do:

- **`-1` never becomes a row.** The sentinel that corrupted the workbook's own
  totals does not enter the database in any form; an unfilled month stays absent.
- **The Monefy CSV wins on detail.** Any month that already holds transactions is
  skipped and named in the output. Writing the workbook's monthly aggregate on top
  of imported line items would double every overlapping month, which is the most
  likely way this step could corrupt real data.
- **Aggregates are labelled.** Each carries `workbook monthly aggregate` and the
  original cell value in its description, so a rounded figure is always traceable.
- **Re-running is safe.** Budgets and snapshots upsert; the second run reports
  zero aggregates because its own rows now cover those months.

## 3. Import the Monefy export

Through the normal pipeline — the same one the bot uses:

```
Import → choose file → resolve any unmapped names → commit
```

An unrecognised category **blocks the batch**. That is the point: the previous
pipeline discarded 237 of 1,683 rows silently, and this one refuses to.

## 4. Check the parity page

```bash
curl -s -b j localhost:8080/api/v1/reports/parity | jq '{matched, deviations: (.deviations|length)}'
# {"matched": true, "deviations": 11}
```

**This is the cutover criterion.** The workbook is retired when the page is clean —
not when someone feels ready. The eleven deviations are the documented ones from
[appendix-excel-parity.md](appendix-excel-parity.md) §A.8; anything else is a bug.

The comparison itself runs on every build:

```bash
make parity
```

## 5. Connect Telegram (optional)

```bash
export MONEYAPP_TELEGRAM_TOKEN='...'   # from @BotFather; never a config-file literal
docker compose up -d
```

Settings → Telegram → Connect a chat → send `/link ABC123` to the bot → share a
Monefy export to it. The bot relays the file to the same importer and replies once.
It does nothing else, by design.

## 6. Turn on automatic rates

Settings → Currencies. Each rate shows its provider, last fetch and freshness.
Both providers are free and keyless; a manual value always wins over a fetched one,
and both stay visible.

Confirm the fetched figures are in the same range as the workbook's hand-typed
ones. A move beyond 15% in a day is rejected and flagged rather than stored — if
the workbook's rate was stale, expect exactly that on the first fetch, and set the
value manually once to establish a sane baseline.

## 7. Backups and the restore drill

Nightly, at `backup.schedule`, gzipped to `backup.path`, `backup.keep` retained,
integrity-checked after every write. A copy that fails its check is deleted and the
previous archive kept.

```bash
docker compose run --rm moneyapp backup now
docker compose run --rm moneyapp backup list
```

**Perform the drill.** An untested backup is not a backup:

```bash
docker compose down
gunzip -c /path/to/backups/moneyapp-<timestamp>.db.gz > /path/to/config/moneyapp.db
docker compose up -d
docker compose run --rm moneyapp migrate status
curl -fs localhost:8080/readyz
```

This is also asserted automatically, on every build, by `TestBackup_Restorable`:
it takes a real backup, restores it into a fresh file, and checks both the data and
the schema version.

## 8. Run in parallel, then retire

Keep both systems for one month. When the parity page has stayed clean across that
month, stop updating the workbook.

## 9. Rotate the old credentials

**Not optional, and not something this repository can do for you.** The previous
bot's `main.go` carried its Telegram token and a Trading212 API key as literals,
and they are in that repository's git history:

1. Stop the old bot.
2. Revoke its Telegram token with `@BotFather` (`/revoke`) and issue a new one.
3. Revoke the Trading212 API key from the Trading212 account settings.

Rotating is the only fix. A key in git history stays in git history.
