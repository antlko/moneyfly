import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

// In dev the SPA runs on Vite's own server and the Go API on :5007, so /api and
// the SSE stream are proxied. In production both come from the same origin —
// the Go binary serves the built SPA out of its embedded FS.
export default defineConfig({
  plugins: [
    vue(),
    tailwindcss(),
    /*
     * The service worker is what makes an installed moneyfly *open* with no
     * network. The data layer is already offline-first — IndexedDB plus a push
     * queue — but without a cached shell there is nothing to run it: launching
     * the icon in a tunnel would show the browser's offline page.
     *
     * It caches the shell only. API responses are never cached: the sync engine
     * has to see a real failure to know it is offline, and a stale 200 from a
     * cache would leave it believing it had synced.
     */
    VitePWA({
      registerType: 'autoUpdate',
      // The manifest is a real file in public/, so there is one source of truth
      // for it rather than two that can disagree.
      manifest: false,
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,ico,webmanifest}'],
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/api\//],
        // Never let a navigation request resolve to a cached API response.
        runtimeCaching: [],
        cleanupOutdatedCaches: true,
      },
      devOptions: {
        // Off in dev: a service worker and HMR fight over who serves modules.
        enabled: false,
      },
    }),
  ],
  build: {
    // Every browser this PWA supports has these; the default target emits
    // downlevel helpers for engines that could not run the app anyway.
    target: 'es2022',
    rollupOptions: {
      output: {
        /*
         * Split the two dependencies that never change on an app release into
         * their own chunks, so a deploy that touches only our code does not
         * invalidate ~170 KB of vendor bytes the browser already has.
         *
         * Only these two: chunking by "everything in node_modules" is the
         * common version and a worse one — it bundles rarely-used libraries in
         * with the runtime and makes the first load bigger, not smaller.
         */
        manualChunks(id) {
          if (id.includes('/node_modules/dexie/')) return 'dexie'
          if (/\/node_modules\/(@?vue|vue-router|pinia)\//.test(id)) return 'vue'
          return undefined
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:5007',
        changeOrigin: true,
        // The sync event stream is SSE: buffering it would defeat the point.
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes) => {
            if (proxyRes.headers['content-type']?.includes('text/event-stream')) {
              proxyRes.headers['cache-control'] = 'no-cache, no-transform'
            }
          })
        },
      },
    },
  },
})
