import { createRouter, createWebHistory } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useNotifyStore } from '@/stores/notify'
import DashboardView from '@/views/DashboardView.vue'

/**
 * There is one route tree for both form factors. The shell component decides
 * whether to render the Monefy mobile frame or the desktop dashboard, so
 * resizing the window never changes the URL and a link works on either device.
 */
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: DashboardView },
    {
      // `kind` is a route param rather than two components: the screen is
      // identical apart from the sign and which category list it shows.
      path: '/new/:kind(expense|income)',
      name: 'record',
      component: () => import('@/views/RecordView.vue'),
      props: true,
    },
    {
      // Editing reuses the record screen: the fields are the same, and sending
      // someone somewhere that looks different to fix a typo is disorienting.
      path: '/edit/:id',
      name: 'edit-record',
      component: () => import('@/views/RecordView.vue'),
      props: true,
    },
    {
      path: '/accounts',
      name: 'accounts',
      component: () => import('@/views/AccountsView.vue'),
    },
    {
      path: '/transfer',
      name: 'transfer',
      component: () => import('@/views/TransferView.vue'),
    },
    {
      path: '/search',
      name: 'search',
      component: () => import('@/views/SearchView.vue'),
    },
    {
      path: '/account',
      name: 'account',
      component: () => import('@/views/AccountView.vue'),
    },
    {
      path: '/currencies',
      name: 'currencies',
      component: () => import('@/views/CurrenciesView.vue'),
    },
    {
      path: '/recurring',
      name: 'recurring',
      component: () => import('@/views/RecurringView.vue'),
    },
    {
      path: '/budgets',
      name: 'budgets',
      component: () => import('@/views/BudgetsView.vue'),
    },
    {
      path: '/import',
      name: 'import',
      component: () => import('@/views/ImportView.vue'),
    },
    {
      path: '/integrations',
      name: 'integrations',
      component: () => import('@/views/IntegrationsView.vue'),
    },
    {
      // Root manages the others. Guarded twice: the router below sends a
      // non-admin to `/` before this ever renders, and the API refuses the
      // requests regardless (adminMW) — the route guard is for a clean
      // redirect, not the actual boundary.
      path: '/admin/users',
      name: 'admin-users',
      component: () => import('@/views/UsersView.vue'),
      meta: { adminOnly: true },
    },
    {
      // Same guarding as /admin/users. Deliberately not everything in
      // config.yaml — see InstanceSettingsView.vue's own doc comment for
      // what stays file/env-only and why.
      path: '/admin/settings',
      name: 'admin-settings',
      component: () => import('@/views/InstanceSettingsView.vue'),
      meta: { adminOnly: true },
    },
    {
      // `/sync` is the user-facing name — the dashboard's status line points at
      // it, so "Offline" has somewhere to lead. `/debug/sync` is the old path
      // and still works; it additionally shows the row-level debug tools.
      path: '/sync',
      alias: '/debug/sync',
      name: 'sync-debug',
      component: () => import('@/views/SyncDebugView.vue'),
    },
    {
      path: '/signin',
      name: 'signin',
      component: () => import('@/views/SignInView.vue'),
      meta: { public: true },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { public: true },
    },
  ],
})

/**
 * Routes are private by default — a new screen is protected unless it opts out,
 * which is the safe direction to forget in.
 *
 * The guard awaits `bootstrap()` so a hard refresh on any page resolves the
 * session before deciding. Without that wait every reload would bounce through
 * /signin and lose the page the user was on.
 */
router.beforeEach(async (to) => {
  // Each screen starts with a clean slate — an error from a screen you have
  // since left is not information about the one you are looking at now. See
  // notify.ts's clearErrors for the failure this fixes.
  useNotifyStore().clearErrors()

  const auth = useAuthStore()
  await auth.bootstrap()

  if (!to.meta.public && !auth.isSignedIn) {
    return { name: 'signin', query: to.fullPath === '/' ? {} : { next: to.fullPath } }
  }
  if (to.name === 'signin' && auth.isSignedIn) {
    return { name: 'home' }
  }
  if (to.meta.adminOnly) {
    // Confirmed with the server, not from the cached profile: bootstrap renders
    // from cache so the ledger paints instantly, but a privilege check must not
    // ride on that. Without this a demoted account walks onto the screen and
    // every call on it answers 403 — which reads as the app being broken rather
    // than as "you are not an admin". One round trip, on admin screens only.
    await auth.whenChecked()
    if (!auth.user?.isAdmin) return { name: auth.isSignedIn ? 'home' : 'signin' }
  }
  return true
})

export default router
