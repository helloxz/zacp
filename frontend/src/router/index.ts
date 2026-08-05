import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '@/stores/session'

/**
 * 应用路由（无语言路径前缀；语言由 localStorage / 浏览器决定）。
 *
 * 三态路由：
 * - `/`           无项目首屏 / 空态引导（有项目时由守卫自动跳转）
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

/**
 * 首页兜底守卫：`/` 只在「一个项目都不存在」时展示【新建项目】引导；
 * 存在项目时自动跳到该项目的最近会话（/sessions/:id）或新建会话空态
 * （/new?workspaceId=X）。replace 跳转避免历史栈膨胀（回退不重复经过首页）。
 */
router.beforeEach(async (to) => {
  if (to.name !== 'home') {
    return true
  }
  const sessionStore = useSessionStore()
  // 等待首屏数据就绪（幂等：与 AppShell onMounted 的调用复用同一 promise）
  await sessionStore.loadInitial()

  // 「第一个项目」= 侧栏第一个分组（最新会话所在项目；无会话时最近使用），
  // 与 SidebarSessionList 分组顺序一致，避免跳到侧栏后面的项目
  const first = sessionStore.firstWorkspace()
  if (!first) {
    // 无任何项目：留在首页显示【新建项目】引导
    return true
  }

  // 该项目下的最近活跃会话（sessions 按 updatedAt 倒序，首个匹配即最近）
  const recent = sessionStore.sessions.find(
    (s) => s.workspaceId === first.id,
  )
  if (recent) {
    return {
      name: 'session',
      params: { sessionId: String(recent.id) },
      replace: true,
    }
  }
  // 项目下还没有会话 → 进入该项目的「新建会话」空态
  return {
    name: 'new',
    query: { workspaceId: String(first.id) },
    replace: true,
  }
})

export default router
