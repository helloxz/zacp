import { createRouter, createWebHistory } from 'vue-router'

/**
 * 应用路由（无语言路径前缀；语言由 localStorage / 浏览器决定）。
 */
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      component: () => import('@/layouts/AppLayout.vue'),
      children: [
        {
          path: '',
          name: 'home',
          component: () => import('@/pages/HomePage.vue'),
          meta: { titleKey: 'nav.home' },
        },
        {
          path: 'sessions',
          name: 'sessions',
          component: () => import('@/pages/SessionsPage.vue'),
          meta: { titleKey: 'nav.sessions' },
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/pages/SettingsPage.vue'),
          meta: { titleKey: 'nav.settings' },
        },
      ],
    },
  ],
})

export default router
