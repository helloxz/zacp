import { createRouter, createWebHistory } from 'vue-router'

/**
 * 应用路由（无语言路径前缀；语言由 localStorage / 浏览器决定）。
 * 壳层设计：`/` 空态与 `/sessions/:id` 会话中共享 ShellPage（AppShell + ChatPane），
 * ChatPane 按 route.params.sessionId 切换状态（见设计文档 §2.3）。
 */
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('@/pages/ShellPage.vue'),
    },
    {
      // 后端 Session id 为数字（uint）
      path: '/sessions/:sessionId(\\d+)',
      name: 'session',
      component: () => import('@/pages/ShellPage.vue'),
    },
  ],
})

export default router
