<script setup lang="ts">
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
import { useSyncStore } from '@/stores/sync'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Row } from '@/sync/types'

const dashboard = useDashboardStore()
const taxonomy = useTaxonomyStore()
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

/**
 * Tapping a category — on the donut or in the list — starts an expense already
 * assigned to it. For anything you buy regularly that turns three taps into two.
 */
const recordIn = (category: Row) =>
  router.push({ path: '/new/expense', query: { category: String(category.id) } })

const todo = (what: string) => () => console.info(`${what} lands in a later phase`)
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
      <p
        v-if="dashboard.foreignCount"
        class="mx-4 mt-2 shrink-0 rounded-lg bg-mf-green-soft/30 p-2 text-xs"
      >
        {{ dashboard.foreignCount }} record(s) in another currency are not included — exchange rates
        arrive in a later phase.
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
          @edit="todo('Editing a record')"
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

    <p
      v-if="syncStore.pending || syncStore.state === 'offline'"
      class="shrink-0 pt-1 text-center text-xs text-mf-muted"
    >
      {{ syncStore.label }}
    </p>

    <RecordFabs @expense="router.push('/new/expense')" @income="router.push('/new/income')" />

    <Transition name="mf-sheet">
      <RecordsSheet
        v-if="showRecords"
        :rows="dashboard.rows"
        :currency="dashboard.baseCurrency"
        :title="dashboard.label"
        @close="showRecords = false"
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
