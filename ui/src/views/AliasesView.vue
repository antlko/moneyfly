<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { pushToast } from '@/lib/toast'
import { useTaxonomyStore } from '@/stores/taxonomy'

/**
 * The alias tables, visible and editable. A mapping decided during an import stays
 * inspectable afterwards (docs/08-ux.md §8.8), and these are what prevent the 14%
 * silent loss the old pipeline had.
 */
const taxonomy = useTaxonomyStore()

const newCategoryAlias = ref({ target: null as number | null, source: '' })
const newAccountAlias = ref({ target: null as number | null, source: '' })
const error = ref('')

async function addCategoryAlias() {
  error.value = ''
  if (!newCategoryAlias.value.target || !newCategoryAlias.value.source) return
  try {
    await taxonomy.createCategoryAlias(newCategoryAlias.value.target, newCategoryAlias.value.source)
    pushToast('Alias added. That source name will resolve from now on.', 'success')
    newCategoryAlias.value = { target: null, source: '' }
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Could not save the alias.'
  }
}

async function addAccountAlias() {
  error.value = ''
  if (!newAccountAlias.value.target || !newAccountAlias.value.source) return
  try {
    await taxonomy.createAccountAlias(newAccountAlias.value.target, newAccountAlias.value.source)
    pushToast('Alias added.', 'success')
    newAccountAlias.value = { target: null, source: '' }
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Could not save the alias.'
  }
}

onMounted(async () => {
  await taxonomy.load()
  await taxonomy.loadAliases()
})
</script>

<template>
  <section>
    <h1 class="mb-1 text-lg font-semibold">Import aliases</h1>
    <p class="mb-4 text-xs text-slate-500">
      Source names as they appear in an export, mapped to canonical categories and accounts. An
      unmapped name blocks its import batch rather than being silently dropped — that is what these
      rows prevent. Whitespace is significant: <code>Family&nbsp;</code> and <code>Family</code> are
      different source names.
    </p>

    <p v-if="error" class="mb-3 text-sm text-state-severe" role="alert">{{ error }}</p>

    <h2 class="mb-2 mt-4 text-sm font-semibold uppercase tracking-wide text-slate-500">
      Categories ({{ taxonomy.categoryAliases.length }})
    </h2>
    <form class="mb-3 flex flex-wrap gap-2" @submit.prevent="addCategoryAlias">
      <label for="cat-source" class="sr-only">Source name</label>
      <input
        id="cat-source"
        v-model="newCategoryAlias.source"
        placeholder="Source name from the file"
        class="min-w-40 flex-1 rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <label for="cat-target" class="sr-only">Category</label>
      <select
        id="cat-target"
        v-model="newCategoryAlias.target"
        class="rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        <option :value="null">Maps to…</option>
        <option v-for="c in taxonomy.categories" :key="c.id" :value="c.id">{{ c.name }}</option>
      </select>
      <button
        type="submit"
        class="tap-target rounded-xl bg-brand-600 px-3 py-2 text-sm font-semibold text-white"
      >
        Add
      </button>
    </form>
    <ul class="space-y-1">
      <li
        v-for="alias in taxonomy.categoryAliases"
        :key="alias.id"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm dark:border-slate-800 dark:bg-slate-900"
      >
        <code class="flex-1 truncate">{{ JSON.stringify(alias.source_name) }}</code>
        <span aria-hidden="true" class="text-slate-400">→</span>
        <span class="flex-1 truncate">{{ alias.target_name }}</span>
        <button
          type="button"
          class="tap-target px-2 text-xs text-state-severe underline"
          :aria-label="'Delete alias ' + alias.source_name"
          @click="taxonomy.deleteCategoryAlias(alias.id)"
        >
          delete
        </button>
      </li>
    </ul>

    <h2 class="mb-2 mt-6 text-sm font-semibold uppercase tracking-wide text-slate-500">
      Accounts ({{ taxonomy.accountAliases.length }})
    </h2>
    <form class="mb-3 flex flex-wrap gap-2" @submit.prevent="addAccountAlias">
      <label for="acc-source" class="sr-only">Source name</label>
      <input
        id="acc-source"
        v-model="newAccountAlias.source"
        placeholder="Source name from the file"
        class="min-w-40 flex-1 rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <label for="acc-target" class="sr-only">Account</label>
      <select
        id="acc-target"
        v-model="newAccountAlias.target"
        class="rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        <option :value="null">Maps to…</option>
        <option v-for="a in taxonomy.accounts" :key="a.id" :value="a.id">{{ a.name }}</option>
      </select>
      <button
        type="submit"
        class="tap-target rounded-xl bg-brand-600 px-3 py-2 text-sm font-semibold text-white"
      >
        Add
      </button>
    </form>
    <ul class="space-y-1">
      <li
        v-for="alias in taxonomy.accountAliases"
        :key="alias.id"
        class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm dark:border-slate-800 dark:bg-slate-900"
      >
        <code class="flex-1 truncate">{{ JSON.stringify(alias.source_name) }}</code>
        <span aria-hidden="true" class="text-slate-400">→</span>
        <span class="flex-1 truncate">{{ alias.target_name }}</span>
        <button
          type="button"
          class="tap-target px-2 text-xs text-state-severe underline"
          :aria-label="'Delete alias ' + alias.source_name"
          @click="taxonomy.deleteAccountAlias(alias.id)"
        >
          delete
        </button>
      </li>
    </ul>
  </section>
</template>
