import { createRouter, createWebHistory } from 'vue-router'

/**
 * 应用路由（无语言路径前缀；语言由 localStorage / 浏览器决定）。
 *
 * 三态路由：
 * - `/`           无项目首屏 / 空态引导
 * - `/new`        新建会话空态（agent tab 选择 + 隐式草稿预览配置项；?workspaceId=X 预选项目）
 * - `/sessions/:id` 已有会话（对话列表；agent 已锁定，无 tab）
 *
 * 壳层设计：三者共享 ShellPage（AppShell + ChatPane），ChatPane 按 route 分流状态。
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
      // 新建会话空态：选择 agent + 预览配置项；query workspaceId 指定所属项目
      path: '/new',
      name: 'new',
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
