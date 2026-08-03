/**
 * Compile-time checks that the hand-written store types still match the generated
 * OpenAPI schema.
 *
 * `npm run gen:api` regenerates `schema.d.ts` from docs/openapi.yaml. This file
 * turns a drift between the spec and the client into a type error at build time
 * rather than a runtime surprise — which is what "API types are generated from
 * openapi.yaml, never hand-written" is there to prevent
 * (docs/implementation-plan/00-conventions.md §10).
 *
 * It emits nothing at runtime.
 */

import type { components } from './schema'
import type { Account, Category, Money, Transaction, User } from './types'

/** Fails to compile unless T is assignable to U and U to T on the listed keys. */
type MustMatch<T extends U, U> = T

type SchemaMoney = components['schemas']['Money']
type SchemaCategory = components['schemas']['Category']
type SchemaAccount = components['schemas']['Account']
type SchemaTransaction = components['schemas']['Transaction']
type SchemaUser = components['schemas']['User']

// Money is the one shape every screen depends on: integer minor units plus the
// exponent needed to render them.
export type _Money = MustMatch<Money, Required<SchemaMoney>>

// For the rest, the spec marks most properties optional (OpenAPI has no notion of
// "always present in a response"), so the check runs in the direction that
// matters: every field the client reads must exist in the spec.
export type _Category = MustMatch<Pick<SchemaCategory, keyof SchemaCategory>, Partial<Category>>
export type _Account = MustMatch<Pick<SchemaAccount, keyof SchemaAccount>, Partial<Account>>
export type _Transaction = MustMatch<
  Pick<SchemaTransaction, 'id' | 'account_id' | 'occurred_on' | 'kind'>,
  Partial<Pick<Transaction, 'id' | 'account_id' | 'occurred_on' | 'kind'>>
>
export type _User = MustMatch<Pick<SchemaUser, keyof SchemaUser>, Partial<User>>
