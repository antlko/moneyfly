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
registerSW({ immediate: true })
void navigator.storage?.persist?.()
