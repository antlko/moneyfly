/**
 * Period helpers. A period is a calendar month, "YYYY-MM" — the same unit the API
 * uses, so no conversion happens at the boundary.
 */

export function currentPeriod(now = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

export function shiftPeriod(period: string, months: number): string {
  const [year, month] = period.split('-').map(Number)
  const date = new Date(Date.UTC(year, month - 1 + months, 1))
  return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`
}

/** Renders a period as a month name, so a chart axis is never a bare index. */
export function formatPeriod(period: string): string {
  const [year, month] = period.split('-').map(Number)
  const date = new Date(Date.UTC(year, month - 1, 1))
  return date.toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
}

export function periodRange(period: string): { from: string; to: string } {
  const [year, month] = period.split('-').map(Number)
  const last = new Date(Date.UTC(year, month, 0)).getUTCDate()
  return {
    from: `${period}-01`,
    to: `${period}-${String(last).padStart(2, '0')}`,
  }
}

export function today(now = new Date()): string {
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

/** Formats an ISO date for display. */
export function formatDate(iso: string): string {
  const [year, month, day] = iso.split('-').map(Number)
  return new Date(Date.UTC(year, month - 1, day)).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    timeZone: 'UTC',
  })
}
