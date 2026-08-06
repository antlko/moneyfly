<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'

import CategoryIcon from '@/components/monefy/CategoryIcon.vue'
import MoneyAmount from '@/components/monefy/MoneyAmount.vue'
import { shortDate } from '@/lib/period'
import type { PeriodKind } from '@/lib/period'
import { useAccountsStore } from '@/stores/accounts'
import { useDashboardStore } from '@/stores/dashboard'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

/**
 * The desktop analytics dashboard's content — everything except the sidebar,
 * which DesktopShell provides once for every route.
 *
 * Every figure here reads the same stores the mobile dashboard does
 * (dashboard.ts, accounts.ts, taxonomy.ts): this is a second *view* of the
 * same computed data, not a second implementation of it. A total that
 * disagreed between the phone and the desktop dashboard would be far worse
 * than either one looking plain.
 */
const dashboard = useDashboardStore()
const accounts = useAccountsStore()
const taxonomy = useTaxonomyStore()
const router = useRouter()

const PERIOD_KINDS: { kind: PeriodKind; label: string }[] = [
  { kind: 'month', label: 'Month' },
  { kind: 'week', label: 'Week' },
  { kind: 'day', label: 'Day' },
  { kind: 'year', label: 'Year' },
  { kind: 'all', label: 'All time' },
]

// Most recent first, and capped: this is a glance at recent activity, not a
// register — Search already exists for finding a specific old record, and a
// table of everything in "All time" would make the page itself the thing
// that needs scrolling past.
const RECENT_LIMIT = 50
const recentRows = computed(() => [...dashboard.rows].reverse().slice(0, RECENT_LIMIT))

const accountName = (id: unknown) => taxonomy.accounts.find((a) => a.id === id)?.name ?? ''

function openRecord(row: Row) {
  void router.push(`/edit/${row.id}`)
}
</script>

<template>
  <div class="space-y-6">
    <!-- Period -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <button
          v-if="dashboard.pageable"
          type="button"
          aria-label="Previous period"
          class="grid size-8 place-items-center rounded-full text-mf-muted hover:bg-mf-surface"
          @click="dashboard.step(-1)"
        >
          <ChevronLeft :size="18" />
        </button>
        <h1 class="min-w-40 text-center text-xl font-semibold text-mf-ink">{{ dashboard.label }}</h1>
        <button
          v-if="dashboard.pageable"
          type="button"
          aria-label="Next period"
          class="grid size-8 place-items-center rounded-full text-mf-muted hover:bg-mf-surface"
          @click="dashboard.step(1)"
        >
          <ChevronRight :size="18" />
        </button>
      </div>

      <div class="flex gap-1 rounded-full bg-mf-surface p-1">
        <button
          v-for="p in PERIOD_KINDS"
          :key="p.kind"
          type="button"
          class="rounded-full px-3 py-1.5 text-sm"
          :class="
            dashboard.period.kind === p.kind
              ? 'bg-mf-green text-white'
              : 'text-mf-ink hover:bg-mf-bg'
          "
          @click="dashboard.setPeriodKind(p.kind)"
        >
          {{ p.label }}
        </button>
      </div>
    </div>

    <p v-if="dashboard.unconvertedCount > 0" class="rounded-lg bg-mf-red/15 px-4 py-2 text-sm text-mf-red-text">
      {{ dashboard.unconvertedCount }} record{{ dashboard.unconvertedCount === 1 ? '' : 's' }} not
      counted below — no exchange rate for that day yet.
    </p>

    <!-- Summary cards -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Income</p>
        <MoneyAmount :minor="dashboard.incomeMinor" :currency="dashboard.baseCurrency" class="text-2xl text-mf-green-dark" />
      </div>
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Expenses</p>
        <MoneyAmount
          :minor="Math.abs(dashboard.expenseMinor)"
          :currency="dashboard.baseCurrency"
          class="text-2xl text-mf-red-text"
        />
      </div>
      <div class="rounded-2xl bg-mf-surface p-5">
        <p class="text-sm text-mf-muted">Balance</p>
        <MoneyAmount
          :minor="dashboard.balanceMinor"
          :currency="dashboard.baseCurrency"
          class="text-2xl"
          :class="dashboard.balanceMinor < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
        />
      </div>
    </div>

    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <!-- Category breakdown -->
      <section class="rounded-2xl bg-mf-surface p-5">
        <h2 class="mb-4 font-medium">By category</h2>
        <p v-if="dashboard.byCategory.length === 0" class="text-sm text-mf-muted">
          Nothing recorded for this period.
        </p>
        <ul v-else class="space-y-3">
          <li v-for="c in dashboard.byCategory" :key="String(c.category.id)">
            <div class="mb-1 flex items-center gap-2 text-sm">
              <CategoryIcon :icon="c.category.icon" :color="c.category.color" :size="18" />
              <span class="min-w-0 flex-1 truncate">{{ c.category.name }}</span>
              <MoneyAmount :minor="Math.abs(c.totalMinor)" :currency="dashboard.baseCurrency" />
            </div>
            <div class="h-1.5 overflow-hidden rounded-full bg-mf-muted/20">
              <div
                class="h-full rounded-full"
                :style="{
                  width: `${Math.round(c.share * 100)}%`,
                  backgroundColor: `var(--color-cat-${c.category.color ?? 'gray'})`,
                }"
              />
            </div>
          </li>
        </ul>
      </section>

      <!-- Accounts -->
      <section class="rounded-2xl bg-mf-surface p-5">
        <h2 class="mb-4 font-medium">Accounts</h2>
        <ul class="divide-y divide-mf-muted/20">
          <li
            v-for="a in taxonomy.activeAccounts"
            :key="String(a.id)"
            class="flex items-center gap-3 py-2 text-sm"
          >
            <CategoryIcon :icon="a.icon" :color="a.color" :size="20" />
            <span class="min-w-0 flex-1 truncate">{{ a.name }}</span>
            <MoneyAmount
              :minor="accounts.balanceOf(a.id)"
              :currency="String(a.currency)"
              :class="accounts.balanceOf(a.id) < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
            />
          </li>
        </ul>
      </section>
    </div>

    <!-- Recent transactions -->
    <section class="rounded-2xl bg-mf-surface p-5">
      <h2 class="mb-4 font-medium">
        Recent records
        <span class="text-sm font-normal text-mf-muted">({{ recentRows.length }})</span>
      </h2>
      <p v-if="recentRows.length === 0" class="text-sm text-mf-muted">Nothing recorded for this period.</p>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b border-mf-muted/20 text-left text-xs text-mf-muted">
            <th class="pb-2 font-normal">Date</th>
            <th class="pb-2 font-normal">Category</th>
            <th class="pb-2 font-normal">Account</th>
            <th class="pb-2 font-normal">Note</th>
            <th class="pb-2 text-right font-normal">Amount</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in recentRows"
            :key="String(row.id)"
            class="cursor-pointer border-b border-mf-muted/10 last:border-0 hover:bg-mf-bg"
            @click="openRecord(row)"
          >
            <td class="py-2 whitespace-nowrap text-mf-muted">{{ shortDate(String(row.occurredOn)) }}</td>
            <td class="py-2">
              <span class="flex items-center gap-2">
                <CategoryIcon
                  v-if="taxonomy.byId.get(String(row.categoryId))"
                  :icon="taxonomy.byId.get(String(row.categoryId))?.icon"
                  :color="taxonomy.byId.get(String(row.categoryId))?.color"
                  :size="16"
                />
                {{ row.kind === 'transfer' ? 'Transfer' : (taxonomy.byId.get(String(row.categoryId))?.name ?? 'Uncategorised') }}
              </span>
            </td>
            <td class="py-2 text-mf-muted">{{ accountName(row.accountId) }}</td>
            <td class="max-w-50 truncate py-2 text-mf-muted">{{ row.note }}</td>
            <td
              class="py-2 text-right font-medium whitespace-nowrap"
              :class="Number(row.amountMinor ?? 0) < 0 ? 'text-mf-red-text' : 'text-mf-green-dark'"
            >
              <MoneyAmount :minor="Number(row.amountMinor ?? 0)" :currency="String(row.currency)" />
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>
