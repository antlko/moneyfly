# ADR 0005 — PWA only, Vue 3, no React Native

**Status:** Accepted · **Date:** 2026-07-29

## Context

iOS App Store release costs money, which the owner explicitly wants to avoid. Android matters only as a contingency — Monefy on Android is a recurring subscription that has not been bought. The brief floated React Native with Expo as a possible route.

That choice cascades: a genuine React Native plan argues for React on the web so components, types and the API client can be shared. But both reference repos (`golite`, `upmonitor`) are Vue, and `upmonitor` specifically is Vue 3 + TS + Pinia + Tailwind v4 + shadcn-vue.

Asked directly, the owner chose PWA only with Vue.

## Decision

**Vue 3 + TypeScript + Pinia + Tailwind v4 + shadcn-vue**, embedded in the Go binary. Installable PWA via Add to Home Screen on iOS and the standard prompt on Android. **No React, no React Native, no native app.**

## Rationale

- Matches `upmonitor` exactly — a stack the author already operates, with component patterns to copy.
- One codebase, one artefact, one deploy. No app-store review, no signing, no release cadence.
- The PWA covers the actual contingency: if Monefy access is lost, expense entry moves into this app on any device with a browser.
- React Native would mean a second build system, a second dependency tree and a second UI to keep in sync, to avoid an install flow that takes three taps.

## Consequences

- **iOS PWA limits are accepted**: manual install, no push unless installed, no Background Sync, storage evictable after ~7 days unused. Mitigations: a one-time install hint on iOS Safari, and caching **only** the app shell so eviction costs a reload rather than data. Nothing in v1 depends on push notifications.
- No app-store presence or discoverability. Irrelevant for a personal tool.
- Should native ever become necessary, the REST API is the reuse boundary — the frontend would be rewritten. Accepted as unlikely.
- A Trusted Web Activity wrapper remains available for Android if Play Store presence is ever wanted; it needs no change to the app.

## Alternatives rejected

| Option | Why not |
| --- | --- |
| React web + Expo later | Diverges from both reference repos to hedge an unplanned future |
| Native iOS | Direct cost the owner rejected |
| TWA now | Adds a build target while the PWA already suffices |
