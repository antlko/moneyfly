<p align="center">
  <img src="web-ui/public/icon.svg" width="88" alt="">
</p>

<h1 align="center">moneyfly</h1>

<p align="center">Self-hosted expense tracking that syncs across your devices.</p>

<p align="center">
  <a href="https://github.com/antlko/moneyfly/pkgs/container/moneyfly"><img src="https://img.shields.io/badge/ghcr.io-antlko%2Fmoneyfly-2496ED?logo=docker&logoColor=white" alt="Docker image"></a>
</p>

---

> **Status: usable.** Install it, record spending in three taps, switch periods, run accounts and
> transfers in several currencies, edit and search — all of it working with no connection, syncing
> when one returns. Budgets, recurring records and the Monefy CSV import are still to come. See
> [the roadmap](#roadmap).

moneyfly is a personal expense tracker you run yourself. On a phone it works the way Monefy does,
because that design is hard to beat for the one thing that matters: a spend takes three taps —
tap `−`, type the amount, pick the category. On a wide screen the same app becomes an analytics
dashboard. Your data lives on your server and in your browser, and nowhere else.

## Why

- **Self-hosted, with real sync.** One binary, one SQLite file, one directory to back up. Devices
  sync through your server — no Google Drive, no Dropbox, no vendor.
- **Works offline.** The phone writes to local storage first and syncs in the background, so
  recording a coffee in a basement café works exactly as well as at home.
- **Installs like an app.** It is a PWA: Add to Home Screen on iOS or Android and it runs
  full-screen with its own icon. There is no native app to install and none is planned.
- **Your data stays exportable.** CSV export is configurable down to the column order, with a
  Monefy-compatible profile so you can leave as easily as you arrived.

## Quick start

Pull the published image:

```bash
docker run -d --name moneyfly -p 5007:5007 -v moneyfly-config:/config ghcr.io/antlko/moneyfly:latest
```

Or use Compose — [`docker-compose.yml`](docker-compose.yml) is ready to copy, and pastes straight
into Portainer → Stacks → Add stack:

```bash
docker compose up -d
```

To build from source instead, uncomment the `build:` stanza in that file:

```bash
docker compose up --build
```

Then open <http://localhost:5007>. The first account you create claims the instance and becomes the
admin.

Configuration is optional — a missing `config.yaml` starts with sensible defaults. To customise,
copy `config/config.example.yaml` to `config/config.yaml` and restart. Every field is documented in
[docs/CONFIGURATION.md](docs/CONFIGURATION.md).

> **Installing on a phone needs HTTPS.** Service workers only register over HTTPS or on
> `localhost`, so `http://192.168.x.x` will not offer "Add to Home Screen". Put the instance behind
> Caddy, Tailscale Serve, or any reverse proxy with a certificate.

## Development

```bash
make dev-api
```

```bash
make dev-ui
```

Two servers side by side: Go on `:5007`, Vite on `:5173` proxying `/api` to it. Full instructions,
including how to test sync between two devices, are in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Roadmap

| Phase | | |
| --- | --- | --- |
| 0 | Skeleton — build, serve, embed | ✅ |
| 1 | Accounts: password + OIDC sign-in | ✅ |
| 2 | Sync engine (offline-first op-log) | ✅ |
| 3 | Monefy dashboard and record screens | ✅ |
| 4 | Accounts, transfers, search, periods | ✅ |
| 5 | Multi-currency with daily FX rates | ✅ |
| 6–7 | Budgets, recurring records | |
| 8 | Monefy CSV import | |
| 9 | Export profiles, webhooks, API tokens | |
| 10 | Desktop analytics dashboard | |
| 11 | PWA polish, dark theme, PIN lock | ◐ installable & offline |

## Relationship to Monefy

moneyfly reproduces Monefy's screen layout and interaction flow, which is the part worth keeping.
It contains none of Monefy's artwork, wordmark or code, and is not affiliated with or endorsed by
its authors. Category icons come from [Lucide](https://lucide.dev).

## License

MIT — see [LICENSE](LICENSE).
