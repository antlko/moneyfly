import { uuidv7 } from './uuid'

const KEY = 'moneyfly.deviceId'

/**
 * This browser profile's stable device id.
 *
 * It lives in localStorage rather than IndexedDB deliberately. It is needed
 * synchronously (sign-in sends it), and it must outlive the replica: if Safari
 * evicts IndexedDB, the device comes back with the *same* identity and
 * re-bootstraps, instead of appearing as a brand new device every eviction.
 */
export function deviceId(): string {
  let id = localStorage.getItem(KEY)
  if (!id) {
    id = uuidv7()
    localStorage.setItem(KEY, id)
  }
  return id
}

/** A rough platform label, shown on the devices screen so a row is recognisable. */
export function platform(): string {
  const ua = navigator.userAgent
  const standalone = window.matchMedia('(display-mode: standalone)').matches

  let os = 'Web'
  if (/iPhone|iPad|iPod/.test(ua)) os = 'iOS'
  else if (/Android/.test(ua)) os = 'Android'
  else if (/Macintosh/.test(ua)) os = 'macOS'
  else if (/Windows/.test(ua)) os = 'Windows'
  else if (/Linux/.test(ua)) os = 'Linux'

  return standalone ? `${os} (installed)` : os
}
