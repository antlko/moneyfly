import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

/**
 * Routes mirror the navigation in docs/08-ux.md §8.2: Budget, Capital, quick entry,
 * History, More, plus the year grid the workbook's layout asks for.
 */
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/budget' },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { public: true },
    },
    {
      path: '/change-password',
      name: 'change-password',
      component: () => import('@/views/ChangePasswordView.vue'),
      meta: { allowWhilePasswordChangePending: true },
    },
    { path: '/budget', name: 'budget', component: () => import('@/views/BudgetView.vue') },
    { path: '/entry', name: 'entry', component: () => import('@/views/QuickEntryView.vue') },
    { path: '/history', name: 'history', component: () => import('@/views/HistoryView.vue') },
    { path: '/more', name: 'more', component: () => import('@/views/MoreView.vue') },
    { path: '/capital', name: 'capital', component: () => import('@/views/CapitalView.vue') },
    { path: '/year', name: 'year', component: () => import('@/views/YearView.vue') },
    { path: '/import', name: 'import', component: () => import('@/views/ImportView.vue') },
    { path: '/settings', name: 'settings', component: () => import('@/views/SettingsView.vue') },
    {
      path: '/settings/categories',
      name: 'settings-categories',
      component: () => import('@/views/CategoriesView.vue'),
    },
    {
      path: '/settings/accounts',
      name: 'settings-accounts',
      component: () => import('@/views/AccountsView.vue'),
    },
    {
      path: '/settings/aliases',
      name: 'settings-aliases',
      component: () => import('@/views/AliasesView.vue'),
    },
    {
      path: '/settings/plan',
      name: 'settings-plan',
      component: () => import('@/views/PlanView.vue'),
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFound.vue'),
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.checked) {
    await auth.refresh()
  }
  if (to.meta.public) {
    return auth.user ? { name: 'budget' } : true
  }
  if (!auth.user) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  // The bootstrap admin can reach exactly one screen until the password changes;
  // the API refuses everything else with a 403 regardless.
  if (auth.user.must_change_password && !to.meta.allowWhilePasswordChangePending) {
    return { name: 'change-password' }
  }
  return true
})
