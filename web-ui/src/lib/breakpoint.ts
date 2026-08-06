import { useMediaQuery } from '@vueuse/core'

/**
 * Where the app stops being the Monefy phone frame and becomes the desktop
 * dashboard — see docs/DEVELOPMENT.md "Checking against the Monefy
 * reference". 640px, not a conventional "tablet vs desktop" width: this is
 * the point the *fixed single-screen app frame* stops making sense, which is
 * a narrower question than "does a sidebar fit comfortably." Tailwind's own
 * `sm` breakpoint, so the shell-level media query in tailwind.css and this
 * composable are reading the same number without importing from each other.
 */
export const DESKTOP_BREAKPOINT = 640

/**
 * Reactive: true at and above DESKTOP_BREAKPOINT.
 *
 * This is the one switch "the shell component picks the mobile Monefy frame
 * or the desktop dashboard" (CLAUDE.md) is built from — every place that
 * differs between the two form factors reads this rather than duplicating
 * its own width check, so there is exactly one definition of "desktop" in
 * the app.
 */
export function useIsDesktop() {
  return useMediaQuery(`(min-width: ${DESKTOP_BREAKPOINT}px)`)
}
