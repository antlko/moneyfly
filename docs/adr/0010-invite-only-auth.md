# ADR 0010 — Invite-only registration, server-side sessions

**Status:** Accepted · **Date:** 2026-07-29

## Context

The app is internet-facing behind a reverse proxy and holds complete personal financial history in **plaintext** ([0002](0002-no-encryption.md)). Expected users: one, possibly a partner, possibly a few friends later. `golite` demonstrates JWT plus Google OAuth; `upmonitor` uses bcrypt with sessions and admin/read-only roles.

## Decision

**Invite-only.** No public registration endpoint. The first admin is bootstrapped from config on first run and must change the password at first login. Further users are created by an admin or via a single-use invite.

**Server-side sessions**, not JWT. Opaque random token, stored hashed, in an `HttpOnly; Secure; SameSite=Strict` cookie. Roles: `admin`, `user`, `readonly`.

## Rationale

- Open registration on a personal server hosting plaintext finances is an unnecessary liability. There is no growth goal to serve.
- **Sessions beat JWT here** because revocation is the operation that matters. Logging out a lost phone, or cutting off a user, must be immediate. JWT revocation needs a denylist — which is a session table with extra steps and a worse failure mode.
- No refresh-token dance, no clock-skew issues, no key rotation.
- `SameSite=Strict` gives CSRF protection nearly free, with origin checking on state-changing requests as a second layer.

## Consequences

- A session lookup per request. Trivial against local SQLite, indexed on the token hash.
- Adding a user is a deliberate act. Correct for the threat model.
- No Google OAuth in v1, despite `golite` having an example. It adds a third-party dependency in the login path for one user. Kept as an optional later addition, not removed as a possibility.
- Password reset needs SMTP, which is not guaranteed available. v1 uses a **CLI reset** (`moneyapp reset-password --email`) — no mail dependency, no reset-token attack surface, and the admin has shell access anyway.
- Session TTL of 30 days keeps a home-screen PWA logged in; changing a password revokes all other sessions.
- 2FA is not in v1. Reasonable given invite-only access and a small user set; the schema does not preclude it.

## Controls

| Control | Value |
| --- | --- |
| Hashing | bcrypt, cost 12 |
| Session token | 32 bytes from `crypto/rand`, stored as SHA-256 |
| Cookie | `HttpOnly`, `Secure`, `SameSite=Strict`, host-only |
| Login rate limit | Per-IP, exponential backoff |
| `/link` rate limit | Per-chat, to prevent code guessing |
| Password change | Revokes all other sessions |
| Bootstrap password | Environment only; forced change at first login |
