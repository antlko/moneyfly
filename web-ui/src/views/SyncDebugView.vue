<script setup lang="ts">
import { RouterLink } from 'vue-router'

import { db } from '@/db'
import { useLiveQuery } from '@/db/live'
import { sync } from '@/sync/engine'
import { bodyOf } from '@/sync/lww'
import type { Row } from '@/sync/types'
import { useSyncStore } from '@/stores/sync'

/*
 * Phase 2 proof screen. It exists to make convergence visible: open it in a
 * normal window and a private window (two device ids), record spends in both
 * with the network off, then reconnect and watch the lists match.
 *
 * Phase 3a replaces it with the real dashboard. It stays reachable at
 * /debug/sync afterwards, because it is the fastest way to see what the replica
 * actually holds.
 */
const syncStore = useSyncStore()

const rows = useLiveQuery<Row[]>(
  () => db.txn.where('deleted').equals(0).reverse().sortBy('occurredOn'),
  [],
)

const notes = ['Coffee', 'Groceries', 'Taxi', 'Cinema', 'Bread', 'Beer']

async function addRandom() {
  await sync.write('txn', {
    kind: 'expense',
    occurredOn: new Date().toISOString().slice(0, 10),
    amountMinor: -(50 + Math.floor(Math.random() * 5000)),
    currency: 'EUR',
    note: notes[Math.floor(Math.random() * notes.length)],
  })
}

async function rename(row: Row) {
  const body = bodyOf(row)
  await sync.write('txn', { ...body, note: `${String(body.note ?? '')} ✎` }, row.id)
}

const money = (minor: unknown) =>
  typeof minor === 'number' ? `€${(Math.abs(minor) / 100).toFixed(2)}` : '—'
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <header class="bg-mf-green px-4 pt-safe-t pb-3 text-white">
      <div class="flex h-14 items-center gap-3">
        <RouterLink to="/" class="text-sm">‹ Back</RouterLink>
        <h1 class="text-lg font-semibold">Sync</h1>
        <span class="ml-auto text-xs text-white/85">{{ syncStore.label }}</span>
      </div>
    </header>

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(6rem+var(--spacing-safe-b))]">
      <section class="rounded-2xl bg-mf-surface p-4 text-sm">
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt class="text-mf-muted">state</dt>
          <dd class="text-right font-medium">{{ syncStore.state }}</dd>
          <dt class="text-mf-muted">pending</dt>
          <dd class="text-right font-medium">{{ syncStore.pending }}</dd>
          <dt class="text-mf-muted">rows</dt>
          <dd class="text-right font-medium">{{ rows.length }}</dd>
          <dt class="text-mf-muted">live stream</dt>
          <dd class="text-right font-medium">
            {{ syncStore.streamConnected ? 'connected' : 'reconnecting' }}
          </dd>
          <dt class="text-mf-muted">device</dt>
          <dd class="truncate text-right font-mono text-xs">{{ syncStore.deviceId }}</dd>
        </dl>
        <p v-if="syncStore.error" class="mt-2 text-mf-red-text">{{ syncStore.error }}</p>
        <button
          type="button"
          class="mt-3 rounded-full border border-mf-green px-4 py-1.5 text-sm text-mf-green-dark"
          @click="syncStore.syncNow()"
        >
          Sync now
        </button>
      </section>

      <ul class="space-y-2">
        <li
          v-for="row in rows"
          :key="row.id"
          class="flex items-center gap-3 rounded-xl bg-mf-surface px-4 py-3 text-sm"
        >
          <div class="min-w-0 flex-1">
            <p class="truncate">{{ row.note || '(no note)' }}</p>
            <p class="font-mono text-xs text-mf-muted">
              {{ row.occurredOn }} · lamport {{ row.lamport }} ·
              {{ String(row.deviceId).slice(0, 8) }}
            </p>
          </div>
          <span class="font-medium text-mf-red-text">{{ money(row.amountMinor) }}</span>
          <button type="button" class="text-xs text-mf-green-dark" @click="rename(row)">edit</button>
          <button type="button" class="text-xs text-mf-red-text" @click="sync.remove('txn', row.id)">
            delete
          </button>
        </li>
        <li v-if="!rows.length" class="rounded-xl bg-mf-surface px-4 py-6 text-center text-sm text-mf-muted">
          Nothing recorded yet.
        </li>
      </ul>
    </main>

    <footer
      class="fixed inset-x-0 bottom-0 flex justify-center px-6 pt-2 pb-[calc(1rem+var(--spacing-safe-b))]"
    >
      <button
        type="button"
        class="rounded-full bg-mf-green px-8 py-3 font-medium text-white shadow-lg"
        @click="addRandom"
      >
        Record a random expense
      </button>
    </footer>
  </div>
</template>
