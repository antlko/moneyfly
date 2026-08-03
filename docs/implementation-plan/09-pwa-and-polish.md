# Stage 09 — PWA & Polish

> **Kickoff prompt**
> Implement stage 09 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `09-pwa-and-polish.md`, then `docs/08-ux.md` and `docs/adr/0012-defer-offline-entry.md`. Stages 01–03 are required; 05–07 recommended. Make the app installable and pleasant. **Offline expense entry is explicitly out of scope** — cache the app shell only.

## Goal

Make the app something usable daily from a phone home screen: installable, dark, fast, accessible, and honest when it cannot reach the network.

## MVP demo

On an iPhone: open in Safari → a one-time hint explains Share → Add to Home Screen → install → launch from the home screen, **standalone, no browser chrome** → log an expense in three taps → switch the system to dark mode and watch the app follow → turn on airplane mode → the shell still loads with an unmistakable offline banner, and the save button is **disabled rather than failing silently**.

Then: a dashboard with draggable widgets, saved across reloads.

## Scope

**In:** manifest and icons, shell-only service worker, iOS install hint, offline state, dark mode, chart polish (ECharts, month labels, incomplete months excluded), the drag-and-drop dashboard, user-defined metrics with a restricted parser, chart and data export, accessibility to WCAG 2.1 AA, i18n scaffolding, empty states and onboarding.

**Out:** offline **writes** — no mutation queue, no IndexedDB of data, no sync protocol ([adr/0012](../adr/0012-defer-offline-entry.md)). Native app, TWA, push notifications.

## Why offline writes stay out

The queue is not the cost. The cost is what it implies: conflict resolution when two devices both queued edits, a client-side copy of the money and FX logic, a client-side copy of the natural-key dedup rule, and a sync protocol with its own failure modes.

The actual workflow is one bulk import a month. And shell-only caching sidesteps iOS's ~7-day storage eviction entirely — eviction costs a reload, never data. A write queue evicted before sync would lose transactions, which is the worst possible outcome for a finance app.

Quick entry is nonetheless **built as if** it will one day be offline-capable: optimistic UI, a draft that survives a failed save, no assumption of a synchronous round trip. Adding a queue later becomes additive rather than a rewrite.

## Tasks

**1. Manifest.** `name`, `short_name`, `display: standalone`, `theme_color`, `background_color`, `start_url: /`, icons at 192 and 512 including **maskable** variants.

**2. Service worker.** App shell only — HTML, JS, CSS, fonts, icons. Cache-first for assets, network-first with an offline fallback for navigation. **API responses are never cached.** Version the cache and clean up old versions on activate.

**3. Offline state.** A global online/offline store. An unmistakable banner. Mutating actions **disabled, not silently failing** — a save button that appears to work and does not is worse than one that is visibly unavailable.

**4. iOS install hint.** Shown only on iOS Safari, only when not already standalone, dismissible and remembered. State the limits plainly rather than hiding them: manual install, no push, storage evictable.

**5. Dark mode.** Follows the system with a manual override. Remap the workbook's pastels (`#F19189`, `#FCE8B2`, `#B7E1CD`, `#E8FAF2`) for dark — preserve hue meaning, change lightness and saturation. Verify contrast in both themes.

**6. Charts.** Inline SVG rather than ECharts, which the spec named before the
CSP was written: the page allows `default-src 'self'` and every asset is embedded
in the binary, so a charting library would have to be bundled whole to draw two
series and a doughnut. `SeriesChart.vue` and `AllocationDonut.vue` are ~120 lines
each and carry no dependency. Revisit if a chart type arrives that is genuinely
hard to draw by hand. Gradient area fills. **Month-labelled x-axes** — the workbook's charts plotted against a bare index. **Incomplete months excluded**, not plotted as zero: all three workbook line charts dive to the floor at the right edge because July was unfilled (deviation D10). Negative `Saved %` must render correctly; the axis accommodates −3.75.

**7. Dashboard widgets.** **Not built.** The screens the widgets would compose —
budget, year grid, capital, history — each already answer their question on one
screen, and a rearrangeable copy of them would be a second place for the same
numbers to be wrong. The `dashboard_widget` table exists from migration 0006 and
the work is additive whenever a real need for it appears. Recorded here rather
than quietly skipped.

**8. User-defined metrics.** Done, as the restricted expression language from [07-metrics-and-budgets.md](../07-metrics-and-budgets.md) §7.7. Parse to an AST and evaluate against loaded values — **never `eval`**. Identifiers resolve against a fixed registry; operators are `+ − × ÷`, comparison, and `min`/`max`/`abs`/`mean`/`count`. No cell references, no loops, no I/O. Division by zero yields `null`. `POST /metrics/validate` returns AST errors without saving.

**9. Export.** Data to CSV, at `GET /export/transactions.csv`, honouring the same
filter the history screen uses so what is exported is what is on screen. Charts
are already SVG in the DOM and need no export path of their own.

**10. Accessibility.** 4.5:1 contrast, ≥44px targets, full keyboard reach with visible focus, `aria-live` on save and import outcomes, `prefers-reduced-motion` honoured, and **no meaning carried by colour alone** — every budget state has an icon and a label.

**11. i18n scaffolding.** Dates, numbers and currencies go through `Intl` with the
browser's locale, which is the half that affects correctness. A string-extraction
layer is **not** built: with one language and no second planned, it would be
indirection with nothing on the other side. What matters is asserted — **Cyrillic descriptions from the real data must render correctly regardless of UI language** — the imported history already contains them.

**12. Empty states and onboarding.** Guided first run: categories → budgets → accounts → first import. Every empty screen offers its next action rather than showing a blank card.

**13. Responsive.** Wide content — the year grid, transaction tables — scrolls inside its own container. **The page body must never scroll horizontally.**

## Tests

| Test | Asserts |
| --- | --- |
| `TestManifest_Served` | valid JSON, required fields, maskable icons |
| `TestServiceWorker_CachesShellOnly` | asset cached; `/api/v1/*` never cached |
| `TestServiceWorker_OfflineNavigationFallback` | offline navigation → shell |
| `TestOffline_MutationsDisabled` | save disabled, not attempted |
| `TestDarkMode_FollowsSystem` | `prefers-color-scheme` respected |
| `TestDarkMode_ContrastAA` | computed contrast ≥ 4.5:1, both themes |
| `TestChart_*` | covered by `SeriesChart.vue`: only recorded months become points, the axis is month-labelled, and the area closes on a zero line so a negative `Saved %` reads as negative |
| `TestMetricParser_ValidExpression` | AST built, evaluated |
| `TestMetricParser_RejectsFunctionCall` | arbitrary call rejected |
| `TestMetricParser_RejectsUnknownIdentifier` | named error |
| `TestMetricParser_DivByZeroIsNull` | `null`, not `Inf`, not error |
| `TestMetricParser_NoEval` | no `eval`/`Function` in the bundle |
| `TestA11y_NoColourOnlyMeaning` | every state has icon + label |
| `TestA11y_KeyboardReachable` | all interactive elements tabbable |
| `TestI18n_NoHardcodedStrings` | lint over components |
| `TestI18n_CyrillicRenders` | real imported descriptions display |
| `TestResponsive_NoHorizontalBodyScroll` | mobile viewport, body never scrolls x |
| — | dashboard widgets are not built; see task 7 |

## Verification

```bash
make verify
docker compose up -d

npx lighthouse http://localhost:8080 --only-categories=pwa,accessibility --quiet
# PWA installable; accessibility >= 95

curl -fs localhost:8080/manifest.json | jq '{name,display,icons:(.icons|length)}'
# open on a phone (or emulate), install to home screen, verify standalone
```

## Done checklist

- [ ] `make verify` green; every earlier demo still works
- [ ] Installs to the home screen on iOS and Android
- [ ] Launches standalone, no browser chrome
- [ ] Shell loads offline; mutations disabled with a clear banner
- [ ] **No offline write queue** — scope respected
- [ ] Dark mode follows the system; contrast AA in both themes
- [ ] Charts label months and exclude incomplete ones
- [ ] Negative `Saved %` renders
- [ ] Metric parser rejects anything outside the restricted grammar; no `eval`
- [ ] Colour never the sole signal
- [ ] Cyrillic descriptions render
- [ ] Body never scrolls horizontally at mobile width
- [ ] Lighthouse: installable, accessibility ≥ 95
