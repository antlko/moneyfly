<script setup lang="ts">
import { useEventListener } from '@vueuse/core'
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import BalancePill from '@/components/monefy/BalancePill.vue'
import CategoryDonut from '@/components/monefy/CategoryDonut.vue'
import CategoryList from '@/components/monefy/CategoryList.vue'
import MonefyHeader from '@/components/monefy/MonefyHeader.vue'
import MonthCarousel from '@/components/monefy/MonthCarousel.vue'
import RecordFabs from '@/components/monefy/RecordFabs.vue'
import FilterDrawer from '@/components/monefy/FilterDrawer.vue'
import MenuDrawer from '@/components/monefy/MenuDrawer.vue'
import RecordsSheet from '@/components/monefy/RecordsSheet.vue'
import SwipePager from '@/components/monefy/SwipePager.vue'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { SETTING, useSettingsStore } from '@/stores/settings'
import { useSyncStore } from '@/stores/sync'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
const fx = useFxStore()
const settings = useSettingsStore()
const syncStore = useSyncStore()
const router = useRouter()

const showRecords = ref(false)
const showFilter = ref(false)
const showMenu = ref(false)

// Seed the starting categories once the replica has had a chance to arrive, so
// a second device does not briefly show an empty grid and write its own set.
// The ids are deterministic, so even a genuine race merges rather than duplicates.
onMounted(() => taxonomy.ensureSeeded(dashboard.baseCurrency))
watch(
  () => syncStore.revision,
  () => void taxonomy.ensureSeeded(dashboard.baseCurrency),
)

/*
 * Top the rate cache up when the app opens and whenever it comes back to the
 * foreground — the same trigger the sync engine uses, and for the same reason:
 * a phone that has been in a pocket for a week has stale rates and no idea.
 *
 * Which currencies get fetched is read from the replica, so this costs one
 * request per foreign currency actually in use, and none at all for someone
 * with a single currency.
 */
const declaredCurrencies = () => settings.get<string[]>(SETTING.currencies, [])

onMounted(() => void fx.refresh(dashboard.baseCurrency, declaredCurrencies()))
useEventListener(document, 'visibilitychange', () => {
  if (!document.hidden) void fx.refresh(dashboard.baseCurrency, declaredCurrencies())
})

/**
 * Tapping a category — on the donut or in the list — starts an expense already
 * assigned to it. For anything you buy regularly that turns three taps into two.
 */
const recordIn = (category: Row) =>
  router.push({ path: '/new/expense', query: { category: String(category.id) } })

/** Tapping a record opens it for editing — the row itself is the control. */
const openRecord = (row: Row) => router.push(`/edit/${row.id}`)
</script>

<template>
  <div class="flex h-full flex-col overflow-hidden bg-mf-bg">
    <MonefyHeader
      :subtitle="dashboard.accountLabel"
      @filter="showFilter = true"
      @search="router.push('/search')"
      @transfer="router.push('/transfer')"
      @menu="showMenu = true"
    />

    <MonthCarousel :period="dashboard.period" @select="dashboard.setPeriod" />

    <!--
      The balance pill moves. In list mode it sits above the rows, as a header
      for them; in donut mode the reference puts it at the bottom, just over the
      record buttons, so the chart owns the middle of the screen. Two positions
      for one component rather than two components.
    -->
    <BalancePill
      v-if="dashboard.view === 'list'"
      :balance-minor="dashboard.balanceMinor"
      :currency="dashboard.baseCurrency"
      @toggle-view="dashboard.toggleView"
      @toggle-sort="dashboard.toggleSort"
      @open="showRecords = true"
    />

    <SwipePager
      :key="dashboard.view"
      :disabled="!dashboard.pageable"
      @prev="dashboard.step(-1)"
      @next="dashboard.step(1)"
    >
      <!--
        Foreign-currency rows are converted now, so this warns about the case
        that is left: no rate known for that pair on that day. Those rows are
        out of the totals, and saying so is the difference between a visible gap
        and a month that quietly looks cheaper than it was.
      -->
      <p
        v-if="dashboard.unconvertedCount"
        class="mx-4 mt-2 shrink-0 rounded-lg bg-mf-green-soft/30 p-2 text-xs"
      >
        {{ dashboard.unconvertedCount }} record(s) have no exchange rate yet and are not included in
        the total.
      </p>

      <!-- The donut block takes the whole body: its icon frame is sized to fill it. -->
      <div v-if="dashboard.view === 'donut'" class="min-h-0 flex-1 px-2 py-1">
        <CategoryDonut
          :totals="dashboard.byCategory"
          :all-categories="taxonomy.expenseCategories"
          :income-minor="dashboard.incomeMinor"
          :expense-minor="dashboard.expenseMinor"
          :currency="dashboard.baseCurrency"
          @select="recordIn"
        />
      </div>
      <div v-else class="min-h-0 flex-1 overflow-y-auto">
        <CategoryList
          :totals="dashboard.byCategory"
          :rows="dashboard.rows"
          :currency="dashboard.baseCurrency"
          @open="openRecord"
        />
      </div>
    </SwipePager>

    <BalancePill
      v-if="dashboard.view === 'donut'"
      :balance-minor="dashboard.balanceMinor"
      :currency="dashboard.baseCurrency"
      show-trailing-toggle
      @toggle-view="dashboard.toggleView"
      @toggle-sort="dashboard.toggleSort"
      @open="showRecords = true"
    />

    <RecordFabs @expense="router.push('/new/expense')" @income="router.push('/new/income')" />

    <Transition name="mf-sheet">
      <RecordsSheet
        v-if="showRecords"
        :rows="dashboard.rows"
        :title="dashboard.label"
        @close="showRecords = false"
        @open="openRecord"
      />
    </Transition>

    <Transition name="mf-drawer-l">
      <FilterDrawer
        v-if="showFilter"
        :period="dashboard.period"
        :account-label="dashboard.accountLabel"
        :selected="dashboard.accountFilter"
        :accounts="taxonomy.activeAccounts"
        :currency="dashboard.baseCurrency"
        @close="showFilter = false"
        @kind="dashboard.setPeriodKind"
        @interval="dashboard.setInterval"
        @go-to-day="dashboard.goToDay"
        @select-accounts="dashboard.setAccountFilter"
        @manage-accounts="router.push('/accounts')"
      />
    </Transition>

    <Transition name="mf-drawer-r">
      <MenuDrawer v-if="showMenu" @close="showMenu = false" @go="router.push($event)" />
    </Transition>
  </div>
</template>
