import 'fake-indexeddb/auto'

import { IDBFactory } from 'fake-indexeddb'
import { reactive } from 'vue'
import { beforeEach, describe, expect, it } from 'vitest'

import { META_PROFILE, MoneyflyDB } from './index'

/**
 * Regression cover for the write that broke registration.
 *
 * `auth.createAccount` stored `user.value` — a deep `ref`, so the getter hands
 * back a reactive Proxy — and IndexedDB's structured clone rejected it. The
 * throw landed after the server had already created the account, so the form
 * showed "could not be cloned" for an account that existed.
 */
describe('MoneyflyDB.setMeta', () => {
  let db: MoneyflyDB

  beforeEach(async () => {
    db = new MoneyflyDB(`meta-${Math.random()}`, new IDBFactory())
    await db.open()
  })

  it('stores a reactive object', async () => {
    const profile = reactive({ id: 'u1', email: 'a@b.c', baseCurrency: 'EUR', isAdmin: true })

    await expect(db.setMeta(META_PROFILE, profile)).resolves.toBeUndefined()
    await expect(db.getMeta(META_PROFILE, null)).resolves.toEqual({
      id: 'u1',
      email: 'a@b.c',
      baseCurrency: 'EUR',
      isAdmin: true,
    })
  })

  it('stores null, which is how signing out clears the cached profile', async () => {
    await db.setMeta(META_PROFILE, { id: 'u1' })
    await db.setMeta(META_PROFILE, null)

    await expect(db.getMeta(META_PROFILE, 'fallback')).resolves.toBe(null)
  })

  it('stores a reactive array, the shape the account filter holds', async () => {
    const filter = reactive(['a1', 'a2'])

    await db.setMeta('filter.accounts', filter)

    await expect(db.getMeta('filter.accounts', [])).resolves.toEqual(['a1', 'a2'])
  })
})
