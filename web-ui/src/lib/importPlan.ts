/**
 * What an import will actually do, worked out from the preview.
 *
 * Kept out of the view because it is the part that has to be *right*: the
 * screen's job is to say how many rows are about to be written and how many are
 * about to be skipped, and a wrong number there is worse than no number. It is
 * also the only part of the import screen that can be unit-tested at all —
 * there is no component test setup, by design (see vitest.config.ts).
 */
import type { ImportGroupCount, ImportNameStatus, ImportPreview } from '@/api/http'
import { CATEGORY_COLORS, CATEGORY_ICONS } from '@/lib/categories'

/** A name is usable if it already resolved, or the operator has mapped it. */
export function isSettled(status: ImportNameStatus, mapping: Record<string, string>): boolean {
  return status.resolved || Boolean(mapping[status.key])
}

/** The keys of every category/account that will resolve at commit time. */
function settledKeys(
  statuses: ImportNameStatus[],
  mapping: Record<string, string>,
): Set<string> {
  return new Set(statuses.filter((s) => isSettled(s, mapping)).map((s) => s.key))
}

export interface RowPlan {
  /** Rows that will be written. */
  importable: number
  /** Rows that will be skipped, counted once even when both ends are unmapped. */
  skipped: number
}

/**
 * Split the file's rows into what will land and what will not.
 *
 * Counted over (category, account) groups rather than by summing the two
 * unresolved lists, because a row whose category *and* account are both
 * unmapped is one skipped row, not two — summing the name counts reported more
 * skipped rows than the file contained.
 */
export function planRows(
  groups: ImportGroupCount[],
  categoryKeys: Set<string>,
  accountKeys: Set<string>,
): RowPlan {
  let importable = 0
  let skipped = 0
  for (const g of groups) {
    if (categoryKeys.has(g.categoryKey) && accountKeys.has(g.accountKey)) importable += g.count
    else skipped += g.count
  }
  return { importable, skipped }
}

/** planRows against a whole preview and the operator's current mappings. */
export function planImport(
  preview: ImportPreview,
  categoryMap: Record<string, string>,
  accountMap: Record<string, string>,
): RowPlan {
  return planRows(
    preview.groups,
    settledKeys(preview.categories, categoryMap),
    settledKeys(preview.accounts, accountMap),
  )
}

/** A category the bulk-create action would make, shown before anything is written. */
export interface PlannedCategory {
  key: string
  name: string
  kind: 'expense' | 'income'
  icon: string
  color: string
}

/** An account the bulk-create action would make. */
export interface PlannedAccount {
  key: string
  name: string
  currency: string
}

/**
 * Pick a stable icon and colour for a name.
 *
 * Deterministic on the name so the same export twice proposes the same
 * appearance, and so two people importing the same file end up with categories
 * that look alike — a random palette pick would make a re-run look like a
 * different suggestion and invite second-guessing.
 *
 * A *palette key* is chosen, never a hex: category rows store the key, so that
 * a re-theme does not have to rewrite synced rows on every device (CLAUDE.md).
 */
const ICON_NAMES = Object.keys(CATEGORY_ICONS)

function appearanceFor(name: string): { icon: string; color: string } {
  let hash = 0
  for (let i = 0; i < name.length; i++) hash = (hash * 31 + name.charCodeAt(i)) >>> 0
  return {
    icon: ICON_NAMES[hash % ICON_NAMES.length],
    color: CATEGORY_COLORS[hash % CATEGORY_COLORS.length],
  }
}

/**
 * What "create all missing" would create, for the confirmation list.
 *
 * Creation is still never automatic — this only describes it. The importer's
 * rule is that an unrecognised name is never coerced onto something else and
 * never invented behind the operator's back (docs/MONEFY-PARITY.md §5); a list
 * shown up front and written only on an explicit press keeps that, while not
 * making a twenty-category file twenty separate chores.
 */
export function plannedCategories(unresolved: ImportNameStatus[]): PlannedCategory[] {
  return unresolved.map((c) => ({
    key: c.key,
    name: c.name,
    kind: c.kind === 'income' ? 'income' : 'expense',
    ...appearanceFor(c.name),
  }))
}

export function plannedAccounts(
  unresolved: ImportNameStatus[],
  fallbackCurrency: string,
): PlannedAccount[] {
  return unresolved.map((a) => ({
    key: a.key,
    // An account named after its own currency ("EUR", "HUF") is how the
    // reference export writes them, and is the fallback when the server sent
    // no currency for this entry.
    name: a.name,
    currency: a.currency || (/^[A-Za-z]{3}$/.test(a.name) ? a.name.toUpperCase() : fallbackCurrency),
  }))
}

/**
 * The currencies in the file that this replica cannot yet convert.
 *
 * `declared` is the user's own currency setting and `convertible` is what the
 * rate cache can actually handle — both live on the device, so this needs no
 * request. A currency missing from either is why rows import fine and then sit
 * outside every total under "no exchange rate yet".
 */
export function undeclaredCurrencies(
  preview: ImportPreview,
  baseCurrency: string,
  declared: string[],
  convertible: (code: string) => boolean,
): string[] {
  const declaredSet = new Set(declared)
  return preview.currencies
    .map((c) => c.code)
    // The base currency is never converted to itself, so it can never be the
    // reason a row falls out of a total — checking it would report a problem
    // that does not exist on every single import.
    .filter((code) => code !== baseCurrency)
    .filter((code) => !declaredSet.has(code) || !convertible(code))
}
