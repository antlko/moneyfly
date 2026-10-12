<script setup lang="ts">
import { Archive, Pencil, Plus } from '@lucide/vue'
import { computed, nextTick, ref, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import ModalSheet from '@/components/monefy/ModalSheet.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import ScreenHeader from '@/components/monefy/ScreenHeader.vue'
import { CATEGORY_COLORS, CATEGORY_ICONS, type CategoryColor } from '@/lib/categories'
import { currencyName } from '@/lib/currencies'
import { focusUnlessTouch } from '@/lib/keyboard'
import { exponent, toMajor, toMinor } from '@/lib/money'
import { today } from '@/lib/period'
import {
  isLiquid,
  isSpendable,
  SUGGESTED_GROUPS,
  UNGROUPED,
  useAccountsStore,
} from '@/stores/accounts'
import { useCapitalStore } from '@/stores/capital'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

/**
 * Where the money is.
 *
 * Accounts are grouped the way people group them in their own spreadsheets —
 * cash, banks, deposits, gold — with a subtotal per group and two headline
 * figures: everything, and what is **ready to use**. That second number is the
 * one people actually plan with; a term deposit counts towards what you are
 * worth, not towards what you can spend this month.
 *
 * Balances are still derived (opening balance + records — docs/SYNC.md §2).
 * "Set balance" moves the opening balance so the derived figure comes out
 * right, and records the figure as this month's capital check-in, so the
 * Capital screen and this one never disagree about what you said you had.
 */
const accounts = useAccountsStore()
const capital = useCapitalStore()
const taxonomy = useTaxonomyStore()
const dashboard = useDashboardStore()
const settings = useSettingsStore()
const fx = useFxStore()
const router = useRouter()

const editing = ref<Row | null>(null)
const creating = ref(false)
const settingBalance = ref<Row | null>(null)
const balanceInput = ref('')

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

const groupSuggestions = computed(() => [
  ...new Set([...accounts.groupNames, ...SUGGESTED_GROUPS]),
])

const form = ref({
  name: '',
  group: '',
  liquid: true,
  spendable: true,
  currency: dashboard.baseCurrency,
  icon: 'Wallet',
  color: 'green' as CategoryColor,
  /** What the account holds now, as typed. */
  balance: '0',
})

const iconNames = Object.keys(CATEGORY_ICONS)
const archived = computed(() => taxonomy.accounts.filter((a) => a.archived))

/** Only show group headings once there is more than one group to tell apart. */
const showGroups = computed(() => accounts.groups.length > 1)

/** An account's balance in the base currency, or null when it already is (or cannot be priced). */
function inBase(account: Row): number | null {
  const currency = String(account.currency ?? '')
  if (currency === dashboard.baseCurrency) return null
  return fx.convert(accounts.balanceOf(account.id), currency, dashboard.baseCurrency, today())
}

function startCreate() {
  editing.value = null
  creating.value = true
  form.value = {
    name: '',
    group: '',
    liquid: true,
    spendable: true,
    currency: dashboard.baseCurrency,
    icon: 'Wallet',
    color: 'green',
    balance: '0',
  }
}

function startEdit(account: Row) {
  creating.value = false
  editing.value = account
  form.value = {
    name: String(account.name ?? ''),
    group: String(account.group ?? ''),
    liquid: isLiquid(account),
    spendable: isSpendable(account),
    currency: String(account.currency ?? dashboard.baseCurrency),
    icon: String(account.icon ?? 'Wallet'),
    color: (account.color as CategoryColor) ?? 'green',
    balance: String(toMajor(accounts.balanceOf(account.id), String(account.currency))),
  }
}

async function save() {
  const name = form.value.name.trim()
  if (!name) return
  const currency = form.value.currency.trim().toUpperCase()
  const balanceMinor = toMinor(Number(form.value.balance) || 0, currency)
  const details = {
    name,
    currency,
    icon: form.value.icon,
    color: form.value.color,
    group: form.value.group.trim(),
    liquid: form.value.liquid,
    spendable: form.value.spendable,
  }

  const account = editing.value
  if (account) {
    const id = String(account.id)
    const changedBalance = balanceMinor !== accounts.balanceOf(id)
    // One write for the details and the balance together — see openingFor.
    await accounts.update(id, {
      ...details,
      ...(changedBalance && { initialBalanceMinor: accounts.openingFor(id, balanceMinor) }),
    })
    if (changedBalance) await capital.checkInNow(id, balanceMinor, currency)
  } else {
    // A new account has no records, so what it holds now *is* its opening balance.
    const id = await accounts.create({ ...details, initialBalanceMinor: balanceMinor })
    if (balanceMinor !== 0) await capital.checkInNow(id, balanceMinor, currency)
  }

  editing.value = null
  creating.value = false
}

/** Move the live balance and record it as this month's check-in, together. */
async function setBalanceOf(id: string, minor: number, currency: string) {
  await accounts.setBalance(id, minor)
  await capital.checkInNow(id, minor, currency)
}

const open = computed(() => creating.value || editing.value !== null)

/**
 * Archive rather than delete — records keep pointing at the account, and its
 * past check-ins keep their place in the capital table.
 */
async function archiveEditing() {
  const account = editing.value
  if (!account) return
  editing.value = null
  await accounts.archive(String(account.id))
}

function closeForm() {
  creating.value = false
  editing.value = null
}

const balanceField = useTemplateRef<HTMLInputElement>('balanceField')
function startSetBalance(account: Row) {
  settingBalance.value = account
  balanceInput.value = String(toMajor(accounts.balanceOf(account.id), String(account.currency)))
  void nextTick(() => {
    const field = balanceField.value
    focusUnlessTouch(field)
    // Selected, so typing replaces the figure — but only where it was focused.
    if (field && document.activeElement === field) field.select()
  })
}

async function saveBalance() {
  const account = settingBalance.value
  if (!account) return
  const currency = String(account.currency)
  const value = Number(balanceInput.value)
  if (!Number.isFinite(value)) return
  settingBalance.value = null
  await setBalanceOf(String(account.id), toMinor(value, currency), currency)
}

/** How far the opening balance would move — said out loud before it happens. */
const balanceDelta = computed(() => {
  const account = settingBalance.value
  if (!account) return 0
  const value = Number(balanceInput.value)
  if (!Number.isFinite(value)) return 0
  return toMinor(value, String(account.currency)) - accounts.balanceOf(account.id)
})
</script>

<template>
  <div class="flex h-full flex-col bg-mf-bg">
    <ScreenHeader title="Accounts">
      <template #actions>
        <button type="button" aria-label="New account" class="grid size-11 place-items-center" @click="startCreate">
          <Plus :size="22" :stroke-width="2" />
        </button>
      </template>
    </ScreenHeader>

    <main
      class="flex-1 space-y-4 overflow-y-auto p-4 pb-[calc(1rem+var(--spacing-safe-b))] sm:mx-auto sm:w-full sm:max-w-4xl sm:p-0"
    >
      <!-- The two numbers people plan with. -->
      <section class="grid grid-cols-2 gap-3">
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted sm:text-sm">Net worth</p>
          <MoneyAmount
            :minor="accounts.liveTotals.totalMinor"
            :currency="dashboard.baseCurrency"
            class="text-xl font-semibold sm:text-2xl"
            :class="accounts.liveTotals.totalMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
          />
        </div>
        <div class="rounded-2xl bg-mf-surface p-4">
          <p class="text-xs text-mf-muted sm:text-sm">Ready to use</p>
          <MoneyAmount
            :minor="accounts.liveTotals.readyMinor"
            :currency="dashboard.baseCurrency"
            class="text-xl font-semibold sm:text-2xl"
            :class="accounts.liveTotals.readyMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
          />
        </div>
        <p v-if="accounts.liveTotals.unconverted" class="col-span-2 text-xs text-mf-red-text">
          {{ accounts.liveTotals.unconverted }} account(s) left out — no exchange rate for their currency yet.
        </p>
      </section>

      <section
        v-for="group in accounts.groups"
        :key="group.name"
        class="overflow-hidden rounded-2xl bg-mf-surface"
      >
        <header
          v-if="showGroups"
          class="flex items-baseline justify-between gap-3 border-b border-mf-muted/20 px-4 py-2.5"
        >
          <h2 class="font-medium">{{ group.name }}</h2>
          <MoneyAmount
            :minor="accounts.liveTotals.byGroup.get(group.name) ?? 0"
            :currency="dashboard.baseCurrency"
            class="text-sm text-mf-muted"
          />
        </header>

        <ul class="divide-y divide-mf-muted/20">
          <li v-for="account in group.accounts" :key="account.id" class="flex items-center gap-3 px-4 py-3">
            <CategoryIcon :icon="account.icon" :color="account.color" :size="28" />
            <div class="min-w-0 flex-1">
              <p class="truncate">{{ account.name }}</p>
              <p class="truncate text-xs text-mf-muted">
                {{ account.currency }}<template v-if="!isLiquid(account)"> · not ready to use</template
                ><template v-if="!isSpendable(account)"> · no spending</template>
              </p>
            </div>

            <!-- The balance is the button: setting it is the thing people come here to do. -->
            <button
              type="button"
              class="rounded-lg px-2 py-1 text-right hover:bg-mf-bg"
              :aria-label="`Set the balance of ${account.name}`"
              title="Set balance"
              @click="startSetBalance(account)"
            >
              <MoneyAmount
                :minor="accounts.balanceOf(account.id)"
                :currency="String(account.currency)"
                class="block font-medium"
                :class="accounts.balanceOf(account.id) < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
              />
              <MoneyAmount
                v-if="inBase(account) !== null"
                :minor="inBase(account)!"
                :currency="dashboard.baseCurrency"
                class="block text-xs text-mf-muted"
              />
            </button>

            <button
              type="button"
              class="grid size-9 shrink-0 place-items-center rounded-full text-mf-green-dark hover:bg-mf-bg"
              :aria-label="`Edit ${account.name}`"
              title="Edit"
              @click="startEdit(account)"
            >
              <Pencil :size="16" :stroke-width="1.8" />
            </button>
          </li>
        </ul>
      </section>

      <p class="px-1 text-xs text-mf-muted">
        Tap a balance to set what an account holds now. Your records stay as they are — the opening
        balance moves to match, and the figure is saved as this month's
        <RouterLink to="/capital" class="text-mf-green-dark underline">capital check-in</RouterLink>.
      </p>

      <section v-if="archived.length" class="overflow-hidden rounded-2xl bg-mf-surface opacity-80">
        <p class="border-b border-mf-muted/20 px-4 py-2 text-xs text-mf-ink/70">Archived</p>
        <ul class="divide-y divide-mf-muted/20">
          <li v-for="account in archived" :key="account.id" class="flex items-center gap-3 px-4 py-3">
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
      </section>
    </main>

    <!-- Set balance: one number, and what it will do. -->
    <Transition name="mf-sheet">
      <ModalSheet v-if="settingBalance" size="sm" label="Set balance" @close="settingBalance = null">
        <form class="space-y-4 p-4 sm:p-6" @submit.prevent="saveBalance">
          <div>
            <h2 class="text-lg font-medium">What is in {{ settingBalance.name }} now?</h2>
            <p class="text-sm text-mf-muted">{{ currencyName(String(settingBalance.currency)) }}</p>
          </div>
          <label class="flex items-center gap-3">
            <input
              ref="balanceField"
              v-model="balanceInput"
              type="number"
              :step="exponent(String(settingBalance.currency)) === 0 ? '1' : '0.01'"
              class="min-w-0 flex-1 rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 text-right text-lg tabular-nums outline-none focus:border-mf-green"
            />
            <span class="shrink-0">{{ settingBalance.currency }}</span>
          </label>
          <p class="text-xs text-mf-muted">
            <template v-if="balanceDelta !== 0">
              The opening balance moves by
              <MoneyAmount :minor="balanceDelta" :currency="String(settingBalance.currency)" />, so
              the records you already have add up to this.
            </template>
            <template v-else>Matches what your records add up to.</template>
            It is also saved as this month's capital check-in.
          </p>
          <div class="flex gap-3 pt-1">
            <button type="button" class="flex-1 rounded-full border border-mf-muted py-2.5" @click="settingBalance = null">
              Cancel
            </button>
            <button type="submit" class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white">
              Set balance
            </button>
          </div>
        </form>
      </ModalSheet>
    </Transition>

    <Transition name="mf-sheet">
      <ModalSheet v-if="open" size="lg" :label="editing ? 'Edit account' : 'New account'" @close="closeForm">
        <form class="space-y-4 p-4 sm:p-6" @submit.prevent="save">
          <h2 class="text-lg font-medium">{{ editing ? 'Edit account' : 'New account' }}</h2>

          <input
            v-model="form.name"
            type="text"
            placeholder="Name"
            required
            maxlength="40"
            class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
          />

          <div class="grid gap-3 sm:grid-cols-2">
            <label class="text-sm">
              <span class="mb-1 block text-mf-muted">Group</span>
              <input
                v-model="form.group"
                type="text"
                list="account-groups"
                maxlength="30"
                :placeholder="`e.g. Banks, Gold — or leave for “${UNGROUPED}”`"
                class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
              />
              <datalist id="account-groups">
                <option v-for="g in groupSuggestions" :key="g" :value="g" />
              </datalist>
            </label>

            <label class="text-sm">
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
              <button type="button" class="mt-1 text-xs text-mf-green-dark" @click="router.push('/currencies')">
                Add a currency…
              </button>
            </label>

            <label class="text-sm">
              <span class="mb-1 block text-mf-muted">{{ editing ? 'Balance now' : 'Starting balance' }}</span>
              <input
                v-model="form.balance"
                type="number"
                :step="exponent(form.currency) === 0 ? '1' : '0.01'"
                class="w-full rounded-lg border border-mf-muted/60 bg-mf-surface px-3 py-2 outline-none focus:border-mf-green"
              />
              <span v-if="editing" class="mt-1 block text-xs text-mf-muted">
                Opening balance
                <MoneyAmount
                  :minor="Number(editing.initialBalanceMinor ?? 0)"
                  :currency="String(editing.currency)"
                />
                — it moves if you change this.
              </span>
            </label>

            <!--
              A switch rather than a checkbox: it is a property of the account,
              read as "is this money to hand", and it changes a headline number.
            -->
            <label class="flex cursor-pointer items-start gap-3 text-sm sm:pt-6">
              <button
                type="button"
                role="switch"
                :aria-checked="form.liquid"
                class="relative mt-0.5 h-6 w-10 shrink-0 rounded-full transition-colors"
                :class="form.liquid ? 'bg-mf-green' : 'bg-mf-muted'"
                @click="form.liquid = !form.liquid"
              >
                <span
                  class="absolute top-0.5 left-0.5 size-5 rounded-full bg-mf-surface shadow transition-transform"
                  :class="form.liquid && 'translate-x-4'"
                />
              </button>
              <span>
                <span class="block">Ready to use</span>
                <span class="block text-xs text-mf-muted">
                  Off for money that is yours but not to hand — a term deposit, gold, investments.
                </span>
              </span>
            </label>

            <!--
              Separate from "Ready to use": crypto can be to hand and still not
              somewhere you log spending, and a USD stash can be kept and left
              alone. Off hides the account from the record screen only —
              transfers, balances and capital still count it.
            -->
            <label class="flex cursor-pointer items-start gap-3 text-sm sm:pt-6">
              <button
                type="button"
                role="switch"
                :aria-checked="form.spendable"
                class="relative mt-0.5 h-6 w-10 shrink-0 rounded-full transition-colors"
                :class="form.spendable ? 'bg-mf-green' : 'bg-mf-muted'"
                @click="form.spendable = !form.spendable"
              >
                <span
                  class="absolute top-0.5 left-0.5 size-5 rounded-full bg-mf-surface shadow transition-transform"
                  :class="form.spendable && 'translate-x-4'"
                />
              </button>
              <span>
                <span class="block">Track spending</span>
                <span class="block text-xs text-mf-muted">
                  Off for an account you only hold money in — investments, a rarely used USD stash.
                  It stays in your balances and capital, and you can still transfer to and from it.
                </span>
              </span>
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
            <div class="grid grid-cols-8 gap-2 sm:grid-cols-12">
              <button
                v-for="option in iconNames"
                :key="option"
                type="button"
                class="grid aspect-square place-items-center rounded-lg border"
                :class="form.icon === option ? 'border-mf-green bg-mf-green-soft/30' : 'border-transparent'"
                :aria-label="option"
                @click="form.icon = option"
              >
                <CategoryIcon :icon="option" :color="form.color" :size="22" />
              </button>
            </div>
          </div>

          <div class="flex gap-3 pt-1">
            <button type="button" class="flex-1 rounded-full border border-mf-muted py-2.5" @click="closeForm">
              Cancel
            </button>
            <button type="submit" class="flex-1 rounded-full bg-mf-green py-2.5 font-medium text-white">
              Save
            </button>
          </div>

          <!--
            Archiving lives here, not on the row: it is rare, and a row's
            buttons sit next to the balance people tap every month.
          -->
          <button
            v-if="editing"
            type="button"
            class="mx-auto flex items-center gap-1.5 text-sm text-mf-red-text"
            @click="archiveEditing"
          >
            <Archive :size="15" :stroke-width="1.8" /> Archive this account
          </button>
        </form>
      </ModalSheet>
    </Transition>
  </div>
</template>
