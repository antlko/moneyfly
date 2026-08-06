<script setup lang="ts">
import { FileUp } from '@lucide/vue'
import { computed, ref } from 'vue'

import * as http from '@/api/http'
import type { ImportNameStatus, ImportPreview, ImportResult } from '@/api/http'
import NewCategorySheet from '@/components/monefy/NewCategorySheet.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useDashboardStore } from '@/stores/dashboard'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { sync } from '@/sync/engine'

/**
 * The Monefy CSV importer: pick a file, resolve whatever it does not already
 * recognise, commit. See docs/MONEFY-PARITY.md §5 for the format and why an
 * unrecognised category or account is never guessed at — a naive import that
 * did lost 14% of a real export without telling anyone.
 */
const taxonomy = useTaxonomyStore()
const accounts = useAccountsStore()
const dashboard = useDashboardStore()
const settings = useSettingsStore()

type Step = 'pick' | 'preview' | 'done'
const step = ref<Step>('pick')
const busy = ref(false)
const error = ref('')

const fileName = ref('')
const csvText = ref('')
const preview = ref<ImportPreview | null>(null)
const result = ref<ImportResult | null>(null)

/** CSV name -> id, filled in as the operator maps each unresolved one. */
const categoryMap = ref<Record<string, string>>({})
const accountMap = ref<Record<string, string>>({})

const fileInput = ref<HTMLInputElement | null>(null)

async function pickFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  error.value = ''
  busy.value = true
  try {
    fileName.value = file.name
    csvText.value = await file.text()
    preview.value = await http.importMonefyPreview(csvText.value)
    categoryMap.value = {}
    accountMap.value = {}
    step.value = 'preview'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not read that file'
  } finally {
    busy.value = false
    if (fileInput.value) fileInput.value.value = ''
  }
}

const unresolvedCategories = computed(() => preview.value?.categories.filter((c) => !c.resolved) ?? [])
const unresolvedAccounts = computed(() => preview.value?.accounts.filter((a) => !a.resolved) ?? [])
const readyToImport = computed(
  () => preview.value !== null && !unresolvedCategories.value.some((c) => !categoryMap.value[c.name]),
)

// --- mapping: categories, by reusing the record screen's own sheet ---------------

const mappingCategoryFor = ref<ImportNameStatus | null>(null)

async function createMappedCategory(input: {
  name: string
  icon: string
  color: string
  kind: 'expense' | 'income'
}) {
  const csvName = mappingCategoryFor.value?.name
  mappingCategoryFor.value = null
  if (!csvName) return
  const id = await sync.write('category', { ...input, sortOrder: 999, archived: 0 })
  categoryMap.value = { ...categoryMap.value, [csvName]: id }
}

// --- mapping: accounts, a lighter inline form — an account is just a name and
// a currency, not worth a whole second sheet component for ---------------------

const creatingAccountFor = ref<string | null>(null)
const newAccountName = ref('')
const newAccountCurrency = ref('')

function startNewAccount(name: string) {
  creatingAccountFor.value = name
  newAccountName.value = name
  // The reference export names accounts after their currency ("EUR", "HUF");
  // when that happens to be true here too, it is the right default rather
  // than a coincidence to ignore.
  newAccountCurrency.value = /^[A-Za-z]{3}$/.test(name) ? name.toUpperCase() : dashboard.baseCurrency
}

async function createMappedAccount() {
  const csvName = creatingAccountFor.value
  const name = newAccountName.value.trim()
  const currency = newAccountCurrency.value.trim().toUpperCase()
  if (!csvName || !name || currency.length !== 3) return
  const id = await accounts.create({
    name,
    currency,
    icon: 'Wallet',
    color: 'green',
    initialBalanceMinor: 0,
  })
  accountMap.value = { ...accountMap.value, [csvName]: id }
  creatingAccountFor.value = null
}

const currencyOptions = computed(() => {
  const codes = new Set<string>([dashboard.baseCurrency, ...settings.get<string[]>(SETTING.currencies, [])])
  return [...codes].filter((c) => c.length === 3).sort()
})

// --- commit ------------------------------------------------------------------------

async function commit() {
  busy.value = true
  error.value = ''
  try {
    result.value = await http.importMonefyCommit(csvText.value, categoryMap.value, accountMap.value)
    step.value = 'done'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Import failed'
  } finally {
    busy.value = false
  }
}

function startOver() {
  step.value = 'pick'
  preview.value = null
  result.value = null
  csvText.value = ''
  fileName.value = ''
  error.value = ''
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Import Monefy CSV" />

    <main class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <p v-if="error" class="rounded-2xl bg-mf-red/15 p-4 text-sm text-mf-red-text">{{ error }}</p>

      <!-- Step 1: pick a file -->
      <section v-if="step === 'pick'" class="rounded-2xl bg-mf-surface p-4">
        <p class="mb-4 text-sm text-mf-muted">
          Export from Monefy (Settings → Backup → Export to CSV) and choose the file here. Nothing is
          written until you confirm the mapping on the next screen.
        </p>
        <input
          ref="fileInput"
          type="file"
          accept=".csv,text/csv"
          class="hidden"
          @change="pickFile"
        />
        <button
          type="button"
          :disabled="busy"
          class="flex w-full items-center justify-center gap-2 rounded-full bg-mf-green py-3 font-medium text-white disabled:opacity-50"
          @click="fileInput?.click()"
        >
          <FileUp :size="20" :stroke-width="2" />
          {{ busy ? 'Reading…' : 'Choose a CSV file' }}
        </button>
      </section>

      <!-- Step 2: preview and map -->
      <template v-else-if="step === 'preview' && preview">
        <section class="rounded-2xl bg-mf-surface p-4 text-sm">
          <p class="font-medium">{{ fileName }}</p>
          <p class="mt-1 text-mf-muted">
            {{ preview.totalRows }} row{{ preview.totalRows === 1 ? '' : 's' }} read.
            <span v-if="preview.parseErrors.length" class="text-mf-red-text">
              {{ preview.parseErrors.length }} could not be read and will be skipped.
            </span>
          </p>
        </section>

        <section v-if="unresolvedCategories.length" class="rounded-2xl bg-mf-surface p-4">
          <h2 class="mb-1 font-medium">Categories to map</h2>
          <p class="mb-3 text-sm text-mf-muted">
            These names are not in your categories. Map each to an existing one or create a new one —
            a row left unmapped is skipped, not guessed at.
          </p>
          <ul class="divide-y divide-mf-muted/20">
            <li v-for="c in unresolvedCategories" :key="c.name" class="flex items-center gap-3 py-2.5">
              <div class="min-w-0 flex-1">
                <p class="truncate">{{ c.name }}</p>
                <p class="text-xs text-mf-muted">
                  {{ c.count }} row{{ c.count === 1 ? '' : 's' }} · {{ c.kind }}
                </p>
              </div>
              <select
                class="w-40 rounded-lg border border-mf-muted/60 bg-mf-bg px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                :value="categoryMap[c.name] ?? ''"
                @change="
                  ($event.target as HTMLSelectElement).value === '__new__'
                    ? (mappingCategoryFor = c)
                    : (categoryMap = { ...categoryMap, [c.name]: ($event.target as HTMLSelectElement).value })
                "
              >
                <option value="" disabled>Choose…</option>
                <option
                  v-for="opt in c.kind === 'income' ? taxonomy.incomeCategories : taxonomy.expenseCategories"
                  :key="opt.id"
                  :value="opt.id"
                >
                  {{ opt.name }}
                </option>
                <option value="__new__">+ Create "{{ c.name }}"</option>
              </select>
            </li>
          </ul>
        </section>

        <section v-if="unresolvedAccounts.length" class="rounded-2xl bg-mf-surface p-4">
          <h2 class="mb-1 font-medium">Accounts to map</h2>
          <ul class="divide-y divide-mf-muted/20">
            <li v-for="a in unresolvedAccounts" :key="a.name" class="py-2.5">
              <div class="flex items-center gap-3">
                <div class="min-w-0 flex-1">
                  <p class="truncate">{{ a.name }}</p>
                  <p class="text-xs text-mf-muted">{{ a.count }} row{{ a.count === 1 ? '' : 's' }}</p>
                </div>
                <select
                  class="w-40 rounded-lg border border-mf-muted/60 bg-mf-bg px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                  :value="accountMap[a.name] ?? ''"
                  @change="
                    ($event.target as HTMLSelectElement).value === '__new__'
                      ? startNewAccount(a.name)
                      : (accountMap = { ...accountMap, [a.name]: ($event.target as HTMLSelectElement).value })
                  "
                >
                  <option value="" disabled>Choose…</option>
                  <option v-for="opt in taxonomy.activeAccounts" :key="opt.id" :value="opt.id">
                    {{ opt.name }}
                  </option>
                  <option value="__new__">+ Create "{{ a.name }}"</option>
                </select>
              </div>

              <form
                v-if="creatingAccountFor === a.name"
                class="mt-2 flex items-center gap-2 rounded-lg bg-mf-bg p-2"
                @submit.prevent="createMappedAccount"
              >
                <input
                  v-model="newAccountName"
                  type="text"
                  placeholder="Account name"
                  required
                  class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                />
                <select
                  v-model="newAccountCurrency"
                  class="rounded-lg border border-mf-muted/60 bg-mf-surface px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                >
                  <option v-for="code in currencyOptions" :key="code" :value="code">{{ code }}</option>
                </select>
                <button type="submit" class="rounded-full bg-mf-green px-3 py-1.5 text-sm text-white">
                  Add
                </button>
              </form>
            </li>
          </ul>
        </section>

        <div class="flex gap-3">
          <button
            type="button"
            class="flex-1 rounded-full border border-mf-muted py-2.5"
            @click="startOver"
          >
            Cancel
          </button>
          <button
            type="button"
            :disabled="busy || !readyToImport"
            class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white disabled:opacity-50"
            @click="commit"
          >
            {{ busy ? 'Importing…' : `Import ${preview.totalRows} rows` }}
          </button>
        </div>
        <p v-if="!readyToImport" class="text-center text-xs text-mf-muted">
          Map every category above to continue, or leave an account unmapped to skip just its rows.
        </p>
      </template>

      <!-- Step 3: done -->
      <section v-else-if="step === 'done' && result" class="rounded-2xl bg-mf-surface p-4 text-sm">
        <h2 class="mb-2 font-medium">Import complete</h2>
        <p>{{ result.imported }} transaction{{ result.imported === 1 ? '' : 's' }} added.</p>
        <p v-if="result.alreadyImported" class="text-mf-muted">
          {{ result.alreadyImported }} already in your ledger, skipped.
        </p>
        <p v-if="result.parseErrors.length" class="text-mf-red-text">
          {{ result.parseErrors.length }} row{{ result.parseErrors.length === 1 ? '' : 's' }} could not
          be read: line {{ result.parseErrors.map((e) => e.line).join(', ') }}.
        </p>
        <p v-if="result.unresolved.length" class="text-mf-red-text">
          {{ result.unresolved.length }} row{{ result.unresolved.length === 1 ? '' : 's' }} skipped for
          an unmapped category or account.
        </p>
        <button
          type="button"
          class="mt-4 w-full rounded-full bg-mf-green py-2.5 font-medium text-white"
          @click="startOver"
        >
          Import another file
        </button>
      </section>
    </main>

    <Transition name="mf-sheet">
      <NewCategorySheet
        v-if="mappingCategoryFor"
        :kind="mappingCategoryFor.kind === 'income' ? 'income' : 'expense'"
        @cancel="mappingCategoryFor = null"
        @create="createMappedCategory"
      />
    </Transition>
  </div>
</template>
