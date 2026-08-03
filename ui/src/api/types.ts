/**
 * Domain types used by the stores.
 *
 * The generated `schema.d.ts` (npm run gen:api) is the source of truth for the
 * request and response shapes; these aliases give the stores stable names and are
 * checked against the generated schema in api/schema.check.ts, so a drift between
 * the spec and the client is a build failure rather than a runtime surprise.
 */

import type { BaseMoney, Money } from '@/lib/money'

export type { BaseMoney, Money }

export type BudgetState =
  'severely_over' | 'over' | 'approaching' | 'within' | 'zero' | 'not_recorded'

export interface User {
  id: number
  email: string
  display_name: string | null
  role: 'admin' | 'user' | 'readonly'
  base_currency: string
  timezone: string
  fiscal_year_start_month: number
  must_change_password: boolean
}

export interface Category {
  id: number
  name: string
  kind: 'expense' | 'income'
  is_essential: boolean
  icon: string | null
  color: string | null
  sort_order: number
  parent_id: number | null
  archived_at: string | null
}

export interface CategoryInput {
  name: string
  kind: 'expense' | 'income'
  is_essential?: boolean
  icon?: string
  color?: string
  sort_order?: number
  parent_id?: number | null
}

export type AssetClass = 'cash' | 'bank' | 'deposit' | 'investment' | 'metal' | 'crypto' | 'other'

export interface Account {
  id: number
  name: string
  asset_class: AssetClass
  currency: string
  is_liquid: boolean
  counts_toward_net_worth: boolean
  price_ticker: string | null
  cost_basis: Money | null
  parent_id: number | null
  sort_order: number
  is_computed: boolean
  archived_at: string | null
}

export interface AccountInput {
  name: string
  asset_class: AssetClass
  currency: string
  is_liquid?: boolean
  counts_toward_net_worth?: boolean
  price_ticker?: string
  cost_basis_minor?: number | null
  parent_id?: number | null
  sort_order?: number
}

export interface Alias {
  id: number
  source: string
  source_name: string
  target_id: number
  target_name: string
}

export interface Currency {
  code: string
  exponent: number
  symbol: string
  name: string
}

export interface Transaction {
  id: number
  account_id: number
  account_name: string
  category_id: number | null
  category_name?: string
  occurred_on: string
  kind: 'expense' | 'income' | 'transfer_in' | 'transfer_out'
  amount: Money
  base_amount: BaseMoney | null
  description: string | null
  merchant: string | null
  occurrence: number
  unconverted: boolean
  transfer_group_id: number | null
  import_batch_id: number | null
}

export interface TransactionInput {
  account_id: number
  category_id?: number | null
  occurred_on: string
  kind: 'expense' | 'income'
  amount: Money
  description?: string | null
  merchant?: string | null
}

export interface TransactionPage {
  items: Transaction[]
  next_cursor: string | null
  has_more: boolean
}

export interface Budget {
  category_id: number
  category_name: string
  period: string
  planned: Money
}

export interface BudgetReportRow {
  category_id: number
  name: string
  icon: string | null
  color: string | null
  is_essential: boolean
  /** null means the month recorded nothing at all — never zero. */
  actual: Money | null
  /** null means no plan was recorded, which differs from a plan of zero. */
  planned: Money | null
  ratio: number | null
  state: BudgetState
  state_label: string
}

export interface BudgetReport {
  period: string
  valuation: string
  base_currency: string
  spend_total: Money | null
  planned_total: Money
  income: Money | null
  diff: Money | null
  saved_percent: number | null
  possible_minimum: Money
  /** Rows whose base figure is missing because no rate covered their date. */
  unconverted: number
  categories: BudgetReportRow[]
}

export interface BuildInfo {
  version: string
  commit: string
  built_at?: string
}

export type ImportStatus =
  'received' | 'parsed' | 'needs_mapping' | 'previewed' | 'committed' | 'reverted' | 'failed'

export type ImportRowStatus = 'new' | 'duplicate' | 'unmapped' | 'rejected' | 'committed'

export interface ImportBatch {
  id: number
  source: string
  origin: 'web' | 'telegram' | 'cli'
  filename: string
  file_sha256: string
  status: ImportStatus
  rows_total: number
  rows_new: number
  rows_duplicate: number
  rows_unmapped: number
  rows_rejected: number
  error?: string
  created_at: string
  committed_at: string | null
  reverted_at: string | null
  /** The identical file had already been committed; nothing was reprocessed. */
  already_imported?: boolean
}

/** A proposal, never an application: confirming it is what creates the alias. */
export interface ImportSuggestion {
  id: number
  name: string
  confidence: 'high' | 'medium'
}

export interface ImportUnmappedName {
  source_name: string
  row_count: number
  reason?: string
  suggestion: ImportSuggestion | null
}

export interface ImportVanishedRow {
  transaction_id: number
  occurred_on: string
  account_name: string
  category_name: string
  amount: Money
  description?: string
}

export interface ImportRow {
  id: number
  line_no: number
  raw_line: string
  occurred_on: string | null
  account_name?: string
  category_name?: string
  amount: Money | null
  description: string | null
  status: ImportRowStatus
  reason?: string
  transaction_id: number | null
}

export interface ImportPreview {
  batch_id: number
  filename: string
  status: ImportStatus
  origin: 'web' | 'telegram' | 'cli'
  date_range: { from: string; to: string } | null
  rows_total: number
  rows_new: number
  rows_duplicate: number
  rows_unmapped: number
  rows_rejected: number
  months_touched: number
  unmapped_categories: ImportUnmappedName[]
  unmapped_accounts: ImportUnmappedName[]
  vanished: ImportVanishedRow[]
  rejected: ImportRow[]
  by_currency: Record<string, number>
  warnings: string[]
  committed_at: string | null
  reverted_at: string | null
}

export interface ImportMapping {
  source_name: string
  target_id: number
}

export interface PeriodSummary {
  period: string
  /** false means the month recorded nothing at all; every money field is then null. */
  recorded: boolean
  spend_total: Money | null
  planned_total: Money
  possible_minimum: Money
  income: Money | null
  diff: Money | null
  /** Unclamped ratio. Null when income is zero. */
  saved_percent: number | null
  unconverted: number
}

export interface ReportSummary {
  from: string
  to: string
  base_currency: string
  fiscal_year: string
  periods: PeriodSummary[]
}

export interface CategoryCell {
  period: string
  /** Null for an unrecorded month. Rendered blank — never 0, never -1. */
  actual: Money | null
  planned: Money | null
  ratio: number | null
  state: BudgetState
  state_label: string
}

export interface CategorySeries {
  category_id: number
  name: string
  icon: string | null
  color: string | null
  is_essential: boolean
  archived: boolean
  /** Column C: the mean over recorded months only. */
  average: Money | null
  /** Column T: the sum over recorded months, without the sheet's -1 sentinel. */
  total: Money | null
  cells: CategoryCell[]
}

export interface ReportCategories {
  from: string
  to: string
  base_currency: string
  periods: string[]
  recorded: Record<string, boolean>
  categories: CategorySeries[]
  summary: PeriodSummary[]
}

export type BurnMode =
  'essential_planned' | 'actual_trailing_3' | 'actual_trailing_12' | 'legacy_blend'

export interface SnapshotRow {
  account_id: number
  account_name: string
  asset_class: AssetClass
  currency: string
  period: string
  amount: Money | null
  quantity_nano: number | null
  base_amount: Money | null
  /** Last month's figure, so entry is confirm-or-adjust rather than retype. */
  previous_amount: Money | null
  is_liquid: boolean
  counts_toward_net_worth: boolean
  /** A computed parent: its value is the sum of its children and cannot be written. */
  computed: boolean
  note?: string
  value: Money | null
}

export interface AllocationShare {
  account_id: number
  name: string
  asset_class: AssetClass
  value: Money
  /** Fraction of net worth. Every slice is a leaf, converted first, so these sum to 1. */
  share: number
}

export interface CapitalChange {
  recorded: boolean
  total: Money | null
  /** Money actually saved or spent. */
  real: Money | null
  /** What the currencies did. */
  fx: Money | null
}

export interface CapitalReport {
  period: string
  base_currency: string
  general: Money | null
  ready_for_usage: Money | null
  runway_months: number | null
  burn_mode: BurnMode
  burn_rate: Money | null
  change: CapitalChange
  allocation: AllocationShare[]
  accounts: SnapshotRow[]
}

export interface CapitalSeriesPoint {
  period: string
  recorded: boolean
  general: Money | null
  ready_for_usage: Money | null
  runway_months: number | null
  change: CapitalChange
}

export interface CapitalSeries {
  from: string
  to: string
  base_currency: string
  burn_mode: BurnMode
  points: CapitalSeriesPoint[]
}

export interface DriftRow {
  account_id: number
  name: string
  snapshot: Money | null
  implied: Money | null
  /** snapshot − implied. The snapshot always wins; this is informational. */
  difference: Money | null
}

export type SettingMode = 'manual' | 'auto'

export interface Setting {
  key: string
  mode: SettingMode
  /** The user's own figure. It wins whenever present. */
  manual_value: string | null
  /** What the provider last returned. Kept even when overridden, so both show. */
  last_value: string | null
  effective_value: string | null
  overridden: boolean
  provider_key: string | null
  last_fetched_at: string | null
  last_error: string | null
  refresh_seconds: number
  stale: boolean
  updated_at: string
}

export interface RateProvider {
  key: string
  kind: string
  endpoint: string
  priority: number
  enabled: boolean
}

export interface TelegramLink {
  id: number
  chat_id: number | null
  /** Only present when the link is minted; it is a credential for ten minutes. */
  code?: string
  expires_at: string
  linked_at: string | null
  revoked_at: string | null
  live: boolean
  created_at: string
}

export interface TelegramLinkCode {
  code: string
  expires_at: string
  instructions: string
}
