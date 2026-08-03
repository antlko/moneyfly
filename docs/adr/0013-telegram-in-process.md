# ADR 0013 — Telegram bot: minimal, in-process, opt-in

**Status:** Accepted · **Date:** 2026-07-29 · **Revised:** scope narrowed

## Context

The research report proposed removing the bot entirely: the system *"replaces the current Telegram Bot→Google Sheets pipeline"*. That is a regression — the bot's value is specific and hard to replicate.

But the old bot is also not the model to follow. It grew into a small application: parsing, one-month filtering, aggregation, report formatting, and writing 18 positional values into fixed spreadsheet cells. That accumulation is where several of its defects live.

So the question was not *whether* to keep a bot, but how little it should do.

## Decision

Keep it, **in the same binary**, **long-polling only**, **disabled by default**, doing exactly one thing: **receive a Monefy export and hand the bytes to the importer.**

Commands: document upload, `/link`, `/unlink`, `/help`. Nothing else.

## Rationale

**Keep it,** because the phone never has to reach the server. Monefy shares the CSV to Telegram; the server pulls it. No file transfer to a computer, no inbound endpoint, no VPN, any network.

**Minimal,** because everything beyond ingestion either duplicates the app or invents a new failure surface. Report commands mean two renderers of the same numbers drifting apart. Free-text expense entry means parsing amounts out of prose. Notifications are a separate concern with their own delivery, retry and opt-out semantics.

**In-process,** because the bot needs the importer, the alias table and the database. A separate deployable would need a duplicate of that logic or an internal API, doubling the operational surface for one user.

**Long-polling only,** because webhooks would need a routable HTTPS endpoint and proxy configuration to save a few seconds on a monthly upload. Long-polling also means Telegram buffers while the app is down.

**Off by default,** because Telegram necessarily sees every uploaded file. Web upload is the zero-third-party path, and anyone unwilling to accept the transport should get that by default rather than by remembering to opt out.

## Consequences

- **A bot fault must not take down the web server.** Supervised goroutine: panics recovered per update with the update ID logged, exponential backoff with jitter on poll failure, logged once per transition.
- Poll offset **persisted**, so a restart neither replays nor skips.
- Downloads capped at Telegram's 20 MB `getFile` limit and streamed to disk.
- **Telegram sees every uploaded file** — inherent, accepted, and the reason for opt-in.
- Any chat could previously upload. Now an unlinked chat gets one instructional reply, with `/link` rate-limited per chat.
- One shared bot; `chat_id` → `user_id` linking provides isolation. Per-user tokens would mean per-user secrets in shared config for no benefit.
- Ingestion goes through the **same pipeline as web upload**, so there is one parser, one alias table, one dedup rule and one set of tests — not a second path that can drift.
- The bot token is the app's **only** secret, and it is optional. Every provider is keyless, so a deployment with the bot off needs no secrets at all.

## Revisit when

A bot command would fix a real inconvenience that the app cannot. That is evidence; anticipating it is not.
