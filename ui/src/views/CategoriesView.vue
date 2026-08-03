<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ApiError } from '@/api/client'
import { pushToast } from '@/lib/toast'
import { useTaxonomyStore } from '@/stores/taxonomy'
import type { Category, CategoryInput } from '@/api/types'

const taxonomy = useTaxonomyStore()

const showArchived = ref(false)
const archived = ref<Category[]>([])
const editing = ref<Category | null>(null)
const creating = ref(false)
const mergeSource = ref<Category | null>(null)
const mergeTarget = ref<number | null>(null)
const error = ref('')

const draft = ref<CategoryInput>({
  name: '',
  kind: 'expense',
  is_essential: false,
  icon: '',
  color: '#8b5cf6',
})

const palette = [
  '#4F7CAC',
  '#57BB8A',
  '#E8A33D',
  '#D96C6C',
  '#8E7DBE',
  '#4FB0C6',
  '#C9A227',
  '#7E8D85',
]
const icons = [
  '🏠',
  '🛒',
  '🍽️',
  '🚌',
  '💊',
  '👕',
  '🎁',
  '🎨',
  '📱',
  '🧾',
  '💡',
  '🚕',
  '✈️',
  '🏋️',
  '📚',
  '🧼',
]

function startCreate() {
  creating.value = true
  editing.value = null
  draft.value = { name: '', kind: 'expense', is_essential: false, icon: '🏷️', color: '#8b5cf6' }
}

function startEdit(c: Category) {
  editing.value = c
  creating.value = false
  draft.value = {
    name: c.name,
    kind: c.kind,
    is_essential: c.is_essential,
    icon: c.icon ?? '',
    color: c.color ?? '#8b5cf6',
    parent_id: c.parent_id,
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
      await taxonomy.updateCategory(editing.value.id, draft.value)
      pushToast(`${draft.value.name} updated.`, 'success')
    } else {
      await taxonomy.createCategory(draft.value)
      pushToast(`${draft.value.name} created.`, 'success')
    }
    cancel()
  } catch (err) {
    error.value =
      err instanceof ApiError
        ? Object.values(err.fieldErrors)[0] ||
          (err.status === 409 ? 'A category with that name already exists.' : err.message)
        : 'Could not reach the server.'
  }
}

async function archive(c: Category) {
  await taxonomy.archiveCategory(c.id)
  // Archiving preserves history: the category leaves entry, historical rows keep it.
  pushToast(`${c.name} archived. Its history is untouched.`, 'info')
  if (showArchived.value) archived.value = await taxonomy.loadArchived()
}

async function toggleEssential(c: Category) {
  await taxonomy.updateCategory(c.id, {
    name: c.name,
    kind: c.kind,
    is_essential: !c.is_essential,
    icon: c.icon ?? undefined,
    color: c.color ?? undefined,
    sort_order: c.sort_order,
    parent_id: c.parent_id,
  })
}

async function move(index: number, delta: number) {
  const list = [...taxonomy.categories]
  const target = index + delta
  if (target < 0 || target >= list.length) return
  ;[list[index], list[target]] = [list[target], list[index]]
  await taxonomy.reorderCategories(list)
}

async function doMerge() {
  if (!mergeSource.value || !mergeTarget.value) return
  try {
    await taxonomy.mergeCategory(mergeSource.value.id, mergeTarget.value)
    pushToast('Merged. The old name now resolves to the target on import.', 'success')
    mergeSource.value = null
    mergeTarget.value = null
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Merge failed.'
  }
}

async function toggleArchived() {
  showArchived.value = !showArchived.value
  if (showArchived.value) archived.value = await taxonomy.loadArchived()
}

onMounted(() => taxonomy.load(true))
</script>

<template>
  <section>
    <header class="mb-4 flex items-center gap-2">
      <h1 class="flex-1 text-lg font-semibold">Categories</h1>
      <button
        type="button"
        class="tap-target rounded-xl bg-brand-600 px-3 py-2 text-sm font-semibold text-white"
        @click="startCreate"
      >
        New
      </button>
    </header>

    <p class="mb-4 text-xs text-slate-500">
      The <strong>essential</strong> toggle drives Possible Minimum. Archiving keeps history;
      merging rewrites transactions and leaves an alias behind so imports still resolve.
    </p>

    <form
      v-if="creating || editing"
      class="mb-4 space-y-3 rounded-xl border border-brand-300 bg-brand-50 p-3 dark:border-brand-700 dark:bg-slate-900"
      @submit.prevent="save"
    >
      <div class="grid grid-cols-2 gap-2">
        <div class="col-span-2">
          <label for="c-name" class="mb-1 block text-sm font-medium">Name</label>
          <input
            id="c-name"
            v-model="draft.name"
            required
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
        <div>
          <label for="c-kind" class="mb-1 block text-sm font-medium">Kind</label>
          <select
            id="c-kind"
            v-model="draft.kind"
            class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="expense">Expense</option>
            <option value="income">Income</option>
          </select>
        </div>
        <div class="flex items-end">
          <label class="tap-target flex items-center gap-2 text-sm">
            <input v-model="draft.is_essential" type="checkbox" class="h-5 w-5" />
            Essential
          </label>
        </div>
      </div>

      <fieldset>
        <legend class="mb-1 text-sm font-medium">Icon</legend>
        <div class="flex flex-wrap gap-1">
          <button
            v-for="icon in icons"
            :key="icon"
            type="button"
            class="tap-target rounded-lg border px-2 text-lg"
            :class="
              draft.icon === icon
                ? 'border-brand-500 bg-white dark:bg-slate-800'
                : 'border-transparent'
            "
            :aria-label="'Icon ' + icon"
            :aria-pressed="draft.icon === icon"
            @click="draft.icon = icon"
          >
            {{ icon }}
          </button>
        </div>
      </fieldset>

      <fieldset>
        <legend class="mb-1 text-sm font-medium">Colour</legend>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="color in palette"
            :key="color"
            type="button"
            class="tap-target h-8 w-8 rounded-full border-2"
            :class="
              draft.color === color ? 'border-slate-900 dark:border-white' : 'border-transparent'
            "
            :style="{ backgroundColor: color }"
            :aria-label="'Colour ' + color"
            :aria-pressed="draft.color === color"
            @click="draft.color = color"
          />
        </div>
      </fieldset>

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
        v-for="(c, index) in taxonomy.categories"
        :key="c.id"
        class="flex items-center gap-2 rounded-xl border border-slate-200 bg-white p-2 dark:border-slate-800 dark:bg-slate-900"
      >
        <span
          class="flex h-8 w-8 flex-none items-center justify-center rounded-full"
          :style="{ backgroundColor: (c.color ?? '#64748b') + '22' }"
          aria-hidden="true"
          >{{ c.icon ?? '•' }}</span
        >
        <span class="flex-1 truncate">
          {{ c.name }}
          <span class="ml-1 text-xs text-slate-400">{{ c.kind }}</span>
        </span>

        <button
          type="button"
          class="tap-target rounded-lg px-2 text-xs"
          :class="c.is_essential ? 'bg-state-within/15 text-state-within' : 'text-slate-400'"
          :aria-pressed="c.is_essential"
          :aria-label="(c.is_essential ? 'Unmark' : 'Mark') + ' ' + c.name + ' as essential'"
          @click="toggleEssential(c)"
        >
          essential
        </button>

        <div class="flex flex-none">
          <button
            type="button"
            class="tap-target px-1 text-slate-400"
            :aria-label="'Move ' + c.name + ' up'"
            @click="move(index, -1)"
          >
            ↑
          </button>
          <button
            type="button"
            class="tap-target px-1 text-slate-400"
            :aria-label="'Move ' + c.name + ' down'"
            @click="move(index, 1)"
          >
            ↓
          </button>
        </div>

        <button
          type="button"
          class="tap-target px-2 text-xs text-brand-600 underline"
          @click="startEdit(c)"
        >
          edit
        </button>
        <button
          type="button"
          class="tap-target px-2 text-xs text-slate-500 underline"
          @click="mergeSource = c"
        >
          merge
        </button>
        <button
          type="button"
          class="tap-target px-2 text-xs text-state-severe underline"
          @click="archive(c)"
        >
          archive
        </button>
      </li>
    </ul>

    <div
      v-if="mergeSource"
      class="mt-4 rounded-xl border border-slate-300 p-3 dark:border-slate-700"
      role="group"
      :aria-label="'Merge ' + mergeSource.name"
    >
      <p class="mb-2 text-sm">
        Merge <strong>{{ mergeSource.name }}</strong> into:
      </p>
      <select
        v-model="mergeTarget"
        class="mb-2 w-full rounded-xl border border-slate-300 bg-white px-3 py-2 dark:border-slate-700 dark:bg-slate-900"
      >
        <option :value="null">Choose a category…</option>
        <option
          v-for="c in taxonomy.categories.filter(
            (x) => x.id !== mergeSource!.id && x.kind === mergeSource!.kind,
          )"
          :key="c.id"
          :value="c.id"
        >
          {{ c.name }}
        </option>
      </select>
      <div class="flex gap-2">
        <button
          type="button"
          class="tap-target rounded-xl bg-brand-600 px-3 py-2 text-sm font-semibold text-white disabled:opacity-40"
          :disabled="!mergeTarget"
          @click="doMerge"
        >
          Merge
        </button>
        <button
          type="button"
          class="tap-target px-3 py-2 text-sm text-slate-500"
          @click="mergeSource = null"
        >
          Cancel
        </button>
      </div>
    </div>

    <button
      type="button"
      class="tap-target mt-4 text-sm text-slate-500 underline"
      @click="toggleArchived"
    >
      {{ showArchived ? 'Hide' : 'Show' }} archived
    </button>
    <ul v-if="showArchived" class="mt-2 space-y-1">
      <li
        v-for="c in archived.filter((x) => x.archived_at)"
        :key="c.id"
        class="rounded-xl border border-dashed border-slate-300 p-2 text-sm text-slate-500 dark:border-slate-700"
      >
        {{ c.icon }} {{ c.name }} — archived, history preserved
      </li>
    </ul>
  </section>
</template>
