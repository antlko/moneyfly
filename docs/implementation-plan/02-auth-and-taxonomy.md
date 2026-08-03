# Stage 02 — Auth & Taxonomy

> **Kickoff prompt**
> Implement stage 02 of the MoneyApp implementation plan. Read `docs/implementation-plan/00-conventions.md` and `02-auth-and-taxonomy.md`, plus `docs/03-data-model.md` §3.5, `docs/adr/0010-invite-only-auth.md` and `docs/04-import-monefy.md` §4.7 for the seeded aliases. Stage 01 is complete. Add auth, categories, accounts and aliases. No transactions yet — scope-out is binding.

## Goal

Log in and define the chart of accounts: categories with their essential flag, accounts with asset class and liquidity, and the alias tables that later prevent the 14% import loss.

## MVP demo

```bash
docker compose up -d
docker compose run --rm moneyapp migrate up
# Admin bootstrapped from config on first boot
```

Then in the browser: log in with the bootstrap admin → forced password change → see **18 seeded categories** with essential flags → create an account (`Cash EUR`, cash, EUR, liquid) → rename a category → archive one and watch it leave the list → log out → confirm the session is dead.

Useful on its own: this is the setup a new user must do anyway, and it is finished here.

## Scope

**In:** migrations 0002–0003, bcrypt, sessions, login/logout/me/password, admin bootstrap, CLI password reset, categories CRUD + archive + merge, accounts CRUD, both alias tables, seeded canonical data, auth middleware, login rate limiting, settings UI for categories and accounts.

**Out:** transactions, budgets, snapshots, import, FX, reports, bot. Parent-account *rollups* are stage 06; `parent_id` merely exists here.

## Contracts published here

```go
// internal/domain/auth
type Session struct{ UserID int64; TokenHash string; ExpiresAt time.Time }
type Service interface {
    Login(ctx, email, password string) (token string, err error)
    Authenticate(ctx, token string) (*User, error)
    Logout(ctx, token string) error
    ChangePassword(ctx, userID int64, old, new string) error // revokes other sessions
}

// internal/domain/category
type Category struct {
    ID int64; UserID int64; Name string
    Kind Kind            // "expense" | "income"
    IsEssential bool     // drives Possible Minimum, stage 05
    Icon, Color string; SortOrder int
    ParentID *int64; ArchivedAt *time.Time
}
type Resolver interface {
    // ResolveAlias is THE contract stage 04 depends on.
    // Returns (nil, nil) when unknown — caller must block, never auto-create.
    ResolveAlias(ctx context.Context, userID int64, source, sourceName string) (*Category, error)
}

// internal/domain/account
type Account struct {
    ID int64; UserID int64; Name string
    AssetClass AssetClass // cash|bank|deposit|investment|metal|crypto|other
    Currency string
    IsLiquid bool                 // Ready for usage, stage 06
    CountsTowardNetWorth bool     // General, stage 06
    PriceTicker *string           // XAU, USDT — stage 07
    CostBasis *money.Money        // Invested rows
    ParentID *int64
}
```

`ResolveAlias` returning `(nil, nil)` for unknown rather than an error is deliberate: "unknown" is an expected, actionable outcome in stage 04, not a failure.

## Tasks

**1. Migration 0002** — `category`, `category_alias`, exactly per [03-data-model.md](../03-data-model.md) §3.5.

> **As built:** the seed in tasks 2–4 is **not** written by these migrations. Those
> tables carry `user_id NOT NULL`, and at migration time no user exists to own the
> rows. The canonical taxonomy is installed when a user is created, from
> `internal/domain/seed` — which still means the bootstrap admin has all of it before
> the first login completes. Rationale and consequences in
> [03-data-model.md](../03-data-model.md) §3.7.

**2. Seed canonical categories.** The 18 from the workbook, with corrected spellings (`Appliances`, `Toiletry`), plus **`Utilities` and `Taxi`** — the two with no spreadsheet row that orphaned 120 transactions. Set `is_essential` from `B28`'s membership: House, Food, Eating out, Hobby, Clothes, Health, Transport, Sport, Bills, Services, Toiletry, Communications, Studying. Preserve the workbook's sort order.

**3. Seed aliases.** Every variant from [04-import-monefy.md](../04-import-monefy.md) §4.7: `HotelTrip`, `Communication`, `Clouth`, `Studing`, `Sport`/`Sports`, `Toiletry`/`Toilery`, `Applience`, `Family ` (trailing space), `Счета`. **This seeding is what makes the stage-04 gate pass with zero manual mapping**, so treat a missing alias as a bug here, not there.

**4. Migration 0003** — `account`, `account_alias`. Seed accounts from workbook rows 36–69 per [appendix-excel-parity.md](../appendix-excel-parity.md) §A.4, with correct `asset_class`, `is_liquid` and `counts_toward_net_worth`. Seed `Наличные` → `Cash UAH`, and `UAH`/`EUR`/`HUF` account-name aliases (Monefy names accounts after currencies).

> **As built, two facts the appendix leaves open:**
> `Cash UAH` has no workbook row at all — it exists because the Russian-locale
> exports post to `Наличные`, which is a UAH cash account, and because the export's
> `UAH` account has to resolve somewhere. `Banks FOP` is given currency **UAH**: FOP
> is a Ukrainian sole-trader account and row 46 is a bare literal, so the currency is
> inferred rather than stated.
> Structure and flags only are seeded. `cost_basis_minor` and every balance stay
> empty here; the workbook's figures arrive with `migrate-excel` in stage 10.

**5. Password hashing.** bcrypt cost from config, minimum 10 enforced at validation.

**6. Sessions.** 32 bytes from `crypto/rand`, stored as SHA-256. Cookie `HttpOnly; Secure; SameSite=Strict`, host-only. TTL from config (default 720h). Lookup by hash; expiry checked in SQL, not in Go.

**7. Auth middleware.** Resolves the session to a `*User` in the request context. **`userID` is available only from here** — the single source, per [00-conventions.md](00-conventions.md) §6.

**8. Auth endpoints.** `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `POST /auth/password`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}`. **No registration endpoint** ([adr/0010](../adr/0010-invite-only-auth.md)).

**9. Admin bootstrap.** On first boot, if no user exists and `bootstrap_admin_email` is set, create an admin from `MONEYAPP_BOOTSTRAP_PASSWORD` with `must_change_password`. Every endpoint except password-change returns 403 until it is changed.

**10. CLI reset.** `moneyapp reset-password --email` — no SMTP dependency, no reset-token surface.

**11. Login rate limiting.** Per-IP, exponential backoff, `429` with `Retry-After`. Per-account lockout is deliberately avoided — it is a denial-of-service lever against a known email.

**12. Category service + repo.** CRUD, archive (never hard delete), merge (`POST /categories/{id}/merge` — rewrites references and leaves an alias behind). Unique name per user among non-archived. `ResolveAlias` implemented here.

**13. Account service + repo.** CRUD. Reject a value written directly to an account that has children — parents are computed ([adr/0003](../adr/0003-snapshot-reconcile-balances.md)). Reject a `parent_id` cycle.

**14. Alias endpoints.** List, create, delete, for both categories and accounts.

**15. UI.** Login page with forced password change. Settings → Categories: list, create, edit, archive, drag-to-reorder, essential toggle, icon and colour pickers. Settings → Accounts: same shape plus asset class, currency, liquid and net-worth toggles. Both alias tables visible and editable.

**16. `openapi.yaml`.** Add every endpoint added here; spec must still validate.

## Tests

| Test | Asserts |
| --- | --- |
| `TestLogin_WrongPassword_NoUserEnumeration` | identical response and timing shape for unknown email vs bad password |
| `TestSession_ExpiredRejected` | expired token → 401 |
| `TestChangePassword_RevokesOtherSessions` | other tokens dead, current alive |
| `TestBootstrap_ForcesPasswordChange` | endpoints 403 until changed |
| `TestBootstrap_SkippedWhenUsersExist` | no second admin created |
| `TestRateLimit_LoginBackoff` | Nth attempt → 429 with `Retry-After` |
| `TestCategory_ArchiveKeepsHistory` | archived row still readable by ID |
| `TestCategory_UniqueNameAmongActive` | duplicate active name → 409; archived name reusable |
| `TestCategory_Merge_RewritesAndLeavesAlias` | alias created, references moved |
| `TestResolveAlias_KnownVariants` | all seeded variants resolve |
| `TestResolveAlias_Unknown_ReturnsNilNil` | **no error, no auto-create** |
| `TestSeed_CategoryCount` | 22 categories: 20 expense incl. `Utilities` and `Taxi`, plus the 2 income categories stage 05 added |
| `TestSeed_EssentialFlags` | exactly the 13 from `B28` |
| `TestSeed_IsPerUser` | two users get independent copies of the taxonomy |
| `TestAccount_ParentRejectsDirectValue` | 422 |
| `TestAccount_ParentCycleRejected` | 422 |
| `TestNoUnscopedQueries` | still passes — now with real store code |
| `TestUserIsolation` | user A cannot read or mutate user B's rows, every endpoint |

`TestUserIsolation` is the stage's most important test. With no encryption ([adr/0002](../adr/0002-no-encryption.md)), scoping is the *only* thing separating users, so it is asserted per endpoint from the first stage that has data.

## Verification

```bash
make verify
docker compose up -d && docker compose run --rm moneyapp migrate up
# login as bootstrap admin
curl -s -c j -X POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"'"$MONEYAPP_BOOTSTRAP_PASSWORD"'"}' -i | head -1
curl -s -b j localhost:8080/api/v1/categories | jq 'length'          # 20
curl -s -b j localhost:8080/api/v1/categories | jq '[.[]|select(.is_essential)]|length'  # 13
curl -s localhost:8080/api/v1/categories -i | head -1                # 401 without cookie
```

## Done checklist

- [ ] `make verify` green; stage 01 demo still works
- [ ] Bootstrap admin works once and forces a password change
- [ ] No registration endpoint exists
- [ ] 20 expense categories seeded, 13 essential, `Utilities` and `Taxi` present (stage 05 adds 2 income categories, taking the total to 22)
- [ ] Every alias variant from the real export resolves
- [ ] Unknown alias returns `(nil, nil)` — never auto-creates
- [ ] Accounts seeded with correct liquid / net-worth flags
- [ ] Parent accounts reject direct values
- [ ] `TestUserIsolation` covers every endpoint
- [ ] `openapi.yaml` updated and valid
