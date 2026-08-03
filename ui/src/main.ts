import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { initTheme } from './lib/theme'
import { registerServiceWorker } from './lib/offline'
import './style.css'

// The theme is resolved before the first paint so a dark-mode user never sees a
// white flash.
initTheme()

createApp(App).use(createPinia()).use(router).mount('#app')

// Shell only: assets are cached, API responses never are.
registerServiceWorker()
