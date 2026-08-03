# 10 — Deployment, Operations and CI/CD

## 10.1 Model

One container, one config file, one volume — `upmonitor`'s model, already proven on the target host.

```bash
docker run -d \
  --name moneyapp \
  -p 8080:8080 \
  -v moneyapp-config:/config \
  --restart unless-stopped \
  ghcr.io/antlko/moneyapp:latest
```

```yaml
# docker-compose.yml
services:
  moneyapp:
    image: ghcr.io/antlko/moneyapp:latest
    container_name: moneyapp
    ports:
      - "8080:8080"
    volumes:
      - moneyapp-config:/config
    environment:
      MONEYAPP_TELEGRAM_TOKEN: "${MONEYAPP_TELEGRAM_TOKEN}"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/moneyapp", "healthcheck"]
      interval: 30s
      timeout: 3s
      retries: 3
      start_period: 10s

volumes:
  moneyapp-config:
```

Port **8080**, matching `upmonitor` — not the research report's 80. TLS is terminated by the existing nginx-proxy-manager; the container is never directly exposed.

The healthcheck invokes the binary itself rather than requiring `curl` in the image, which keeps the base distroless.

## 10.2 Volume layout

```
/config/
├── config.yaml
├── moneyapp.db            # + -wal, -shm
├── uploads/<user>/<sha256>
└── backups/moneyapp-YYYY-MM-DDTHH.db.gz
```

Everything that matters is in one volume, so backup and restore are "copy this directory".

## 10.3 Configuration

`config.yaml` is authoritative; environment variables override individual keys. Secrets come from the environment only.

```yaml
server:
  addr: ":8080"
  base_url: "https://money.example.com"   # absolute links in bot messages

database:
  path: "/config/moneyapp.db"
  max_open_conns: 4      # SQLite: writes serialise; a large pool adds contention
  max_idle_conns: 2
  busy_timeout_ms: 5000

auth:
  session_ttl: "720h"
  bcrypt_cost: 12
  bootstrap_admin_email: "admin@example.com"   # first run only
  # bootstrap password from MONEYAPP_BOOTSTRAP_PASSWORD; forced change on login

telegram:
  enabled: false         # opt-in; long-polling only
  token: ""              # MONEYAPP_TELEGRAM_TOKEN
  max_file_bytes: 20971520

providers:
  fx:
    primary: open-er-api
    fallback: fawazahmed0
    refresh: "24h"
    plausibility_max_change: 0.15   # reject a >15% single-day move; flag for review

backup:
  enabled: true
  schedule: "0 3 * * *"
  keep: 14

log:
  level: info
  format: json
  mask_pii: true
```

**Validated at boot.** The process refuses to start on an unusable config rather than failing hours later: `telegram.enabled` with an empty token, an unwritable database path, a `bcrypt_cost` below 10, a `base_url` that is not absolute. Failure prints the offending key and exits non-zero.

No secret ever has a source-code default. This is the direct lesson from `main.go:18-21` of the existing parser, where a live Telegram token and Trading212 key sit as constants under a `// TODO: HIDE IT!!!!!!` comment and are now in git history. **Rotate the Telegram token; revoke the Trading212 key** — nothing here will use it.

In fact the only secret this app has is the Telegram bot token, and that is optional. Every provider is keyless, so a deployment with the bot disabled needs no secrets at all.

## 10.4 Image

Multi-stage, `CGO_ENABLED=0`, distroless static, `amd64` + `arm64`.

```dockerfile
FROM node:22-alpine AS ui
WORKDIR /ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist ./internal/transport/rest/assets/dist
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/moneyapp ./cmd/moneyapp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/moneyapp /moneyapp
USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/config"]
ENTRYPOINT ["/moneyapp"]
```

`CGO_ENABLED=0` is only possible because the SQLite driver is pure Go. That single choice is what keeps arm64 a cross-compile rather than an emulated build — and it is why SQLCipher was rejected ([adr/0002](adr/0002-no-encryption.md)).

Two corrections to the sketch above, both found by running it:

**The build stage is `golang:1.25-alpine`, not 1.23.** `modernc.org/sqlite` and `goose`
declare `go 1.25` in their own `go.mod` files, so an older toolchain cannot compile
them.

**`/config` must exist in the image, owned by `nonroot`.** Docker initialises an empty
named volume from the image's directory, so without it the volume is created
root-owned and the non-root process cannot create the database. The failure is loud
and correct — `config: 1 problem(s): database.path: directory /config is not
writable`, then exit — but it makes a first `docker compose up` fail. distroless has
no shell to `chown` at runtime, so the directory is staged in the build layer and
copied with `--chown=nonroot:nonroot`.

```dockerfile
RUN mkdir -p /out/config                                   # in the build stage
COPY --from=build --chown=nonroot:nonroot /out/config /config
```

The UI is compiled into the binary via `embed.FS`, so there is no static-file volume and no version skew between API and frontend.

## 10.5 Migrations

goose, forward-only, **run explicitly** — not at boot ([adr/0011](adr/0011-explicit-migrations.md)).

```bash
docker exec moneyapp /moneyapp migrate up
docker exec moneyapp /moneyapp migrate status
```

`/readyz` returns unhealthy while the schema version is behind, so a container started against an unmigrated database refuses traffic instead of writing against a schema it does not understand. Automatic migration at boot risks a partially-applied schema change on a crash-loop restart, against a database holding the only copy of years of financial history.

Upgrade order: `docker compose pull` → `down` → `migrate up` → `up -d`. A pre-migration backup is taken automatically.

## 10.6 Backups

Nightly, in-process, using SQLite's online backup API so a consistent copy is taken without stopping writes:

- `sqlite3_backup` to `/config/backups/moneyapp-<ts>.db`, gzipped
- 14 retained, oldest pruned
- Integrity-checked after write; a failed check keeps the previous backup and alerts via Telegram
- One is always taken immediately before `migrate up`

Backups are **plaintext**, consistent with [adr/0002](adr/0002-no-encryption.md). Anything copied off the host should go somewhere trusted rather than a third-party cloud.

### Restore drill

Documented and expected to be rehearsed, because an untested backup is not a backup:

```bash
docker compose down
gunzip -c backups/moneyapp-2026-07-29T03.db.gz > moneyapp.db
docker compose up -d
docker exec moneyapp /moneyapp migrate status
```

`GET /export` is the format-independent escape hatch — a full JSON dump that survives even a SQLite version problem.

## 10.7 CI

GitHub Actions.

**On pull request** — `lint` (golangci-lint, ESLint, Prettier), `test` (`go test -race ./...` plus Vitest), `parity` (the golden-file suite from [appendix-excel-parity.md](appendix-excel-parity.md)), `build` (both architectures, no push), `migrate-check` (up then down then up on a scratch database).

**On merge to `main`** — the above, then build and push `ghcr.io/antlko/moneyapp:main` and `:sha-<short>`.

**On tag `v*`** — push `:vX.Y.Z` and `:latest`, generate release notes, attach the OpenAPI spec.

Two notes on the linters as configured:

- `.golangci.yml` uses the **v2** schema. A golangci-lint older than v2 cannot read
  it and exits with a confusing viper error, so `make tools` installs a matching
  binary into `./bin` and `make lint` prefers it.
- `misspell` is told to ignore `Studing`, `Clouth`, `Toilery` and `Applience`. Those
  are not typos: they are the source names in the real exports, and the alias table
  exists precisely so they resolve. "Fixing" them would recreate the 14% loss.

The frontend's remaining `npm audit` findings are all one advisory chain
(`brace-expansion`, reachable only through eslint, vue-tsc, openapi-typescript and
test-utils). Nothing ships: the UI compiles to static assets embedded in the binary,
and those tools run on this repository's own source. CI therefore does not gate on
`npm audit`.

Coverage is reported but not gated on a percentage. Two suites are gated absolutely:

1. **Parity** — every metric in §7.8 must match the spreadsheet.
2. **Import fidelity** — the real 1,683-row export must yield **1,683** stored rows. A run producing 1,446 fails the build. This is the regression test for the 14% loss, and it is the single most important test in the repository.

## 10.8 Deploy

Manual, by choice. One user, one host, and an unattended auto-update on a financial database is a poor trade.

```bash
cd /srv/moneyapp
docker compose pull
docker compose down
docker exec moneyapp /moneyapp migrate up   # or run migrate in a one-shot container
docker compose up -d
```

Portainer can watch tags for convenience, but auto-pull is off: an image must never migrate a schema without a human present. There is no staging environment; the gates are the parity and import suites plus a pre-upgrade backup.

## 10.9 Observability

- **Logs** — `log/slog` JSON to stdout, collected by Docker. Request ID on every line. PII masked (passwords, tokens, the bot token embedded in Telegram download URLs).
- **Health** — `/healthz` liveness, `/readyz` checks the database and schema version.
- **Metrics** — none. Prometheus for a single-user app is unjustified. `/readyz` plus logs is the whole surface. Revisit if others are hosted.
- **Errors surfaced where they will be seen**: a failed import, a stale provider or a failed backup sends a Telegram message. A log nobody reads is not monitoring.

## 10.10 Resources

Expected steady state: **~40 MB RSS**, negligible CPU. The database after full history — roughly 1,700 transactions, 12 months of snapshots, years of daily rates — is a few megabytes. It shares a small VPS with other containers comfortably; no limits are required, though `mem_limit: 256m` is a reasonable guard.

## 10.11 Security posture

| Control | Position |
| --- | --- |
| TLS | Terminated at nginx-proxy-manager; HSTS enabled there |
| Cookies | `HttpOnly; Secure; SameSite=Strict` |
| CSRF | `SameSite=Strict` plus origin checking on state-changing requests |
| CSP | Self only; no CDNs — everything is embedded |
| Passwords | bcrypt cost 12; changing one revokes other sessions |
| Registration | Invite/admin only; no open sign-up |
| Rate limits | Per-IP on auth, per-user on import, per-chat on `/link` |
| Container | Distroless, non-root, read-only root filesystem except `/config` |
| Secrets | Environment only, never source defaults, masked in logs |
| Encryption at rest | **None**, by decision ([adr/0002](adr/0002-no-encryption.md)) |

Because there is no encryption at rest, the database file is the sensitive artefact. It should live on a volume that is not shared with any public-facing service, and host-level disk encryption is the appropriate layer for it.

## 10.12 Migrating off the current system

1. Deploy, bootstrap the admin account, change the password.
2. `moneyapp migrate up`.
3. `moneyapp migrate-excel --file "[2025-2026] Budget_ Capital Grow.xlsx"` — seeds categories, accounts, budgets, snapshots and the sheet's FX settings.
4. Import the Monefy CSV through the normal pipeline, resolving any unmapped names. Transaction detail from the CSV takes precedence; the spreadsheet only fills months the CSV cannot cover.
5. Verify the parity report: every metric in §7.8 within rounding of the spreadsheet, except the four documented corrections.
6. Link Telegram, send one export end-to-end.
7. Enable auto FX; confirm rates land and match the sheet's manual values.
8. Run both systems in parallel for one month, then retire the sheet.
9. Stop the old bot, **rotate its Telegram token**, and **revoke the Trading212 key**.
