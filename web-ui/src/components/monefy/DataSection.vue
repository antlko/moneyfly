<script setup lang="ts">
import { onMounted, ref } from 'vue'

import * as http from '@/api/http'
import type { EraseResult, EraseScope, ImportBatch } from '@/api/http'
import { useDashboardStore } from '@/stores/dashboard'
import { useNotifyStore } from '@/stores/notify'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'

/**
 * Settings → Data: undo one CSV import, or erase records wholesale.
 *
 * Both are done by the server, which writes tombstones through the ordinary op
 * path so every device learns about them like any other delete — and, unlike
 * a delete made by hand, forgets the rows' natural keys, so the same file can
 * be imported again afterwards (backend/internal/api/handlers_erase.go).
 *
 * Awaiting the network is legitimate here for the same reason it is on the
 * import's commit: an explicit, confirmed action that is a request by nature,
 * not an ordinary write.
 */
const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
const notify = useNotifyStore()

const imports = ref<ImportBatch[]>([])
const busy = ref(false)
const message = ref('')
const error = ref('')

/** The action awaiting its second tap, with the sentence that explains it. */
type Pending =
  | { kind: 'import'; batch: ImportBatch; text: string }
  | { kind: 'erase'; scope: EraseScope; text: string }
const pending = ref<Pending | null>(null)

onMounted(loadImports)

async function loadImports() {
  try {
    imports.value = await http.listImports()
  } catch (e) {
    // Offline is the ordinary reason. The erase buttons still render; they
    // will fail with a message of their own if pressed.
    imports.value = []
    if (!(e instanceof http.NetworkError)) notify.fromError(e, 'Could not load your imports')
  }
}

const label = (b: ImportBatch) => (b.legacy ? 'Earlier imports' : b.fileName || 'CSV import')
const when = (unix: number) => (unix ? new Date(unix * 1000).toLocaleString() : '')
const records = (n: number) => `${n.toLocaleString()} record${n === 1 ? '' : 's'}`

function askUndo(batch: ImportBatch) {
  pending.value = {
    kind: 'import',
    batch,
    text: `Remove the ${records(batch.rows)} imported from “${label(batch)}”? Accounts and categories stay. You can import the file again afterwards.`,
  }
}

function askErase(scope: EraseScope) {
  pending.value = {
    kind: 'erase',
    scope,
    text:
      scope === 'records'
        ? 'Delete every record — expenses, incomes and transfers — on all your devices? Accounts, categories and budgets stay.'
        : 'Delete everything — records, accounts, categories, budgets and recurring payments — on all your devices? The default categories and a Cash account are put back.',
  }
}

async function confirm() {
  const action = pending.value
  if (!action) return
  pending.value = null
  busy.value = true
  message.value = ''
  error.value = ''
  try {
    // Push first, so an edit still in the outbox is part of what gets erased
    // rather than something that arrives afterwards and outlives it.
    await sync.sync()
    const res: EraseResult =
      action.kind === 'import'
        ? await http.undoImport(action.batch.id)
        : await http.eraseData(action.scope)
    // Pull the tombstones now rather than on the next event, so the dashboard
    // is already empty when the person goes back to it.
    await sync.sync()
    if (action.kind === 'erase' && action.scope === 'everything') {
      await taxonomy.restoreDefaults(dashboard.baseCurrency)
    }

    if (res.failed > 0) {
      error.value = `Removed ${records(res.deleted)}, but ${res.failed} could not be: ${res.failureReason ?? 'unknown error'}. Running it again is safe.`
    } else if (action.kind === 'erase' && action.scope === 'everything') {
      message.value = 'Everything was erased. The default categories and a Cash account are back.'
    } else {
      message.value = `Removed ${records(res.deleted)}.`
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    notify.fromError(e, 'Could not remove the data')
  } finally {
    busy.value = false
    await loadImports()
  }
}
</script>

<template>
  <section class="rounded-2xl bg-mf-surface p-4">
    <h2 class="mb-3 font-medium">Data</h2>

    <p v-if="message" class="mb-3 rounded-lg bg-mf-green-soft/40 p-3 text-sm text-mf-green-dark">
      {{ message }}
    </p>
    <p v-if="error" role="alert" class="mb-3 rounded-lg bg-mf-red/20 p-3 text-sm text-mf-red-text">
      {{ error }}
    </p>

    <h3 class="mb-2 text-sm font-medium">Imports</h3>
    <p v-if="!imports.length" class="mb-4 text-sm text-mf-muted">No imported records to undo.</p>
    <ul v-else class="mb-4 space-y-3">
      <li v-for="b in imports" :key="b.id" class="flex items-start justify-between gap-3 text-sm">
        <div class="min-w-0">
          <p class="truncate">{{ label(b) }}</p>
          <p class="text-xs text-mf-muted">
            {{ records(b.rows) }}<template v-if="!b.legacy && b.createdAt"> · {{ when(b.createdAt) }}</template>
          </p>
        </div>
        <button
          type="button"
          :disabled="busy"
          class="shrink-0 text-mf-red-text disabled:opacity-50"
          @click="askUndo(b)"
        >
          Undo
        </button>
      </li>
    </ul>

    <h3 class="mb-1 text-sm font-medium">Erase</h3>
    <p class="mb-3 text-sm text-mf-muted">
      Removes data from every signed-in device and cannot be undone.
      <a :href="http.exportCsvUrl('native')" class="text-mf-green-dark underline">Export a CSV</a>
      first if you might want it back.
    </p>

    <div v-if="pending" class="rounded-xl bg-mf-red/15 p-3 text-sm">
      <p class="mb-3">{{ pending.text }}</p>
      <div class="flex gap-2">
        <button
          type="button"
          class="flex-1 rounded-full border border-mf-muted py-2 font-medium"
          @click="pending = null"
        >
          Cancel
        </button>
        <button
          type="button"
          class="flex-1 rounded-full bg-mf-red-text py-2 font-medium text-white"
          @click="confirm"
        >
          {{ pending.kind === 'import' ? 'Remove' : 'Delete' }}
        </button>
      </div>
    </div>

    <div v-else class="flex flex-col gap-2 sm:flex-row">
      <button
        type="button"
        :disabled="busy"
        class="flex-1 rounded-full border border-mf-red py-2.5 text-sm font-medium text-mf-red-text disabled:opacity-50"
        @click="askErase('records')"
      >
        {{ busy ? 'Working…' : 'Delete all records' }}
      </button>
      <button
        type="button"
        :disabled="busy"
        class="flex-1 rounded-full border border-mf-red py-2.5 text-sm font-medium text-mf-red-text disabled:opacity-50"
        @click="askErase('everything')"
      >
        Reset everything
      </button>
    </div>
  </section>
</template>
