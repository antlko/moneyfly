import { fileURLToPath } from 'node:url'
import { defineConfig, mergeConfig } from 'vitest/config'

import viteConfig from './vite.config.ts'

/*
 * Tests cover src/sync and src/lib only — the sync engine's conflict resolution
 * and the money/date arithmetic. Vue components are verified by running the dev
 * servers, matching upmonitor's convention of not wiring a component test setup.
 */
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['src/{sync,lib,db}/**/*.test.ts'],
      root: fileURLToPath(new URL('./', import.meta.url)),
    },
  }),
)
