import type { RecurringFreq } from './period'

/** In the order the "make recurring" sheet offers them. */
export const RECURRING_FREQS: RecurringFreq[] = ['daily', 'weekly', 'monthly', 'yearly']

export function freqLabel(freq: unknown): string {
  switch (freq) {
    case 'daily':
      return 'Every day'
    case 'weekly':
      return 'Every week'
    case 'monthly':
      return 'Every month'
    case 'yearly':
      return 'Every year'
    default:
      return String(freq ?? '')
  }
}
