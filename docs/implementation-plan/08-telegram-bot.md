# Stage 08 — Telegram Bot

> **Kickoff prompt**
> Implement stage 08 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `08-telegram-bot.md`, then **all of `docs/05-telegram-bot.md`** and `docs/adr/0013-telegram-in-process.md`. Stages 01–04 are required. **This is a deliberately small stage.** The bot relays bytes to the stage-04 importer and returns one reply — it gets no import logic, no reports, no notifications.

## Goal

Forward a Monefy export from the phone and have it import. That is the entire feature.

The value is narrow and specific: **the phone never has to reach the server.** Monefy's share sheet sends the CSV to Telegram; the server pulls it. No file transfer to a computer, no inbound endpoint, no VPN, any network.

## MVP demo

Settings → Connect Telegram → a 6-character code → send `/link ABC123` to the bot → *"Linked."* → export from Monefy on the phone and share to the bot → reply within seconds: *"Imported 40 new, 1,683 duplicates, 29 months. View: <link>"* → the numbers are in the app.

Then the negative cases: an **unlinked** chat sends a file → *"Not linked. Use /link in the app."* and **nothing is stored**. A file with an unknown category → *"2 unknown categories. Resolve: <link>"* and **nothing is committed**.

## Scope

**In:** migration 0011 (`telegram_link`), the long-poll worker with a persisted offset, chat linking, document ingestion via the stage-04 `importer.Service`, four commands, supervision and backoff, rate limiting on `/link`, config gating.

**Out — and this is the point of the stage:**

| Not built | Why |
| --- | --- |
| `/status`, `/balance`, `/spend` | The app renders this better. Two renderers of the same numbers drift apart. |
| Expense entry by message | Free-text amount parsing is a new failure surface. Quick entry is three taps. |
| `/undo` | Reverting an import deserves the preview screen, not a chat confirmation. |
| Scheduled notifications | Separate concern with its own delivery, retry and opt-out surface. |
| Aggregation or formatting in the bot | The old bot's mistake. Aggregation belongs in stage 05, once. |
| Webhook mode | Needs a routable HTTPS endpoint to save seconds on a monthly upload. |

If the app later proves inconvenient in a way a command would fix, that is evidence to revisit. Anticipating it is not.

## Contracts consumed

The bot **implements nothing new** on the import side. It calls stage 04:

```go
batch, err := importer.Receive(ctx, userID, importer.OriginTelegram, filename, reader)
```

Everything — parsing, aliases, dedup, reconcile, commit — is the shared pipeline. One parser, one alias table, one dedup rule, one set of tests. A second path here is exactly how the old system drifted.

## Tasks

**1. Migration 0009 — `telegram_link`.** Per [03-data-model.md](../03-data-model.md)
§3.5, including the partial unique index on
`chat_id WHERE linked_at IS NOT NULL AND revoked_at IS NULL`, plus a `kv` table for
the persisted poll offset. (Numbered 0009, not 0011: stages 04 and 06 needed no
migration of their own because §3.7 had already grouped their tables earlier.)

**2. Worker skeleton.** Long-poll only, behind a four-method `Client` interface —
`GetUpdates`, `SendMessage`, `Download` — implemented over the Bot API in
`client.go`. The interface is what lets every test drive the whole worker with no
network; `gotgbot/v2` was fetched but not used, because four calls behind our own
seam is less code than the adapter would be and one less rc-versioned dependency
in the request path. Started from `cmd/moneyapp` **only when `telegram.enabled`**. Default is `false` — opt-in, because Telegram necessarily sees every uploaded file.

**3. Persisted offset.** Store the last acknowledged `update_id`. On boot, resume from it. Telegram buffers while the app is down, so a file sent during a deploy still arrives — and must arrive **once**, neither replayed nor skipped.

**4. Supervision.** The worker must never take down the HTTP server:
- recover per update, log with `update_id`, continue the loop
- exponential backoff 1s → 60s with jitter on poll failure
- log **once per state transition**, not once per attempt — a flapping network must not fill the disk

**5. Chat resolution.** `chat_id` → `user_id` via `telegram_link`. **Unlinked chats get one instructional reply and are otherwise ignored** — no signal about whether a code exists, and nothing stored.

**6. Linking.** `POST /telegram/link-code` mints a single-use, 10-minute, cryptographically random code. `/link <code>` binds. `/unlink` and the web UI both revoke. Rate-limit `/link` per chat so codes cannot be guessed.

**7. Document ingestion.** `getFile` → **stream** to `/config/uploads` (never buffer in memory) → `importer.Receive` → relay one reply. Cap at Telegram's 20 MB `getFile` limit; the largest observed export is 76 KB.

**8. Reply formatting.** Three outcomes, one short message each: committed with counts, `needs_mapping` with a link, or a failure reason. Links use `server.base_url`.

**9. Commands.** `/link`, `/unlink`, `/help`, plus document upload. `/help` lists exactly these.

**10. Token handling.** From config or environment only, never a source default. Boot fails fast when `enabled: true` and the token is empty. **Redact the token in every log line, including the `getFile` download URL, which embeds it.**

**11. Telegram endpoints.** `POST /telegram/link-code`, `GET /telegram/links`, `DELETE /telegram/links/{id}`. Settings UI shows linked chats with revoke.

## Tests

Telegram API is faked at the client interface; no network in CI.

| Test | Asserts |
| --- | --- |
| `TestLink_ValidCode` | binds, replies, code consumed |
| `TestLink_CodeSingleUse` | second use rejected |
| `TestLink_ExpiredCode` | after TTL → rejected |
| `TestLink_RateLimited` | repeated bad attempts → throttled |
| `TestLink_OneLivePerChat` | second live link → constraint violation |
| `TestUnlink_RevokesImmediately` | next upload refused |
| `TestUpload_UnlinkedChat_StoresNothing` | reply sent, **0 batches created** |
| `TestUpload_LinkedChat_CreatesBatch` | batch via `importer.Receive` |
| `TestUpload_CleanFile_CommitsAndReports` | counts in the reply |
| `TestUpload_UnmappedNames_DoesNotCommit` | `needs_mapping`, link in reply, **0 rows** |
| `TestUpload_OversizeRejected` | > cap → rejected, nothing stored |
| `TestUpload_StreamsToDisk` | large file does not load into memory |
| `TestWorker_PanicRecoveredPerUpdate` | next update still processed |
| `TestWorker_BackoffOnPollFailure` | delays grow, capped at 60s |
| `TestWorker_OffsetPersistedAcrossRestart` | no replay, no skip |
| `TestWorker_DisabledByDefault` | absent config → worker never starts |
| `TestConfig_EnabledWithoutToken_FailsBoot` | non-zero exit naming the key |
| `TestLog_TokenRedactedInDownloadURL` | token never in output |
| `TestBot_NoImportLogic` | `internal/transport/telegram` imports `importer` but no CSV parsing |

`TestBot_NoImportLogic` enforces the architectural point: the bot is a transport, not a second importer.

## Verification

```bash
make verify
# with a real test bot token in the environment
MONEYAPP_TELEGRAM_TOKEN=... docker compose up -d
docker compose run --rm moneyapp migrate up

curl -s -b j -X POST localhost:8080/api/v1/telegram/link-code | jq -r '.code'
# send /link <code> from Telegram        -> "Linked."
# share the Monefy export to the bot     -> "Imported N new, M duplicates..."
curl -s -b j localhost:8080/api/v1/imports | jq '.[0]|{origin,status,rows_new}'
# {"origin":"telegram","status":"committed","rows_new":...}

# then: send from a second, unlinked chat -> refusal, and no new batch
curl -s -b j localhost:8080/api/v1/imports | jq 'length'   # unchanged
```

## Done checklist

- [ ] `make verify` green; stages 01–07 demos still work
- [ ] Export forwarded from the phone imports end to end
- [ ] Unlinked chat refused; **nothing stored**
- [ ] Unmapped names reply with a link and commit nothing
- [ ] Exactly four commands; none of the excluded ones exist
- [ ] Bot contains **no** parsing, dedup or aggregation logic
- [ ] Disabled by default; boot fails fast if enabled without a token
- [ ] Token redacted everywhere, including the download URL
- [ ] Offset persisted — restart neither replays nor skips
- [ ] A bot panic cannot take down the HTTP server
- [ ] Old bot's token rotated before any real use
