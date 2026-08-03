/**
 * Theme resolution: dark follows the system, with a manual override that persists.
 *
 * The resolved value is written to `data-theme` on <html>, which is the single
 * signal every `dark:` utility keys off (see style.css).
 */

export type ThemeChoice = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'moneyapp.theme'

let media: MediaQueryList | null = null

export function initTheme(): void {
  apply(readChoice())
  media = window.matchMedia?.('(prefers-color-scheme: dark)') ?? null
  media?.addEventListener('change', () => {
    if (readChoice() === 'system') apply('system')
  })
}

export function readChoice(): ThemeChoice {
  const stored = localStorage.getItem(STORAGE_KEY)
  return stored === 'light' || stored === 'dark' ? stored : 'system'
}

export function setChoice(choice: ThemeChoice): void {
  if (choice === 'system') localStorage.removeItem(STORAGE_KEY)
  else localStorage.setItem(STORAGE_KEY, choice)
  apply(choice)
}

function apply(choice: ThemeChoice): void {
  const prefersDark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
  const resolved = choice === 'system' ? (prefersDark ? 'dark' : 'light') : choice
  document.documentElement.dataset.theme = resolved
}
