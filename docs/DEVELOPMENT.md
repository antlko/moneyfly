# Development

## Prerequisites

Go (the version in `backend/go.mod`) and Node 24. Docker only if you want to build the image.

> The `web-ui` toolchain wants Node `^22.18.0 || >=24.12.0`. Older 24.x installs work but `npm
> install` prints `EBADENGINE` warnings; upgrade if they bother you or if `npm ci` starts failing.

## Running the two dev servers

```bash
make dev-api
```

```bash
make dev-ui
```

Go serves the API on `:8080`; Vite serves the SPA on `:5173` and proxies `/api` to it. Develop
against the Vite URL — the Go server on its own only has the placeholder SPA embedded.

Both read `./config`. It is gitignored apart from the example, so your local database never gets
committed.

## Building the single binary

```bash
make build
```

Builds the SPA, copies it over `backend/internal/web/dist`, then compiles a static
`CGO_ENABLED=0` binary at `./moneyfly`. `docker build` does the same three steps.

`backend/internal/web/dist/index.html` is a **tracked placeholder** so `//go:embed all:dist`
compiles in a fresh checkout. `make clean` restores it.

## Tests and lint

```bash
make test
```

```bash
make lint
```

Backend: stdlib `testing`, table-driven, temp-file SQLite per test. Frontend: Vitest over
`src/sync`, `src/lib` and `src/db` only — there is no component test setup on purpose, so verify UI
changes by running the dev servers and looking.

## Testing sync between "two devices"

`device_id` is per browser profile, so a normal window and a private window are two devices.

1. Open the app in both.
2. In DevTools → Network, set one to **Offline**.
3. Record a spend in each.
4. Put the offline one back online.
5. Both windows must show the same balance and the same rows.

For the conflict path: take **both** offline, edit the same transaction differently in each, then
bring both back. Both must end up showing the same winner — if they disagree, the resolution rule is
being applied differently on the two sides, which is the bug `docs/SYNC.md` §2 exists to prevent.

## Testing the PWA on a real phone

**Service workers require HTTPS or `localhost`.** Over `http://192.168.x.x` the browser will not
offer "Add to Home Screen" and offline mode will not work — this is the single most common way to
waste an afternoon here.

Options, easiest first:

- `tailscale serve 8080` — gives a real certificate on your tailnet with no configuration.
- Caddy with a local CA, plus installing that CA on the phone.
- `mkcert` + a trusted root on the phone.

Then, on the phone: install from the home screen, turn on airplane mode, record three spends, turn
it off, and confirm they appear on a second device.

**The service worker only exists in a production build** (`devOptions.enabled: false` — a service
worker and HMR fight over who serves modules). So test offline against `make build` output, not the
dev server. And remember it caches: after rebuilding, load the page **with the server up** so the
worker can fetch the new bundle, then take the network away. Skipping that step tests the previous
build and is a reliable way to conclude a fix did not work when it did.

## Checking against the Monefy reference

Set the browser to 591×1280 (the reference screenshot size) and compare with
[MONEFY-PARITY.md](MONEFY-PARITY.md) — header height, balance pill radius, record button diameter,
the 4-column category grid, keypad key order.

For the responsive switch, drag the window from 1280px down to 375px. The change to the mobile shell
at 640px should be the only jump, and the page must never scroll horizontally at any width.

## Adding a migration

Create `backend/internal/db/migrations/NNNNN_name.sql` with `-- +goose Up` and `-- +goose Down`
sections, dialect `sqlite3`. Migrations are embedded and run inside `db.Open` on every start. There
is no migrate command and the distroless image has no shell — never add a manual migration step.
Never edit a migration that has already been applied anywhere.

## Regenerating the PWA icons

```bash
make icons
```

Edit the SVGs in `web-ui/public/` first. The script prefers `rsvg-convert` and falls back to macOS's
`qlmanage`. The PNGs are committed, so this only runs when a source SVG changes.

## Checking exchange rates locally

`fx.enabled: true` in the config is not enough to see anything: the refresh only fetches currencies
the instance actually uses, so an instance holding nothing but euro correctly does nothing. Add an
account in another currency and restart — the startup catch-up runs when today's rates are missing:

```bash
curl -s localhost:8080/api/fx/latest | head -c 400
```

To exercise conversion end to end, record a spend in HUF (0 decimals — the case that catches an
exponent bug) against a euro base currency. The dashboard total should include it, the transaction
row should show the forint amount large with the euro value small underneath, and the whole thing
should keep working with the network off, from the cached rates.

The provider tests run against recorded fixtures in `backend/internal/fx/testdata`, never the live
endpoints — CI must not depend on someone else's uptime.

## Cutting a release

Releases are tagged, not pushed. `.github/workflows/docker-publish.yml` fires on `v*` tags only:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

That builds `linux/amd64` and `linux/arm64` and pushes `ghcr.io/antlko/moneyfly` as `:0.1.0`,
`:0.1`, `:latest` and `:sha-<short>`. Logging in to GHCR uses the `GLOGIN_TOKEN` repository secret
(a PAT with `write:packages`), matching upmonitor.

Two things are easy to get wrong here and both have bitten this pattern before:

- **The tag name is the version.** It is passed through as `VERSION=${{ github.ref_name }}` and
  baked into the binary with `-ldflags -X`. Drop the build arg and every published image reports
  `dev` from `/api/health`.
- **`latest` is gated on the tag, not on the default branch.** The idiomatic
  `enable={{is_default_branch}}` is never true under a `tags:` trigger, so it silently produces no
  `latest` at all — while the README tells people to pull exactly that.

Verify a release with:

```bash
docker run --rm -p 8080:8080 ghcr.io/antlko/moneyfly:latest
```

and check that `curl localhost:8080/api/health` reports the tag you just pushed.
