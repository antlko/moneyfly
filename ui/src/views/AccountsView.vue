<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { formatMoney } from '@/lib/money'
import { pushToast } from '@/lib/toast'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Account, AccountInput, AssetClass } from '@/api/types'

const taxonomy = useTaxonomyStore()

const classes: AssetClass[] = ['cash', 'bank', 'deposit', 'investment', 'metal', 'crypto', 'other']

const editing = ref<Account | null>(null)
const creating = ref(false)
const error = ref('')
const draft = ref<AccountInput>({
  name: '',
  asset_class: 'cash',
  currency: 'EUR',
  is_liquid: true,
  counts_toward_net_worth: true,
})

/** Parents first, then their children, so the rollup structure is visible. */
const tree = computed(() => {
  const roots = taxonomy.accounts.filter((a) => a.parent_id === null)
  const out: { account: Account; depth: number }[] = []
  for (const root of roots) {
    out.push({ account: root, depth: 0 })
    for (const child of taxonomy.accounts.filter((a) => a.parent_id === root.id)) {
      out.push({ account: child, depth: 1 })
    }
  }
  return out
})

function startCreate() {
  creating.value = true
  editing.value = null
  draft.value = {
    name: '',
    asset_class: 'cash',
    currency: 'EUR',
    is_liquid: true,
    counts_toward_net_worth: true,
  }
}

function startEdit(a: Account) {
  editing.value = a
  creating.value = false
  draft.value = {
    name: a.name,
    asset_class: a.asset_class,
    currency: a.currency,
    is_liquid: a.is_liquid,
    counts_toward_net_worth: a.counts_toward_net_worth,
    price_ticker: a.price_ticker ?? undefined,
    cost_basis_minor: a.cost_basis?.amount_minor ?? null,
    parent_id: a.parent_id,
  }
}

function cancel() {
  creating.value = false
  editing.value = null
  error.value = ''
}

async function save() {
  error.value = ''
  try {
    if (editing.value) {
      await taxonomy.updateAccount(editing.value.id, draft.value)
      pushToast(`${draft.value.name} updated.`, 'success')
    } else {
      await taxonomy.createAccount(draft.value)
      pushToast(`${draft.value.name} created.`, 'success')
    }
    cancel()
  } catch (err) {
    error.value =
      err instanceof ApiError
        ? Object.values(err.fieldErrors)[0] ||
          (err.status === 409 ? 'An account with that name already exists.' : err.message)
        : 'Could not reach the server.'
  }
}

async function archive(a: Account) {
  await taxonomy.archiveAccount(a.id)
  pushToast(`${a.name} archived.`, 'info')
}

onMounted(() => taxonomy.load(true))
</script>

<template>
  <section>
    <header class="mb-4 flex items-center gap-2">
      <h1 class="flex-1 text-lg font-semibold">Accounts</h1>
      <button
        type="button"
        class="tap-target rounded-xl bg-brand-600 px-3 py-2 text-sm font-semibold text-white"
        @click="startCreate"
      >
        New
      </button>
    </header>

    <p class="mb-4 text-xs text-slate-500">
      <strong>Liquid</strong> drives Ready for usage; <strong>counts toward net worth</strong>
      drives General. A parent account is computed from its children and cannot hold a value
      directly.
    </p>

    <form
      v-if="creating || editing"
      class="mb-4 space-y-3 rounded-xl border border-brand-300 bg-brand-50 p-3 dark:border-brand-700 dark:bg-slate-900"
      @submit.prevent="save"
    >
      <div class="grid grid-cols-2 gap-2">
        <div class="col-span-2">
          <label for="a-name" class="mb-1 block text-sm font-medium">Name</label>
          <input
            id="a-name"
            v-model="draft.name"
            required
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
        <div>
          <label for="a-class" class="mb-1 block text-sm font-medium">Asset class</label>
          <select
            id="a-class"
            v-model="draft.asset_class"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 capitalize dark:border-slate-700 dark:bg-slate-900"
          >
            <option v-for="c in classes" :key="c" :value="c">{{ c }}</option>
          </select>
        </div>
        <div>
          <label for="a-currency" class="mb-1 block text-sm font-medium">Currency</label>
          <select
            id="a-currency"
            v-model="draft.currency"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option v-for="c in taxonomy.currencies" :key="c.code" :value="c.code">
              {{ c.code }} — {{ c.name }}
            </option>
          </select>
        </div>
        <div>
          <label for="a-ticker" class="mb-1 block text-sm font-medium">
            Price ticker <span class="text-slate-400">(optional)</span>
          </label>
          <input
            id="a-ticker"
            v-model="draft.price_ticker"
            placeholder="XAU, USDT"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
        <div>
          <label for="a-parent" class="mb-1 block text-sm font-medium">
            Parent <span class="text-slate-400">(optional)</span>
          </label>
          <select
            id="a-parent"
            v-model="draft.parent_id"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option :value="null">None</option>
            <option
              v-for="a in taxonomy.accounts.filter((x) => x.id !== editing?.id)"
              :key="a.id"
              :value="a.id"
            >
              {{ a.name }}
            </option>
          </select>
        </div>
      </div>

      <div class="flex flex-wrap gap-4">
        <label class="tap-target flex items-center gap-2 text-sm">
          <input v-model="draft.is_liquid" type="checkbox" class="h-5 w-5" />
          Liquid (ready for usage)
        </label>
        <label class="tap-target flex items-center gap-2 text-sm">
          <input v-model="draft.counts_toward_net_worth" type="checkbox" class="h-5 w-5" />
          Counts toward net worth
        </label>
      </div>

      <p v-if="error" class="text-sm text-state-severe" role="alert">{{ error }}</p>

      <div class="flex gap-2">
        <button
          type="submit"
          class="tap-target rounded-xl bg-brand-600 px-4 py-2 font-semibold text-white"
        >
          Save
        </button>
        <button
          type="button"
          class="tap-target rounded-xl px-4 py-2 text-slate-500"
          @click="cancel"
        >
          Cancel
        </button>
      </div>
    </form>

    <ul class="space-y-1">
      <li
        v-for="{ account, depth } in tree"
        :key="account.id"
        class="flex items-center gap-2 rounded-xl border border-slate-200 bg-white p-2 text-sm dark:border-slate-800 dark:bg-slate-900"
        :class="depth > 0 ? 'ml-4' : ''"
      >
        <span class="flex-1 truncate">
          <span class="font-medium">{{ account.name }}</span>
          <span class="ml-1 text-xs text-slate-400">
            {{ account.asset_class }} · {{ account.currency }}
          </span>
          <span v-if="account.is_computed" class="ml-1 text-xs text-brand-600 dark:text-brand-300">
            computed from children
          </span>
        </span>
        <span
          v-if="account.is_liquid"
          class="rounded-full bg-state-within/15 px-2 text-xs text-state-within"
        >
          liquid
        </span>
        <span
          v-if="!account.counts_toward_net_worth"
          class="rounded-full bg-state-zero/15 px-2 text-xs text-state-zero"
        >
          off net worth
        </span>
        <span v-if="account.price_ticker" class="text-xs text-slate-400">
          {{ account.price_ticker }}
        </span>
        <span v-if="account.cost_basis" class="money text-xs text-slate-400">
          basis {{ formatMoney(account.cost_basis) }}
        </span>
        <button
          type="button"
          class="tap-target px-2 text-xs text-brand-600 underline"
          @click="startEdit(account)"
        >
          edit
        </button>
        <button
          type="button"
          class="tap-target px-2 text-xs text-state-severe underline"
          @click="archive(account)"
        >
          archive
        </button>
      </li>
    </ul>
  </section>
</template>
