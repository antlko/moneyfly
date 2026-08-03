<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import type { ImportBatch, ImportMapping, ImportUnmappedName } from '@/api/types'
import { formatMoney } from '@/lib/money'
import { pushToast } from '@/lib/toast'
import { useOnline } from '@/lib/offline'
import { useImportStore } from '@/stores/imports'
import { useTaxonomyStore } from '@/stores/taxonomy'

/**
 * The import flow from docs/08-ux.md §8.6.
 *
 * The mapping step is the centrepiece and the antidote to the 14% silent loss:
 * unknown names are unmissable and blocking, each with its row count, and a
 * fuzzy suggestion that is pre-selected but still requires confirmation.
 */
const imports = useImportStore()
const taxonomy = useTaxonomyStore()

const fileInput = ref<HTMLInputElement | null>(null)
const dragging = ref(false)
const error = ref('')
const { online } = useOnline()
const committed = ref<ImportBatch | null>(null)

/** One chosen target per unknown source name, seeded from the suggestion. */
const categoryChoice = ref<Record<string, number | ''>>({})
const accountChoice = ref<Record<string, number | ''>>({})

const preview = computed(() => imports.preview)

const unmappedCount = computed(
  () =>
    (preview.value?.unmapped_categories.length ?? 0) +
    (preview.value?.unmapped_accounts.length ?? 0),
)

const everyNameChosen = computed(() => {
  const cats = preview.value?.unmapped_categories ?? []
  const accts = preview.value?.unmapped_accounts ?? []
  return (
    cats.every((n) => !!categoryChoice.value[n.source_name]) &&
    accts.every((n) => !!accountChoice.value[n.source_name])
  )
})

const currencyPairs = computed(() => Object.entries(preview.value?.by_currency ?? {}))

function seedChoices() {
  categoryChoice.value = {}
  accountChoice.value = {}
  for (const n of preview.value?.unmapped_categories ?? []) {
    // Pre-selected, never pre-applied: the batch stays blocked until this is
    // confirmed with the button below.
    categoryChoice.value[n.source_name] = n.suggestion?.id ?? ''
  }
  for (const n of preview.value?.unmapped_accounts ?? []) {
    accountChoice.value[n.source_name] = n.suggestion?.id ?? ''
  }
}

function pick() {
  fileInput.value?.click()
}

async function onFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (file) await send(file)
}

async function onDrop(event: DragEvent) {
  dragging.value = false
  const file = event.dataTransfer?.files?.[0]
  if (file) await send(file)
}

async function send(file: File) {
  error.value = ''
  committed.value = null
  try {
    const batch = await imports.upload(file)
    seedChoices()
    if (batch.already_imported) {
      pushToast(`This exact file was already imported as batch #${batch.id}.`, 'info')
    }
  } catch (e) {
    error.value = messageFor(e)
  }
}

async function confirmMappings() {
  if (!preview.value) return
  const categories: ImportMapping[] = []
  const accounts: ImportMapping[] = []
  for (const [source_name, target_id] of Object.entries(categoryChoice.value)) {
    if (target_id) categories.push({ source_name, target_id: Number(target_id) })
  }
  for (const [source_name, target_id] of Object.entries(accountChoice.value)) {
    if (target_id) accounts.push({ source_name, target_id: Number(target_id) })
  }
  error.value = ''
  try {
    await imports.applyMappings(preview.value.batch_id, { categories, accounts })
    seedChoices()
    pushToast('Mappings saved. Those names will never ask again.', 'success')
  } catch (e) {
    error.value = messageFor(e)
  }
}

async function commit() {
  if (!preview.value) return
  const id = preview.value.batch_id
  error.value = ''
  try {
    const batch = await imports.commit(id)
    committed.value = batch
    pushToast(`Imported ${batch.rows_new} transactions.`, 'success', {
      label: 'Undo',
      run: () => revert(id),
    })
  } catch (e) {
    error.value = messageFor(e)
  }
}

async function revert(batchID: number) {
  error.value = ''
  try {
    await imports.revert(batchID)
    committed.value = null
    pushToast('Import reverted. Every row it created is gone from reports.', 'info')
  } catch (e) {
    error.value = messageFor(e)
  }
}

function startOver() {
  imports.reset()
  committed.value = null
  error.value = ''
}

function messageFor(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return e instanceof Error ? e.message : 'The import could not be processed.'
}

function suggestionLabel(name: ImportUnmappedName): string {
  if (!name.suggestion) return 'no close match — choose one'
  return name.suggestion.confidence === 'high'
    ? `looks like “${name.suggestion.name}”`
    : `closest match: “${name.suggestion.name}”`
}

function shortDate(value: string | null): string {
  if (!value) return '—'
  return new Date(value).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

onMounted(async () => {
  await Promise.all([taxonomy.load(), imports.loadBatches()])
})
</script>

<template>
  <section>
    <header class="mb-4">
      <h1 class="text-lg font-semibold">Import</h1>
      <p class="text-sm text-slate-500">
        A Monefy CSV export. Nothing is stored until you commit, and every batch can be undone.
      </p>
    </header>

    <!-- Step 1: choose a file. -->
    <div
      v-if="!preview"
      class="mb-4 rounded-2xl border-2 border-dashed p-6 text-center transition-colors"
      :class="
        dragging
          ? 'border-brand-500 bg-brand-50 dark:bg-brand-900/20'
          : 'border-slate-300 dark:border-slate-700'
      "
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
    >
      <p class="mb-1 text-sm font-medium">Drop the export here</p>
      <p class="mb-4 text-xs text-slate-500">
        Monefy → Settings → Export to CSV, then send the file to this device.
      </p>
      <button
        type="button"
        class="tap-target rounded-xl bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
        :disabled="imports.uploading || !online"
        @click="pick"
      >
        {{ imports.uploading ? 'Parsing…' : 'Choose a file' }}
      </button>
      <input ref="fileInput" type="file" accept=".csv,text/csv" class="sr-only" @change="onFile" />
    </div>

    <p
      v-if="error"
      class="mb-4 rounded-xl border border-state-severe/40 bg-state-severe/10 p-3 text-sm text-state-severe"
      role="alert"
    >
      {{ error }}
    </p>

    <template v-if="preview">
      <!-- Step 2: mapping. Blocking, unmissable, and never skipped. -->
      <div
        v-if="unmappedCount > 0"
        class="mb-4 rounded-2xl border border-state-approach/50 bg-state-approach/10 p-4"
      >
        <h2 class="mb-1 flex items-center gap-2 text-sm font-semibold text-state-approach">
          <span aria-hidden="true">!</span>
          {{ unmappedCount }} unrecognised name{{ unmappedCount === 1 ? '' : 's' }}
        </h2>
        <p class="mb-3 text-xs text-slate-600 dark:text-slate-300">
          {{ preview.rows_unmapped }} row{{ preview.rows_unmapped === 1 ? '' : 's' }} cannot be
          filed until each name below is mapped. Nothing is imported in the meantime — the
          alternative is dropping them silently, which is how 14% of the old data disappeared.
        </p>

        <ul class="mb-3 space-y-2">
          <li
            v-for="name in preview.unmapped_categories"
            :key="'c-' + name.source_name"
            class="rounded-xl bg-white p-3 dark:bg-slate-900"
          >
            <div class="mb-2 flex items-baseline justify-between gap-2">
              <span class="font-mono text-sm font-semibold">{{ name.source_name }}</span>
              <span class="text-xs text-slate-500">{{ name.row_count }} rows</span>
            </div>
            <p v-if="name.reason" class="mb-2 text-xs text-slate-500">{{ name.reason }}</p>
            <label class="block">
              <span class="sr-only">Map {{ name.source_name }} to a category</span>
              <select
                v-model="categoryChoice[name.source_name]"
                class="tap-target w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-800"
              >
                <option value="">Choose a category…</option>
                <option v-for="c in taxonomy.categories" :key="c.id" :value="c.id">
                  {{ c.icon }} {{ c.name }}
                </option>
              </select>
            </label>
            <p class="mt-1 text-xs text-slate-400">{{ suggestionLabel(name) }}</p>
          </li>

          <li
            v-for="name in preview.unmapped_accounts"
            :key="'a-' + name.source_name"
            class="rounded-xl bg-white p-3 dark:bg-slate-900"
          >
            <div class="mb-2 flex items-baseline justify-between gap-2">
              <span class="font-mono text-sm font-semibold">{{ name.source_name }}</span>
              <span class="text-xs text-slate-500">{{ name.row_count }} rows</span>
            </div>
            <label class="block">
              <span class="sr-only">Map {{ name.source_name }} to an account</span>
              <select
                v-model="accountChoice[name.source_name]"
                class="tap-target w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-800"
              >
                <option value="">Choose an account…</option>
                <option v-for="a in taxonomy.postableAccounts" :key="a.id" :value="a.id">
                  {{ a.name }} ({{ a.currency }})
                </option>
              </select>
            </label>
            <p class="mt-1 text-xs text-slate-400">{{ suggestionLabel(name) }}</p>
          </li>
        </ul>

        <button
          type="button"
          class="tap-target w-full rounded-xl bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="!everyNameChosen || imports.busy"
          @click="confirmMappings"
        >
          Confirm {{ unmappedCount }} mapping{{ unmappedCount === 1 ? '' : 's' }}
        </button>
        <p v-if="!everyNameChosen" class="mt-2 text-center text-xs text-slate-500">
          Every name needs a target before the import can continue.
        </p>
      </div>

      <!-- Step 3: preview. -->
      <div
        class="mb-4 rounded-2xl bg-gradient-to-br from-brand-700 to-brand-900 p-5 text-white shadow-lg"
      >
        <p class="text-xs uppercase tracking-wide text-brand-200">
          {{ preview.filename || 'Export' }}
        </p>
        <p class="money mt-1 text-left text-4xl font-bold tabular-nums">
          {{ preview.rows_new }}
        </p>
        <p class="mt-1 text-sm text-brand-200">
          new of {{ preview.rows_total }} rows
          <template v-if="preview.date_range">
            · {{ shortDate(preview.date_range.from) }} → {{ shortDate(preview.date_range.to) }}
          </template>
        </p>

        <dl class="mt-4 grid grid-cols-3 gap-3 text-sm">
          <div>
            <dt class="text-brand-200">Duplicate</dt>
            <dd class="money text-left font-semibold">{{ preview.rows_duplicate }}</dd>
          </div>
          <div>
            <dt class="text-brand-200">Unmapped</dt>
            <dd class="money text-left font-semibold">{{ preview.rows_unmapped }}</dd>
          </div>
          <div>
            <dt class="text-brand-200">Rejected</dt>
            <dd class="money text-left font-semibold">{{ preview.rows_rejected }}</dd>
          </div>
        </dl>
        <p class="mt-3 text-xs text-brand-200">
          {{ preview.months_touched }} month{{ preview.months_touched === 1 ? '' : 's' }} touched
          <span v-for="[code, n] in currencyPairs" :key="code"> · {{ n }} {{ code }}</span>
        </p>
      </div>

      <ul v-if="preview.warnings.length" class="mb-4 space-y-1" aria-live="polite">
        <li
          v-for="warning in preview.warnings"
          :key="warning"
          class="rounded-xl border border-slate-200 bg-white p-3 text-xs text-slate-600 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-300"
        >
          {{ warning }}
        </li>
      </ul>

      <!-- Rows that vanished from the source, reported and never auto-deleted. -->
      <details
        v-if="preview.vanished.length"
        class="mb-4 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900"
      >
        <summary class="cursor-pointer text-sm font-medium">
          {{ preview.vanished.length }} stored row{{ preview.vanished.length === 1 ? '' : 's' }}
          missing from this file
        </summary>
        <p class="mt-2 text-xs text-slate-500">
          These are inside the file's date range but absent from it — deleted in Monefy after a
          previous import, most likely. They are flagged, never removed; delete them yourself from
          History if that is what you want.
        </p>
        <ul class="mt-2 space-y-1 text-xs">
          <li
            v-for="row in preview.vanished"
            :key="row.transaction_id"
            class="flex items-center justify-between gap-2"
          >
            <span class="truncate">
              {{ row.occurred_on }} · {{ row.category_name }} · {{ row.account_name }}
            </span>
            <span class="money">{{ formatMoney(row.amount) }}</span>
          </li>
        </ul>
      </details>

      <details
        v-if="preview.rejected.length"
        class="mb-4 rounded-xl border border-state-severe/30 bg-white p-3 dark:bg-slate-900"
      >
        <summary class="cursor-pointer text-sm font-medium text-state-severe">
          {{ preview.rejected.length }} line{{ preview.rejected.length === 1 ? '' : 's' }} could not
          be read
        </summary>
        <ul class="mt-2 space-y-2 text-xs">
          <li v-for="row in preview.rejected" :key="row.line_no">
            <p class="font-medium">Line {{ row.line_no }}: {{ row.reason }}</p>
            <p class="truncate font-mono text-slate-500">{{ row.raw_line }}</p>
          </li>
        </ul>
      </details>

      <!-- Step 4: commit, then undo. -->
      <div v-if="preview.status === 'committed'" class="mb-4" aria-live="polite">
        <p class="rounded-xl border border-state-within/40 bg-state-within/10 p-3 text-sm">
          Committed. {{ committed?.rows_new ?? preview.rows_new }} transactions are in your history.
        </p>
        <div class="mt-2 flex gap-2">
          <button
            type="button"
            class="tap-target flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm dark:border-slate-700"
            :disabled="imports.busy"
            @click="revert(preview.batch_id)"
          >
            Undo this import
          </button>
          <button
            type="button"
            class="tap-target flex-1 rounded-xl bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white"
            @click="startOver"
          >
            Import another
          </button>
        </div>
      </div>

      <div v-else class="mb-6 flex gap-2">
        <button
          type="button"
          class="tap-target flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm dark:border-slate-700"
          @click="startOver"
        >
          Cancel
        </button>
        <button
          type="button"
          class="tap-target flex-1 rounded-xl bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="!imports.canCommit || imports.busy || !online"
          @click="commit"
        >
          Commit {{ preview.rows_new }} row{{ preview.rows_new === 1 ? '' : 's' }}
        </button>
      </div>
    </template>

    <!-- Batch history, with a revert per row. -->
    <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500">History</h2>
    <p v-if="!imports.batches.length" class="text-sm text-slate-500">
      No imports yet. The first one brings years of history in at once.
    </p>
    <ul class="space-y-1">
      <li
        v-for="batch in imports.batches"
        :key="batch.id"
        class="flex items-center gap-3 rounded-xl border border-slate-200 bg-white p-3 text-sm dark:border-slate-800 dark:bg-slate-900"
      >
        <button
          type="button"
          class="min-w-0 flex-1 text-left"
          @click="imports.loadPreview(batch.id).then(seedChoices)"
        >
          <span class="block truncate font-medium">{{ batch.filename || 'export.csv' }}</span>
          <span class="block text-xs text-slate-400">
            {{ shortDate(batch.created_at) }} · {{ batch.status }} · {{ batch.rows_new }} new /
            {{ batch.rows_duplicate }} duplicate
            <template v-if="batch.rows_unmapped"> / {{ batch.rows_unmapped }} unmapped</template>
          </span>
        </button>
        <button
          v-if="batch.status === 'committed'"
          type="button"
          class="tap-target px-2 text-xs text-state-severe underline"
          :disabled="imports.busy"
          @click="revert(batch.id)"
        >
          revert
        </button>
      </li>
    </ul>
  </section>
</template>
