import { createPinia } from 'pinia'
import { registerSW } from 'virtual:pwa-register'
import { createApp } from 'vue'

import App from './App.vue'
import './assets/tailwind.css'
import router from './router'

/*
 * Mount only once the router has resolved its first route.
 *
 * `mount` replaces everything inside #app, which includes the inline boot
 * splash in index.html — and `RouterView` renders nothing until the initial
 * navigation completes, guard included. Mounting eagerly therefore swaps a
 * visible splash for a blank page for exactly as long as the guard takes.
 * Waiting for `isReady()` keeps the splash on screen until there is a real
 * screen to put in its place.
 *
 * The guard itself no longer waits on the network (see stores/auth.ts), so this
 * is a frame or two on a warm start, not a stall.
 */
const app = createApp(App).use(createPinia()).use(router)
router.isReady().finally(() => app.mount('#app'))

/*
 * Register the shell cache.
 *
 * `immediate` so a first visit is installable straight away, and the new version
 * takes over on the next launch rather than reloading under the user — a reload
 * mid-entry would throw away whatever they were typing.
 *
 * Asking for persistent storage matters more than it looks on iOS: Safari
 * evicts IndexedDB after about a week of not using a site. Eviction costs no
 * data — the server has everything and the device re-bootstraps — but it does
 * cost the *unsent* queue, which is the one thing that exists nowhere else.
 */
/*
 * …but not while the app is still trying to paint.
 *
 * The precache is ~520 KiB across 41 entries, and `immediate` kicks all of it
 * off inside the first-paint window, competing with the entry chunk for the
 * same connection. Deferring to idle (or `load`, where that is not available)
 * moves the fetches, not the coverage: the same files are cached, a beat later,
 * and offline support is identical.
 *
 * Deliberately *not* narrowing the precache to shrink it — that is the obvious
 * alternative and the wrong one. Every lazily-loaded route is in there on
 * purpose, and dropping them would mean /transfer or /import failing to open in
 * a tunnel, on an app whose whole promise is that it works there.
 */
function whenIdle(fn: () => void): void {
  // Typed as optional deliberately: the DOM lib declares requestIdleCallback
  // unconditionally, so a plain `in` check narrows the fallback branch to
  // `never` and stops compiling — while Safari before 17, very much a target
  // here, does not have it.
  const { requestIdleCallback } = window as Window & {
    requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number
  }
  if (requestIdleCallback) requestIdleCallback(fn, { timeout: 3000 })
  else window.addEventListener('load', fn, { once: true })
}

let updateSW: ReturnType<typeof registerSW> | undefined
whenIdle(() => {
  updateSW = registerSW({ immediate: true })
})
void navigator.storage?.persist?.()

/*
 * Look for a new build when the app comes back to the foreground.
 *
 * An installed PWA is not reloaded the way a tab is. It is opened, used and
 * backgrounded, sometimes for weeks, and the service worker only goes looking
 * for a new version when it is registered — so a phone can sit on a build from
 * three deploys ago and every fix "not work", because it is not running.
 */
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) void updateSW?.()
})

/*
 * Make `:active` work at all on iOS.
 *
 * Safari applies `:active` to a touched element only if the document has a
 * touch listener registered somewhere — an old heuristic for "this page expects
 * to be tapped". Without one, *every* pressed state in the app is dead on an
 * iPhone: the keypad does not shrink, the record buttons do not dip, the
 * account and currency rows do nothing. It looks precisely like a tap that was
 * not received, which is why a tap whose result takes a moment gets repeated.
 *
 * An empty passive listener is the whole fix, and passive so it can never delay
 * a scroll.
 */
document.addEventListener('touchstart', () => {}, { passive: true })

/*
 * Put the shell back after the keyboard closes.
 *
 * iOS scrolls the *visual* viewport to reveal a focused field, and on dismissing
 * the keyboard it does not always put it back. The shell is `position: fixed`,
 * which is measured against the layout viewport, so what is left is an app whose
 * header has slid up under the clock and whose record buttons hang off the
 * bottom — permanently, until the page is reloaded. It looks exactly like a
 * broken layout, and it is the state a phone lands in immediately after signing
 * in, which is the first thing anyone does.
 */
if (typeof window !== 'undefined') {
  const settle = () => window.scrollTo(0, 0)
  window.addEventListener('focusout', settle)
  window.addEventListener('orientationchange', settle)
  window.visualViewport?.addEventListener('resize', settle)
}
