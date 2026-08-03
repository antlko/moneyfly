import { readonly, ref } from 'vue'

/**
 * Online state, and the rule that follows from it.
 *
 * A save button that appears to work and silently does not is worse than one that
 * is visibly unavailable, so mutations are **disabled** while offline rather than
 * queued. There is no write queue by decision
 * (docs/adr/0012-defer-offline-entry.md).
 */
const online = ref(typeof navigator === 'undefined' ? true : navigator.onLine)

if (typeof window !== 'undefined') {
  window.addEventListener('online', () => (online.value = true))
  window.addEventListener('offline', () => (online.value = false))
}

export function useOnline() {
  return { online: readonly(online) }
}

/** Registers the shell-only service worker. Failure is silent: it is an enhancement. */
export function registerServiceWorker(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  if (import.meta.env.DEV) return
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => undefined)
  })
}

/** True when the app is running from the home screen rather than in a browser tab. */
export function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const iosStandalone = (window.navigator as { standalone?: boolean }).standalone === true
  return iosStandalone || window.matchMedia('(display-mode: standalone)').matches
}

/**
 * iOS Safari has no install prompt: the user must use Share → Add to Home Screen.
 * The hint is shown there and nowhere else, because everywhere else it would be
 * wrong.
 */
export function isIOSSafari(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  const iOS = /iPad|iPhone|iPod/.test(ua) || (ua.includes('Macintosh') && 'ontouchend' in document)
  const webkit = /WebKit/.test(ua)
  const otherBrowser = /CriOS|FxiOS|EdgiOS|OPiOS/.test(ua)
  return iOS && webkit && !otherBrowser
}
