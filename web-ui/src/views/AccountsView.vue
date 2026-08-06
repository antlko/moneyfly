<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { computed, ref } from 'vue'

import { useRouter } from 'vue-router'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { CATEGORY_COLORS, CATEGORY_ICONS, type CategoryColor } from '@/lib/categories'
import { currencyName } from '@/lib/currencies'
import { exponent, toMajor, toMinor } from '@/lib/money'
import { useAccountsStore } from '@/stores/accounts'
import { useDashboardStore } from '@/stores/dashboard'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

const accounts = useAccountsStore()
const taxonomy = useTaxonomyStore()
const dashboard = useDashboardStore()
const settings = useSettingsStore()
const router = useRouter()

const editing = ref<Row | null>(null)
const creating = ref(false)

/**
 * The currencies on offer, declared on the Currencies screen.
 *
 * A free-text field here was wrong in both directions: it accepted "eu" or
 * "dollars" as a currency, and it was the only way to introduce one — so the
 * rate machinery could not know a currency existed until an account already
 * used it. Declaring currencies in one place and choosing from that list here
 * settles both.
 */
const currencyOptions = computed(() => {
  const codes = new Set<string>([
    dashboard.baseCurrency,
    ...settings.get<string[]>(SETTING.currencies, []),
  ])
  // Whatever the account being edited already uses stays selectable even if it
  // has since been turned off, or saving the form would silently re-denominate
  // existing money.
  if (editing.value) codes.add(String(editing.value.currency ?? ''))
  return [...codes].filter((c) => c.length === 3).sort()
})

const form = ref({
  name: '',
  currency: dashboard.baseCurrency,
  icon: 'Wallet',
  color: 'green' as CategoryColor,
  opening: '0',
})

const iconNames = Object.keys(CATEGORY_ICONS)
const archived = computed(() => taxonomy.accounts.filter((a) => a.archived))

function startCreate() {
  editing.value = null
  creating.value = true
  form.value = {
    name: '',
    currency: dashboard.baseCurrency,
    icon: 'Wallet',
    color: 'green',
    opening: '0',
  }
}

function startEdit(account: Row) {
  creating.value = false
  editing.value = account
  form.value = {
    name: String(account.name ?? ''),
    currency: String(account.currency ?? dashboard.baseCurrency),
    icon: String(account.icon ?? 'Wallet'),
    color: (account.color as CategoryColor) ?? 'green',
    opening: String(toMajor(Number(account.initialBalanceMinor ?? 0), String(account.currency))),
  }
}

async function save() {
  const name = form.value.name.trim()
  if (!name) return
  const currency = form.value.currency.trim().toUpperCase()
  const input = {
    name,
    currency,
    icon: form.value.icon,
    color: form.value.color,
    initialBalanceMinor: toMinor(Number(form.value.opening) || 0, currency),
  }

  if (editing.value) await accounts.update(String(editing.value.id), input)
  else await accounts.create(input)

  editing.value = null
  creating.value = false
}

const open = computed(() => creating.value || editing.value !== null)

function closeForm() {
  creating.value = false
  editing.value = null
}
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Accounts">
      <template #actions>
        <button type="button" aria-label="New account" class="p-2" @click="startCreate">
          <Plus :size="22" :stroke-width="2" />
        </button>
      </template>
    </ScreenHeader>

    <main class="flex-1 overflow-y-auto pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-2xl">
      <ul class="divide-y divide-mf-muted/25">
        <li
          v-for="account in taxonomy.activeAccounts"
          :key="account.id"
          class="flex items-center gap-3 px-4 py-3"
        >
          <CategoryIcon :icon="account.icon" :color="account.color" :size="28" />
          <div class="min-w-0 flex-1">
            <p class="truncate">{{ account.name }}</p>
            <p class="text-xs text-mf-muted">{{ account.currency }}</p>
          </div>
          <MoneyAmount
            :minor="accounts.balanceOf(account.id)"
            :currency="String(account.currency)"
            class="font-medium"
            :class="accounts.balanceOf(account.id) < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
          />
          <button type="button" class="text-xs text-mf-green-dark" @click="startEdit(account)">
            Edit
          </button>
          <button
            type="button"
            class="text-xs text-mf-red-text"
            @click="accounts.archive(String(account.id))"
          >
            Archive
          </button>
        </li>
      </ul>

      <template v-if="archived.length">
        <p class="bg-mf-green-soft/25 px-4 py-1 text-xs text-mf-ink/70">Archived</p>
        <ul class="divide-y divide-mf-muted/25">
          <li
            v-for="account in archived"
            :key="account.id"
            class="flex items-center gap-3 px-4 py-3 opacity-60"
          >
            <CategoryIcon :icon="account.icon" :color="account.color" :size="28" dim />
            <span class="min-w-0 flex-1 truncate">{{ account.name }}</span>
            <button
              type="button"
              class="text-xs text-mf-green-dark"
              @click="accounts.restore(String(account.id))"
            >
              Restore
            </button>
          </li>
        </ul>
      </template>
    </main>

    <Transition name="mf-sheet">
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-end mf-scrim"
        @click.self="closeForm"
      >
        <form
          class="max-h-[85%] w-full space-y-4 overflow-y-auto rounded-t-2xl bg-mf-bg p-4 pb-[calc(1rem+var(--spacing-safe-b))]"
          @submit.prevent="save"
        >
          <h2 class="text-lg font-medium">{{ editing ? 'Edit account' : 'New account' }}</h2>

          <input
            v-model="form.name"
            type="text"
            placeholder="Name"
            required
            maxlength="40"
            class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
          />

          <div class="flex gap-3">
            <label class="flex-1 text-sm">
              <span class="mb-1 block text-mf-muted">Currency</span>
              <select
                v-model="form.currency"
                required
                class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
              >
                <option v-for="code in currencyOptions" :key="code" :value="code">
                  {{ code }} · {{ currencyName(code) }}
                </option>
              </select>
              <button
                type="button"
                class="mt-1 text-xs text-mf-green-dark"
                @click="router.push('/currencies')"
              >
                Add a currency…
              </button>
            </label>
            <label class="flex-1 text-sm">
              <!--
              An opening balance, not a running one. Everything after it is
              derived from the transactions — see docs/SYNC.md §2.
            -->
              <span class="mb-1 block text-mf-muted">Opening balance</span>
              <input
                v-model="form.opening"
                type="number"
                :step="exponent(form.currency) === 0 ? '1' : '0.01'"
                class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
              />
            </label>
          </div>

          <div>
            <p class="mb-2 text-sm text-mf-muted">Colour</p>
            <div class="flex flex-wrap gap-2">
              <button
                v-for="option in CATEGORY_COLORS"
                :key="option"
                type="button"
                class="h-8 w-8 rounded-full border-2"
                :style="{
                  backgroundColor: `var(--color-cat-${option})`,
                  borderColor: form.color === option ? 'var(--color-mf-ink)' : 'transparent',
                }"
                :aria-label="option"
                @click="form.color = option"
              />
            </div>
          </div>

          <div>
            <p class="mb-2 text-sm text-mf-muted">Icon</p>
            <div class="grid grid-cols-8 gap-2">
              <button
                v-for="option in iconNames"
                :key="option"
                type="button"
                class="grid aspect-square place-items-center rounded-lg border"
                :class="
                  form.icon === option
                    ? 'border-mf-green bg-mf-green-soft/30'
                    : 'border-transparent'
                "
                :aria-label="option"
                @click="form.icon = option"
              >
                <CategoryIcon :icon="option" :color="form.color" :size="22" />
              </button>
            </div>
          </div>

          <div class="flex gap-3 pt-1">
            <button
              type="button"
              class="flex-1 rounded-full border border-mf-muted py-2.5"
              @click="closeForm"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white"
            >
              Save
            </button>
          </div>
        </form>
      </div>
    </Transition>
  </div>
</template>
