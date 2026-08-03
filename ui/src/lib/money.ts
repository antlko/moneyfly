/**
 * Money on the client.
 *
 * Money is never a JS number in logic. The API sends `{amount_minor, currency,
 * exponent}` and this module renders it; arithmetic is the server's job
 * (docs/implementation-plan/00-conventions.md §10). A float here would reintroduce
 * exactly the drift the integer minor units exist to prevent.
 */

export interface Money {
  amount_minor: number
  currency: string
  exponent: number
}

export interface BaseMoney extends Money {
  fx_rate_id: number | null
  as_of_date: string | null
}

const symbols: Record<string, string> = {
  EUR: '€',
  USD: '$',
  HUF: 'Ft',
  UAH: '₴',
}

/**
 * Formats an amount for display, grouped by locale and with the currency's own
 * number of decimals — HUF shows none, so 4,562,000 Ft is readable at a glance.
 */
export function formatMoney(m: Money | null | undefined, opts: { symbol?: boolean } = {}): string {
  if (!m) return '—'
  const showSymbol = opts.symbol !== false
  const negative = m.amount_minor < 0
  const magnitude = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: m.exponent,
    maximumFractionDigits: m.exponent,
  }).format(Math.abs(toDisplayNumber(m)))

  if (!showSymbol) return negative ? `-${magnitude}` : magnitude
  const symbol = symbols[m.currency] ?? m.currency
  // The sign goes outside the symbol: -€3,750.00, not €-3,750.00.
  const withSymbol = m.currency === 'HUF' ? `${magnitude} ${symbol}` : `${symbol}${magnitude}`
  return negative ? `-${withSymbol}` : withSymbol
}

/**
 * Converts minor units to a display number.
 *
 * Only ever used for formatting and for chart geometry — never as an input to a
 * figure the user is shown as authoritative.
 */
export function toDisplayNumber(m: Money): number {
  return m.amount_minor / 10 ** m.exponent
}

/** Renders a ratio as a percentage, or an em dash when it is not recorded. */
export function formatPercent(ratio: number | null | undefined, digits = 0): string {
  if (ratio === null || ratio === undefined) return '—'
  return `${(ratio * 100).toFixed(digits)}%`
}

/**
 * Parses keypad input into minor units for a currency.
 *
 * Returns null for anything not exactly representable, which the entry screen
 * shows as an invalid amount rather than rounding silently.
 */
export function parseAmount(input: string, exponent: number): number | null {
  const trimmed = input.trim()
  if (trimmed === '' || trimmed === '.') return null
  if (!/^\d*(\.\d*)?$/.test(trimmed)) return null

  const [wholePart = '', fracPart = ''] = trimmed.split('.')
  if (fracPart.length > exponent) {
    // More precision than the currency can hold: 10.50 HUF is not a thing.
    if (fracPart.slice(exponent).replace(/0/g, '') !== '') return null
  }
  const whole = wholePart === '' ? 0 : Number.parseInt(wholePart, 10)
  const padded = fracPart.slice(0, exponent).padEnd(exponent, '0')
  const frac = padded === '' ? 0 : Number.parseInt(padded, 10)
  const minor = whole * 10 ** exponent + frac
  if (!Number.isSafeInteger(minor)) return null
  return minor
}

/** Builds a Money for sending to the API. */
export function money(amountMinor: number, currency: string, exponent: number): Money {
  return { amount_minor: amountMinor, currency, exponent }
}

/** The label shown when a figure exists but could not be converted to base currency. */
export const UNCONVERTED_LABEL = 'no rate for this date'
