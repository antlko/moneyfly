<script setup lang="ts">
import { useAccountsStore } from '@/stores/accounts'
import type { Row } from '@/sync/types'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'

/**
 * Pick which account a record belongs to.
 *
 * It also picks the record's currency: an expense is denominated in the account
 * that paid for it, the way the reference does it. That is why this is a sheet
 * and not a setting buried elsewhere — choosing here changes the keypad's
 * decimal places and the currency of the amount you are about to type.
 */
defineProps<{ options: Row[]; selectedId: string }>()
defineEmits<{ select: [Row]; close: [] }>()

const accounts = useAccountsStore()
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex flex-col justify-end bg-black/30"
    @click.self="$emit('close')"
  >
    <section
      class="max-h-[70%] overflow-y-auto rounded-t-2xl bg-mf-bg pb-[calc(1rem+var(--spacing-safe-b))]"
    >
      <header class="px-4 pt-3 pb-2">
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-mf-muted" />
        <h2 class="text-base font-medium">Account</h2>
      </header>

      <ul class="divide-y divide-mf-muted/20">
        <li v-for="account in options" :key="account.id">
          <button
            type="button"
            class="flex w-full items-center gap-3 px-4 py-3 text-left"
            :class="String(account.id) === selectedId && 'bg-mf-green-soft/25'"
            @click="$emit('select', account)"
          >
            <CategoryIcon :icon="account.icon" :color="account.color" :size="28" />
            <div class="min-w-0 flex-1">
              <p class="truncate">{{ account.name }}</p>
              <p class="text-xs text-mf-muted">{{ account.currency }}</p>
            </div>
            <MoneyAmount
              :minor="accounts.balanceOf(account.id)"
              :currency="String(account.currency)"
              class="text-sm"
              :class="accounts.balanceOf(account.id) < 0 ? 'text-mf-red-text' : 'text-mf-ink'"
            />
          </button>
        </li>
      </ul>
    </section>
  </div>
</template>
