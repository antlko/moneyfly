/**
 * Money is an integer number of minor units plus a currency code. Never a float:
 * 0.1 + 0.2 is not 0.3, and a budgeting app that quietly loses a cent per
 * transaction is worse than useless.
 *
 * The number of minor units per major unit is **per currency**. Assuming two is
 * a real bug, not a theoretical one — a genuine Monefy export in this project's
 * test data contains HUF, which has none.
 */

/** Currencies whose minor unit is not 1/100. Everything else is 2. */
const EXPONENTS: Record<string, number> = {
  BIF: 0,
  CLP: 0,
  DJF: 0,
  GNF: 0,
  ISK: 0,
  JPY: 0,
  KMF: 0,
  KRW: 0,
  PYG: 0,
  RWF: 0,
  UGX: 0,
  UYI: 0,
  VND: 0,
  VUV: 0,
  XAF: 0,
  XOF: 0,
  XPF: 0,
  HUF: 0, // Monefy exports it; formally 2, but nobody uses fillér
  BHD: 3,
  IQD: 3,
  JOD: 3,
  KWD: 3,
  LYD: 3,
  OMR: 3,
  TND: 3,
}

/** How many decimal places this currency's minor unit implies. */
export function exponent(currency: string): number {
  return EXPONENTS[currency.toUpperCase()] ?? 2
}

/**
 * Shift a number by `places` decimal digits without going through a
 * multiplication.
 *
 * `1.005 * 100` is `100.49999999999999`, because 1.005 is not representable in
 * binary — so rounding it gives 100 cents where a person means 101. Shifting the
 * exponent of the *decimal* representation instead sidesteps that entirely:
 * `toExponential()` yields the shortest decimal string that round-trips, and
 * moving its exponent is exact.
 */
function shiftDecimal(value: number, places: number): number {
  const [mantissa, exp = '0'] = value.toExponential().split('e')
  return Number(`${mantissa}e${Number(exp) + places}`)
}

/** 14.4 EUR → 1440. Rounds half away from zero, like a cash register. */
export function toMinor(major: number, currency: string): number {
  const shifted = shiftDecimal(Math.abs(major), exponent(currency))
  return Math.sign(major) * Math.round(shifted)
}

/** 1440 EUR → 14.4. Lossy by design — only for display and charts. */
export function toMajor(minor: number, currency: string): number {
  return minor / 10 ** exponent(currency)
}

export interface MoneyParts {
  /** '-' when negative, otherwise ''. */
  sign: string
  symbol: string
  /** The integer part, with grouping. */
  whole: string
  /** The fractional part *without* its separator, or '' for a 0-decimal currency. */
  fraction: string
  separator: string
}

/**
 * Split an amount into the pieces the UI renders at different sizes — Monefy
 * shows `-€317.` large and `03` small, and that only works if the parts are
 * separate.
 *
 * Formatting goes through Intl so symbols, grouping and separators are right for
 * the viewer's locale rather than hardcoded to euros.
 */
export function splitMoney(minor: number, currency: string, locale?: string): MoneyParts {
  const digits = exponent(currency)
  const negative = minor < 0
  const value = Math.abs(minor) / 10 ** digits

  let formatted: string
  try {
    formatted = new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    }).format(value)
  } catch {
    // An unknown or malformed code must not blank out the screen.
    formatted = `${currency} ${value.toFixed(digits)}`
  }

  // Pull the symbol off the front or back, whichever the locale put it at.
  const numeric = formatted.replace(/[^\d.,\s '’]/g, '').trim()
  const symbol = formatted.replace(numeric, '').replace(/[\s ]/g, '').trim()

  if (digits === 0) {
    return { sign: negative ? '-' : '', symbol, whole: numeric, fraction: '', separator: '' }
  }

  // The decimal separator is the last non-digit in the numeric part; grouping
  // separators come earlier, so scanning from the end is what makes this work
  // for both 1.234,56 and 1,234.56.
  const cut = Math.max(numeric.lastIndexOf('.'), numeric.lastIndexOf(','))
  if (cut < 0) {
    return { sign: negative ? '-' : '', symbol, whole: numeric, fraction: '', separator: '' }
  }
  return {
    sign: negative ? '-' : '',
    symbol,
    whole: numeric.slice(0, cut),
    separator: numeric.slice(cut, cut + 1),
    fraction: numeric.slice(cut + 1),
  }
}

/** The same amount as one string. */
export function formatMoney(minor: number, currency: string, locale?: string): string {
  const p = splitMoney(minor, currency, locale)
  return `${p.sign}${p.symbol}${p.whole}${p.separator}${p.fraction}`
}
