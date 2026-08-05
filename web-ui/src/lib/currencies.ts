/**
 * The currencies offered when adding one.
 *
 * A shortlist, not ISO 4217 in full: a picker of 180 codes is a worse way to
 * find "HUF" than a search box over the ones people actually keep money in. The
 * search accepts any three-letter code anyway, so nothing here is a limit — this
 * is only what is suggested.
 *
 * Names are English and untranslated. They exist to disambiguate `RON` from
 * `RSD` in a list, not to be read as prose.
 */
export interface CurrencyOption {
  code: string
  name: string
}

export const CURRENCY_OPTIONS: CurrencyOption[] = [
  { code: 'EUR', name: 'Euro' },
  { code: 'USD', name: 'US dollar' },
  { code: 'GBP', name: 'Pound sterling' },
  { code: 'CHF', name: 'Swiss franc' },
  { code: 'JPY', name: 'Japanese yen' },
  { code: 'CNY', name: 'Chinese yuan' },
  { code: 'AUD', name: 'Australian dollar' },
  { code: 'CAD', name: 'Canadian dollar' },
  { code: 'NZD', name: 'New Zealand dollar' },
  { code: 'SEK', name: 'Swedish krona' },
  { code: 'NOK', name: 'Norwegian krone' },
  { code: 'DKK', name: 'Danish krone' },
  { code: 'ISK', name: 'Icelandic króna' },
  { code: 'PLN', name: 'Polish złoty' },
  { code: 'CZK', name: 'Czech koruna' },
  { code: 'HUF', name: 'Hungarian forint' },
  { code: 'RON', name: 'Romanian leu' },
  { code: 'BGN', name: 'Bulgarian lev' },
  { code: 'RSD', name: 'Serbian dinar' },
  { code: 'UAH', name: 'Ukrainian hryvnia' },
  { code: 'MDL', name: 'Moldovan leu' },
  { code: 'GEL', name: 'Georgian lari' },
  { code: 'TRY', name: 'Turkish lira' },
  { code: 'ILS', name: 'Israeli shekel' },
  { code: 'AED', name: 'UAE dirham' },
  { code: 'SAR', name: 'Saudi riyal' },
  { code: 'EGP', name: 'Egyptian pound' },
  { code: 'ZAR', name: 'South African rand' },
  { code: 'INR', name: 'Indian rupee' },
  { code: 'IDR', name: 'Indonesian rupiah' },
  { code: 'THB', name: 'Thai baht' },
  { code: 'VND', name: 'Vietnamese dong' },
  { code: 'PHP', name: 'Philippine peso' },
  { code: 'MYR', name: 'Malaysian ringgit' },
  { code: 'SGD', name: 'Singapore dollar' },
  { code: 'HKD', name: 'Hong Kong dollar' },
  { code: 'KRW', name: 'South Korean won' },
  { code: 'TWD', name: 'New Taiwan dollar' },
  { code: 'BRL', name: 'Brazilian real' },
  { code: 'ARS', name: 'Argentine peso' },
  { code: 'CLP', name: 'Chilean peso' },
  { code: 'COP', name: 'Colombian peso' },
  { code: 'MXN', name: 'Mexican peso' },
  { code: 'KZT', name: 'Kazakhstani tenge' },
  { code: 'MAD', name: 'Moroccan dirham' },
  { code: 'NGN', name: 'Nigerian naira' },
  { code: 'KES', name: 'Kenyan shilling' },
]

const BY_CODE = new Map(CURRENCY_OPTIONS.map((c) => [c.code, c]))

/** The English name for a code, or the code itself when it is not on the list. */
export const currencyName = (code: string) => BY_CODE.get(code.toUpperCase())?.name ?? code

/** Search by code or name; an exact three-letter code always matches itself. */
export function searchCurrencies(query: string): CurrencyOption[] {
  const q = query.trim().toUpperCase()
  if (!q) return CURRENCY_OPTIONS
  const hits = CURRENCY_OPTIONS.filter(
    (c) => c.code.includes(q) || c.name.toUpperCase().includes(q),
  )
  // A valid code nobody listed is still a currency the server may have a rate
  // for, so offer it rather than insisting the list is exhaustive.
  if (/^[A-Z]{3}$/.test(q) && !hits.some((c) => c.code === q)) {
    return [{ code: q, name: q }, ...hits]
  }
  return hits
}
