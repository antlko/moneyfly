/*
 * Shell-only service worker.
 *
 * It caches the app shell — HTML, JS, CSS, icons — and nothing else. API
 * responses are never cached, deliberately: a stale balance shown as current is
 * worse than no balance at all, and offline *writes* are out of scope by decision
 * (docs/adr/0012-defer-offline-entry.md).
 *
 * The consequence is that iOS evicting storage after about seven days unused
 * costs exactly one reload and never a transaction.
 */

const CACHE = 'moneyapp-shell-v2'

/* The navigation fallback. Everything else is cached as it is requested. */
const SHELL = ['/', '/index.html', '/manifest.json', '/icon-192.png', '/icon-512.png']

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(SHELL))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  /* Drop every older version, so a deploy cannot leave two shells fighting. */
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

/**
 * The API is never cached, in either direction.
 *
 * `/version` is on the list because it reports the running build: a cached copy
 * would keep claiming the old one after a deploy, which is exactly the question
 * that endpoint exists to answer.
 */
const NEVER_CACHED = ['/readyz', '/healthz', '/version']

function isAPI(url) {
  return url.pathname.startsWith('/api/') || NEVER_CACHED.includes(url.pathname)
}

self.addEventListener('fetch', (event) => {
  const request = event.request
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (url.origin !== self.location.origin || isAPI(url)) return

  /* Navigation: try the network so a deploy is picked up, fall back to the
     cached shell so the app still opens with no connection. */
  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request).catch(() => caches.match('/index.html').then((r) => r || caches.match('/'))),
    )
    return
  }

  /* Assets: cache-first. They are content-hashed by the build, so a stale hit is
     a hit on exactly the file that was asked for. */
  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached) return cached
      return fetch(request).then((response) => {
        if (response && response.status === 200 && response.type === 'basic') {
          const copy = response.clone()
          caches.open(CACHE).then((cache) => cache.put(request, copy))
        }
        return response
      })
    }),
  )
})
