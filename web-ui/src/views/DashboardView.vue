<script setup lang="ts">
import { useEventListener } from '@vueuse/core'
import { defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import BalancePill from '@/components/monefy/BalancePill.vue'
import CategoryDonut from '@/components/monefy/CategoryDonut.vue'
import CategoryList from '@/components/monefy/CategoryList.vue'
import MonefyHeader from '@/components/monefy/MonefyHeader.vue'
import MonthCarousel from '@/components/monefy/MonthCarousel.vue'
import PullUpSheet from '@/components/monefy/PullUpSheet.vue'
import RecordFabs from '@/components/monefy/RecordFabs.vue'
import SwipePager from '@/components/monefy/SwipePager.vue'

/*
 * The four components that are never part of a first paint.
 *
 * This is the app's landing route, so the view itself stays eagerly imported —
 * lazy-loading it would put a round trip in front of the one screen that has to
 * be instant. Its component *graph* is a different question: the three overlays
 * are behind `v-if` and appear only once someone opens them, and the desktop
 * content is never rendered on a phone at all, yet all four were in the entry
 * chunk that every phone downloads before it can show anything.
 *
 * Each is wrapped in a <Transition> at the call site, which is what makes this
 * safe — the chunk resolves while the enter transition runs, and there is no
 * fallback to flash because these mount over the screen rather than in it.
 */
const FilterDrawer = defineAsyncComponent(() => import('@/components/monefy/FilterDrawer.vue'))
const MenuDrawer = defineAsyncComponent(() => import('@/components/monefy/MenuDrawer.vue'))
const RecordsSheet = defineAsyncComponent(() => import('@/components/monefy/RecordsSheet.vue'))
const DesktopDashboardContent = defineAsyncComponent(
  () => import('@/components/desktop/DesktopDashboardContent.vue'),
)
import { useIsDesktop } from '@/lib/breakpoint'
import { useDashboardStore } from '@/stores/dashboard'
import { useFxStore } from '@/stores/fx'
import { useRecordBurstStore } from '@/stores/recordBurst'
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
/**
 * The home route is the one place "the shell component picks the mobile
 * Monefy frame or the desktop dashboard" (CLAUDE.md) actually happens: every
 * other route reuses its existing component unmodified inside DesktopShell,
 * because only the dashboard's own layout — donut, carousel, swipe paging —
 * is genuinely mobile-shaped. Everything below is read from the same stores
 * either way; this only decides which template renders them.
 */
const isDesktop = useIsDesktop()

/**
 * A record saved a moment ago, rising out of the button that started it. Taken
 * on mount whatever the form factor, so a burst the desktop layout has nowhere
 * to play is dropped rather than kept for a later visit.
 */
const burst = ref(useRecordBurstStore().take())

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
  if (document.hidden) return
  void fx.refresh(dashboard.baseCurrency, declaredCurrencies())
  // Opening the app shows *now*. See `goToNow` — the period outlives a trip to
  // the home screen, so without this the app comes back on whatever month was
  // being read a week ago.
  dashboard.goToNow()
})
// An installed app restored from the back/forward cache gets no visibility
// change to announce it, only `pageshow`.
useEventListener(window, 'pageshow', () => dashboard.goToNow())

/**
 * Tapping a category — on the donut or in the list — starts an expense already
 * assigned to it. For anything you buy regularly that turns three taps into two.
 */
const recordIn = (category: Row) =>
  router.push({ path: '/new/expense', query: { category: String(category.id) } })

/** The sheet open is list mode; docked is the donut. */
const setListOpen = (open: boolean) => dashboard.setView(open ? 'list' : 'donut')

/** Tapping a record opens it for editing — the row itself is the control. */
const openRecord = (row: Row) => router.push(`/edit/${row.id}`)
</script>

<template>
  <DesktopDashboardContent v-if="isDesktop" />

  <div v-else class="flex h-full flex-col overflow-hidden bg-mf-bg">
    <MonefyHeader
      :subtitle="dashboard.accountLabel"
      @filter="showFilter = true"
      @search="router.push('/search')"
      @transfer="router.push('/transfer')"
      @menu="showMenu = true"
    />

    <MonthCarousel :period="dashboard.period" @select="dashboard.setPeriod" />

    <!--
      Donut and list are one screen with a sheet over it, not two layouts.

      The balance row is the sheet's handle. Docked, it sits over the record
      buttons with the donut above it; pulled up — by a drag that follows the
      finger, or by `≡`, which animates the same travel — it sits under the
      carousel with the category list below it. Those are exactly the two
      layouts the reference has, so at rest nothing differs from them; only the
      motion between them is new. See PullUpSheet.

      Each layer keeps its own pager, so a horizontal swipe changes the period
      over whichever one is showing.
    -->
    <PullUpSheet :open="dashboard.view === 'list'" @update:open="setListOpen">
      <template #behind>
        <SwipePager
          :disabled="!dashboard.pageable"
          @prev="dashboard.step(-1)"
          @next="dashboard.step(1)"
        >
          <!--
            Foreign-currency rows are converted now, so this warns about the case
            that is left: no rate known for that pair on that day. Those rows are
            out of the totals, and saying so is the difference between a visible
            gap and a month that quietly looks cheaper than it was.
          -->
          <p
            v-if="dashboard.unconvertedCount"
            class="mx-4 mt-2 shrink-0 rounded-lg bg-mf-green-soft/30 p-2 text-xs"
          >
            {{ dashboard.unconvertedCount }} record(s) have no exchange rate yet and are not
            included in the total.
          </p>
          <!--
            No horizontal padding: the icon ring is measured from this box, so
            every point of padding here pushes the categories in from the edges
            and takes the same off the chart's diameter twice over.
          -->
          <div class="min-h-0 flex-1 py-1">
            <CategoryDonut
              :totals="dashboard.byCategory"
              :all-categories="taxonomy.expenseCategories"
              :income-minor="dashboard.incomeMinor"
              :expense-minor="dashboard.expenseMinor"
              :currency="dashboard.baseCurrency"
              @select="recordIn"
            />
          </div>
        </SwipePager>
      </template>

      <template #handle>
        <BalancePill
          :balance-minor="dashboard.balanceMinor"
          :currency="dashboard.baseCurrency"
          :show-trailing-toggle="dashboard.view === 'donut'"
          @toggle-view="dashboard.toggleView"
          @toggle-sort="dashboard.toggleSort"
          @open="showRecords = true"
        />
      </template>

      <SwipePager
        :disabled="!dashboard.pageable"
        vertical-scroll
        @prev="dashboard.step(-1)"
        @next="dashboard.step(1)"
      >
        <p
          v-if="dashboard.unconvertedCount"
          class="mx-4 mt-2 shrink-0 rounded-lg bg-mf-green-soft/30 p-2 text-xs"
        >
          {{ dashboard.unconvertedCount }} record(s) have no exchange rate yet and are not included
          in the total.
        </p>
        <!--
          `overscroll-y-contain`: a scroll that reaches the end of the list
          stops there instead of chaining on to a page that cannot scroll — a
          pan with nothing under it, which costs the next tap (PullUpSheet).
        -->
        <div class="min-h-0 flex-1 overflow-y-auto overscroll-y-contain">
          <CategoryList
            :totals="dashboard.byCategory"
            :rows="dashboard.rows"
            :currency="dashboard.baseCurrency"
            @open="openRecord"
          />
        </div>
      </SwipePager>
    </PullUpSheet>

    <RecordFabs
      :burst="burst"
      @expense="router.push('/new/expense')"
      @income="router.push('/new/income')"
      @burst-done="burst = null"
    />

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
