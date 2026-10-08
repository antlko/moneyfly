<script setup lang="ts">
import { FileUp } from '@lucide/vue'
import { computed, ref } from 'vue'

import * as http from '@/api/http'
import type { ImportNameStatus, ImportPreview, ImportResult } from '@/api/http'
import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import NewCategorySheet from '@/components/monefy/NewCategorySheet.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { useNotifyStore } from '@/stores/notify'
import {
  isSettled,
  planImport,
  plannedAccounts,
  plannedCategories,
  undeclaredCurrencies,
} from '@/lib/importPlan'
import { useAccountsStore } from '@/stores/accounts'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import { today } from '@/lib/period'
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
const fx = useFxStore()
const notify = useNotifyStore()

type Step = 'pick' | 'preview' | 'done'
const step = ref<Step>('pick')
const busy = ref(false)
const error = ref('')
/** Non-blocking note about exchange rates; shown next to the currency section. */
const rateNote = ref('')

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
    notify.fromError(e, 'Could not read that file')
  } finally {
    busy.value = false
    if (fileInput.value) fileInput.value.value = ''
  }
}

const unresolvedCategories = computed(() => preview.value?.categories.filter((c) => !c.resolved) ?? [])
const unresolvedAccounts = computed(() => preview.value?.accounts.filter((a) => !a.resolved) ?? [])
const readyToImport = computed(
  () =>
    preview.value !== null &&
    !unresolvedCategories.value.some((c) => !isSettled(c, categoryMap.value)),
)

/**
 * Exactly what this import will write and what it will skip.
 *
 * The button used to read "Import {totalRows} rows" regardless — so an unmapped
 * account silently dropped every one of its rows while the screen promised the
 * whole file. Leaving an account unmapped is still allowed (it is a deliberate
 * escape hatch), but it is no longer invisible.
 */
const plan = computed(() =>
  preview.value
    ? planImport(preview.value, categoryMap.value, accountMap.value)
    : { importable: 0, skipped: 0 },
)

// --- currencies ------------------------------------------------------------------

/**
 * Currencies in the file this replica cannot convert yet.
 *
 * Rows in one of these import perfectly well and then sit outside every total
 * captioned "no exchange rate yet", with nothing to connect that to the file
 * just imported. Cross-referenced entirely from the local replica — the user's
 * declared list and the rate cache — so this costs no request.
 */
const missingCurrencies = computed(() =>
  preview.value
    ? undeclaredCurrencies(
        preview.value,
        dashboard.baseCurrency,
        settings.get<string[]>(SETTING.currencies, []),
        // Checked as of today: a rate lookup takes the nearest *earlier* date,
        // so a currency convertible today is one this replica has rates for at
        // all. `addQuote` then pulls the history the older rows need.
        (code) => fx.canConvert(code, dashboard.baseCurrency, today()),
      )
    : [],
)

/**
 * Declare each missing currency and fetch a rate for it.
 *
 * Concurrently, not one after another: `fx.addQuote` retries a few times with a
 * pause between, so three currencies in series is the better part of a minute
 * with the screen doing nothing visible. A currency with no rate yet is a
 * warning, never a blocker — the transactions are correct either way, they just
 * do not appear in a converted total until a rate arrives.
 */
async function declareCurrencies() {
  const codes = missingCurrencies.value
  if (!codes.length) return
  busy.value = true
  try {
    const declared = settings.get<string[]>(SETTING.currencies, [])
    const merged = [...new Set([...declared, ...codes])].sort()
    await settings.set(SETTING.currencies, merged)

    const results = await Promise.allSettled(
      codes.map((code) => fx.addQuote(code, dashboard.baseCurrency)),
    )
    const missing = codes.filter((_, i) => {
      const r = results[i]
      return r.status === 'rejected' || r.value === false
    })
    if (missing.length) {
      rateNote.value = `No rate for ${missing.join(', ')} yet — it will arrive with the next update`
    }
  } finally {
    busy.value = false
  }
}

// --- bulk create -----------------------------------------------------------------

/**
 * Everything "Create all missing" would create, listed before anything is
 * written.
 *
 * The importer's rule is that a name is never coerced onto something else and
 * never invented behind the operator's back — that is what lost 14% of a real
 * export once. A previewed list, written only on an explicit press, keeps that
 * rule while not turning a twenty-category file into twenty separate chores.
 */
const showBulkCreate = ref(false)
const bulkCategories = computed(() =>
  plannedCategories(unresolvedCategories.value.filter((c) => !categoryMap.value[c.key])),
)
const bulkAccounts = computed(() =>
  plannedAccounts(
    unresolvedAccounts.value.filter((a) => !accountMap.value[a.key]),
    dashboard.baseCurrency,
  ),
)

async function createAllMissing() {
  busy.value = true
  error.value = ''
  try {
    const nextCategories = { ...categoryMap.value }
    for (const c of bulkCategories.value) {
      nextCategories[c.key] = await sync.write('category', {
        name: c.name,
        icon: c.icon,
        color: c.color,
        kind: c.kind,
        sortOrder: 999,
        archived: 0,
      })
    }
    categoryMap.value = nextCategories

    const nextAccounts = { ...accountMap.value }
    for (const a of bulkAccounts.value) {
      nextAccounts[a.key] = await accounts.create({
        name: a.name,
        currency: a.currency,
        icon: 'Wallet',
        color: 'green',
        initialBalanceMinor: 0,
      })
    }
    accountMap.value = nextAccounts

    showBulkCreate.value = false
    await declareCurrencies()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not create everything'
    notify.fromError(e, 'Could not create everything')
  } finally {
    busy.value = false
  }
}

// --- mapping: categories, by reusing the record screen's own sheet ---------------

const mappingCategoryFor = ref<ImportNameStatus | null>(null)

async function createMappedCategory(input: {
  name: string
  icon: string
  color: string
  kind: 'expense' | 'income'
}) {
  const key = mappingCategoryFor.value?.key
  mappingCategoryFor.value = null
  if (!key) return
  const id = await sync.write('category', { ...input, sortOrder: 999, archived: 0 })
  categoryMap.value = { ...categoryMap.value, [key]: id }
}

// --- mapping: accounts, a lighter inline form — an account is just a name and
// a currency, not worth a whole second sheet component for ---------------------

const creatingAccountFor = ref<string | null>(null)
const newAccountName = ref('')
const newAccountCurrency = ref('')

function startNewAccount(account: ImportNameStatus) {
  creatingAccountFor.value = account.key
  newAccountName.value = account.name
  // Prefer the currency actually observed in the CSV for this account. The
  // reference export also names accounts after their currency ("EUR", "HUF"),
  // which is the fallback for a status the server did not send one on.
  newAccountCurrency.value =
    account.currency ||
    (/^[A-Za-z]{3}$/.test(account.name) ? account.name.toUpperCase() : dashboard.baseCurrency)
}

async function createMappedAccount() {
  const key = creatingAccountFor.value
  const name = newAccountName.value.trim()
  const currency = newAccountCurrency.value.trim().toUpperCase()
  if (!key || !name || currency.length !== 3) return

  // Creating the account is what tells this screen the currency is wanted —
  // declare it the same way the Currencies screen's own "add" does, so a
  // HUF export does not require a separate trip there first.
  const declared = settings.get<string[]>(SETTING.currencies, [])
  if (currency !== dashboard.baseCurrency && !declared.includes(currency)) {
    await settings.set(SETTING.currencies, [...declared, currency].sort())
    if (!(await fx.addQuote(currency, dashboard.baseCurrency))) {
      rateNote.value = `No rate for ${currency} yet — it will arrive with the next update`
    }
  }

  const id = await accounts.create({
    name,
    currency,
    icon: 'Wallet',
    color: 'green',
    initialBalanceMinor: 0,
  })
  accountMap.value = { ...accountMap.value, [key]: id }
  creatingAccountFor.value = null
}

const currencyOptions = computed(() => {
  const codes = new Set<string>([dashboard.baseCurrency, ...settings.get<string[]>(SETTING.currencies, [])])
  // The account being created right now must be selectable even when its
  // currency has never been declared — otherwise an operator importing a
  // HUF export with no HUF account yet could never actually pick HUF here.
  if (creatingAccountFor.value) codes.add(newAccountCurrency.value)
  return [...codes].filter((c) => c.length === 3).sort()
})

// --- commit ------------------------------------------------------------------------

async function commit() {
  busy.value = true
  error.value = ''
  try {
    /*
     * Push the outbox before handing the server ids that are still only in it.
     *
     * A category or account created on this screen gets a client-minted id that
     * sits in the local outbox until the engine's next push. The commit sends
     * that id, so without this the server writes thousands of transactions
     * pointing at a category row it has never seen — self-correcting on the
     * next push, but if the tab closes in between, those rows reference
     * something that exists nowhere. Bulk-create turns that from one dangling
     * row into twenty.
     *
     * Legitimate to await here despite "the UI never waits on the network":
     * this is an explicit, user-initiated action that is about to make a
     * request anyway, not an ordinary write.
     */
    await sync.sync()
    result.value = await http.importMonefyCommit(
      csvText.value,
      categoryMap.value,
      accountMap.value,
      fileName.value,
    )
    step.value = 'done'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Import failed'
    notify.fromError(e, 'Import failed')
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

        <!--
          One press instead of twenty. Creation is still explicit and previewed:
          the list below says exactly what will be made before anything is.
        -->
        <section
          v-if="bulkCategories.length || bulkAccounts.length"
          class="rounded-2xl bg-mf-surface p-4 text-sm"
        >
          <template v-if="!showBulkCreate">
            <p class="mb-3 text-mf-muted">
              {{ bulkCategories.length }} categor{{ bulkCategories.length === 1 ? 'y' : 'ies' }}
              and {{ bulkAccounts.length }} account{{ bulkAccounts.length === 1 ? '' : 's' }}
              in this file do not exist yet.
            </p>
            <button
              type="button"
              :disabled="busy"
              class="w-full rounded-full border border-mf-green py-2.5 font-medium text-mf-green-dark disabled:opacity-50"
              @click="showBulkCreate = true"
            >
              Create all missing…
            </button>
          </template>

          <template v-else>
            <h2 class="mb-1 font-medium">This will create</h2>
            <p class="mb-3 text-mf-muted">
              Nothing is written until you confirm. You can still map any of these by hand instead.
            </p>
            <ul class="mb-3 max-h-64 divide-y divide-mf-muted/20 overflow-y-auto">
              <li v-for="c in bulkCategories" :key="c.key" class="flex items-center gap-2 py-2">
                <CategoryIcon :icon="c.icon" :color="c.color" :size="22" />
                <span class="min-w-0 flex-1 truncate">{{ c.name }}</span>
                <span class="text-xs text-mf-muted">{{ c.kind }}</span>
              </li>
              <li v-for="a in bulkAccounts" :key="a.key" class="flex items-center gap-2 py-2">
                <CategoryIcon icon="Wallet" color="green" :size="22" />
                <span class="min-w-0 flex-1 truncate">{{ a.name }}</span>
                <span class="text-xs text-mf-muted">account · {{ a.currency }}</span>
              </li>
            </ul>
            <div class="flex gap-3">
              <button
                type="button"
                class="flex-1 rounded-full border border-mf-muted py-2.5"
                @click="showBulkCreate = false"
              >
                Cancel
              </button>
              <button
                type="button"
                :disabled="busy"
                class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white disabled:opacity-50"
                @click="createAllMissing"
              >
                {{ busy ? 'Creating…' : 'Create' }}
              </button>
            </div>
          </template>
        </section>

        <!--
          Rows in a currency with no rate import fine and then sit outside every
          total, captioned "no exchange rate yet", with nothing to connect that
          to the file just imported. A warning, never a blocker — the
          transactions are correct either way.
        -->
        <section v-if="missingCurrencies.length" class="rounded-2xl bg-mf-surface p-4 text-sm">
          <h2 class="mb-1 font-medium">Currencies in this file</h2>
          <p class="mb-3 text-mf-muted">
            {{ missingCurrencies.join(', ') }}
            {{ missingCurrencies.length === 1 ? 'has' : 'have' }} no exchange rate here yet. These
            rows will import either way, but stay out of converted totals until a rate arrives.
          </p>
          <button
            type="button"
            :disabled="busy"
            class="w-full rounded-full border border-mf-green py-2.5 font-medium text-mf-green-dark disabled:opacity-50"
            @click="declareCurrencies"
          >
            {{ busy ? 'Fetching…' : `Add ${missingCurrencies.join(', ')} and fetch rates` }}
          </button>
          <p v-if="rateNote" class="mt-2 text-xs text-mf-muted">{{ rateNote }}</p>
        </section>

        <section v-if="unresolvedCategories.length" class="rounded-2xl bg-mf-surface p-4">
          <h2 class="mb-1 font-medium">Categories to map</h2>
          <p class="mb-3 text-sm text-mf-muted">
            These names are not in your categories. Map each to an existing one or create a new one —
            a row left unmapped is skipped, not guessed at.
          </p>
          <ul class="divide-y divide-mf-muted/20">
            <li v-for="c in unresolvedCategories" :key="c.key" class="flex items-center gap-3 py-2.5">
              <div class="min-w-0 flex-1">
                <p class="truncate">{{ c.name }}</p>
                <p class="text-xs text-mf-muted">
                  {{ c.count }} row{{ c.count === 1 ? '' : 's' }} · {{ c.kind }}
                </p>
              </div>
              <select
                class="w-40 rounded-lg border border-mf-muted/60 bg-mf-bg px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                :value="categoryMap[c.key] ?? ''"
                @change="
                  ($event.target as HTMLSelectElement).value === '__new__'
                    ? (mappingCategoryFor = c)
                    : (categoryMap = { ...categoryMap, [c.key]: ($event.target as HTMLSelectElement).value })
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
          <p class="mb-3 text-sm text-mf-muted">
            Map each to an existing account in the same currency, or create a new one — a row left
            unmapped is skipped, not guessed at.
          </p>
          <ul class="divide-y divide-mf-muted/20">
            <li v-for="a in unresolvedAccounts" :key="a.key" class="py-2.5">
              <div class="flex items-center gap-3">
                <div class="min-w-0 flex-1">
                  <p class="truncate">{{ a.name }}</p>
                  <p class="text-xs text-mf-muted">
                    {{ a.count }} row{{ a.count === 1 ? '' : 's' }} · {{ a.currency }}
                  </p>
                  <p v-if="a.currencyMismatch" class="text-xs text-mf-red-text">
                    An account named "{{ a.name }}" already exists, in a different currency.
                  </p>
                </div>
                <select
                  class="w-40 rounded-lg border border-mf-muted/60 bg-mf-bg px-2 py-1.5 text-sm outline-none focus:border-mf-green"
                  :value="accountMap[a.key] ?? ''"
                  @change="
                    ($event.target as HTMLSelectElement).value === '__new__'
                      ? startNewAccount(a)
                      : (accountMap = { ...accountMap, [a.key]: ($event.target as HTMLSelectElement).value })
                  "
                >
                  <option value="" disabled>Choose…</option>
                  <option v-for="opt in taxonomy.activeAccounts" :key="opt.id" :value="opt.id">
                    {{ opt.name }} ({{ opt.currency }})
                  </option>
                  <option value="__new__">+ Create "{{ a.name }}"</option>
                </select>
              </div>

              <form
                v-if="creatingAccountFor === a.key"
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
            {{ busy ? 'Importing…' : `Import ${plan.importable} rows` }}
          </button>
        </div>
        <p v-if="plan.skipped" class="text-center text-xs text-mf-red-text">
          {{ plan.skipped }} of {{ preview.totalRows }} row{{ preview.totalRows === 1 ? '' : 's' }}
          will be skipped — their category or account is not mapped.
        </p>
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
        <p v-if="result.failed" class="text-mf-red-text">
          {{ result.failed }} row{{ result.failed === 1 ? '' : 's' }} could not be written. Import the
          same file again — the {{ result.imported }} already added will be skipped.
          <span v-if="result.failureReason" class="block text-mf-muted">
            {{ result.failureReason }}
          </span>
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
