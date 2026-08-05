import { createPinia } from 'pinia'
import { registerSW } from 'virtual:pwa-register'
import { createApp } from 'vue'

import App from './App.vue'
import './assets/tailwind.css'
import router from './router'

createApp(App).use(createPinia()).use(router).mount('#app')

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
const updateSW = registerSW({ immediate: true })
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
