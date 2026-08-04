import { createRouter, createWebHistory } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
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
  const auth = useAuthStore()
  await auth.bootstrap()

  if (!to.meta.public && !auth.isSignedIn) {
    return { name: 'signin', query: to.fullPath === '/' ? {} : { next: to.fullPath } }
  }
  if (to.name === 'signin' && auth.isSignedIn) {
    return { name: 'home' }
  }
  return true
})

export default router
