# zacp frontend

ACP 多 Agent 网关的 Web UI（会话、流式输出、权限确认等）。

## 技术栈

| 项 | 选择 |
|----|------|
| 框架 | Vue 3 + TypeScript + Vite |
| UI | Naive UI（按需：`unplugin-vue-components` + `NaiveUiResolver` + `unplugin-auto-import`） |
| 样式 | Tailwind CSS v4 |
| 状态 | Pinia |
| 路由 | Vue Router |
| 国际化 | vue-i18n（`zh-CN` / `en-US`） |
| 包管理 | **仅 Bun**（勿用 npm / pnpm / yarn） |
| 实时 | 浏览器原生 WebSocket（后端 `/api/v1/ws` 已实现，前端对接为 P2） |

## 开发

```bash
bun install
bun run dev
```

- 监听：**`0.0.0.0:8681`**（本机 `http://127.0.0.1:8681`，局域网用机器 IP 访问）
- 后端地址：环境变量 **`VITE_API_BASE_URL`**（见下方「环境变量」）
- `vite.config.ts` 仍保留 `/api` 开发代理；当 `VITE_API_BASE_URL` 指向完整后端地址时，请求直连该地址（需后端 CORS）

```bash
bun run build
bun run preview
```

## 环境变量

| 文件 | 何时加载 | 说明 |
|------|----------|------|
| `.env.development` | `bun run dev` | 开发默认 |
| `.env.production` | `bun run build` | 生产构建默认 |
| `.env.example` | — | 字段说明（可复制） |
| `.env.*.local` | 对应 mode | 本机覆盖，**不提交** |

| 变量 | 说明 |
|------|------|
| `VITE_API_BASE_URL` | 后端 HTTP 基础 URL，**无尾部 `/`**。开发默认 `http://192.168.50.20:8680`；生产**留空**表示与前端同域（反代 `/api`）。 |

读取封装：`src/config/env.ts`（`apiUrl()` / `wsUrl()`）。  
HTTP 客户端：`src/api/http.ts`（见下方「请求后端」）。

```bash
# 本机临时改后端地址（不改仓库文件）
echo 'VITE_API_BASE_URL=http://127.0.0.1:8080' > .env.development.local
```

## 请求后端

基础域名已封装：业务代码**只写路径** `/api/v1/...`。

```ts
import { http, ApiError } from '@/api'

const { agents } = await http.get<{ agents: unknown[] }>('/api/v1/agents')
```

完整用法（方法列表、query/body/取消、`ApiError`、WebSocket、分层建议）见：

**→ [docs/frontend/development.md](../docs/frontend/development.md)**（前端开发说明）

## 目录结构

```text
src/
├── api/           # REST 封装（P1 接线）
├── components/
│   ├── chat/      # 对话区组件（ChatPane / Composer / MessageList…）
│   └── shell/     # 壳层组件（AppSidebar / SessionList / SettingsDrawer…）
├── composables/   # 组合式函数（语言切换；WS 封装为 P2）
├── layouts/       # 布局（AppShell：侧栏 + 主区）
├── locales/       # vue-i18n 文案（zh-CN / en-US）
├── pages/         # 页面（ShellPage 挂 AppShell）
├── router/        # Vue Router
├── stores/        # Pinia（app / agent / session）
├── styles/        # Tailwind 入口
├── types/         # TS 类型
├── utils/         # 纯函数工具（含相对时间）
├── App.vue
└── main.ts
```

## 多语言约定

- 语言：`zh-CN`、`en-US`
- 解析顺序：`localStorage`（`zacp.locale`）→ `navigator.language`（`zh*` → 中文，否则英文）→ 兜底 `zh-CN`
- 切换入口：设置抽屉 / 顶栏（无 URL 语言前缀）
- 切换时同步：`vue-i18n` + Naive `NConfigProvider` locale + `document.documentElement.lang`
- **不翻译** Agent 流式内容、工具调用结果、用户代码

新增文案：同时改 `src/locales/zh-CN.ts` 与 `en-US.ts`，key 保持一致。

## Naive UI 按需引入

按[官方文档](https://www.naiveui.com/zh-CN/os-theme/docs/import-on-demand)：

| 方式 | 用途 |
|------|------|
| `unplugin-vue-components` + `NaiveUiResolver` | 模板里的 `n-button` 等组件自动按需解析 |
| `unplugin-auto-import` | `useMessage` / `useDialog` / `useNotification` / `useLoadingBar` |

约定：

- **模板组件**：直接写 `<n-xxx>`，不要 `import { NXxx } from 'naive-ui'`
- **script 里 `h()` 渲染**（如菜单 icon）：仍可显式 `import { NIcon } from 'naive-ui'`（解析器只处理 template）
- **locale / 类型**：`zhCN`、`enUS`、`MenuOption` 等继续从 `naive-ui` 引入

## 约定

详见仓库根目录 `AGENTS.md`（可维护性、关键路径中文注释、与后端 camelCase JSON 对齐等）。

## 文档

| 文档 | 说明 |
|------|------|
| [docs/frontend/development.md](../docs/frontend/development.md) | **前端开发说明**（环境变量、HTTP 库、WebSocket） |
| [docs/frontend/chat-shell-ui-design.md](../docs/frontend/chat-shell-ui-design.md) | 对话壳层 UI 设计 |
| [docs/frontend/implementation-plan.md](../docs/frontend/implementation-plan.md) | 壳层 UI **实施计划**（P0-P3 任务与验收） |
| [AGENTS.md](../AGENTS.md) | 全仓库协作约定 |

## 当前进度

| 阶段 | 内容 | 状态 |
|------|------|------|
| P0 | 壳层静态：AppShell + 侧栏 + 空态/会话中 + Composer + 设置抽屉（假数据动线） | ✅ 完成 |
| P1 | REST 接线：types/store 换真数据 + 后端补全局会话列表接口 | 待办 |
| P2 | WebSocket 流式：useAcpSocket、发送/停止、流式追加 | 待办 |
| P3 | 增强：权限弹窗、工具调用卡片、侧栏折叠 | 待办 |

> P0 数据为 store 内假数据（`stores/agent.ts` / `stores/session.ts`），组件不感知真假；P1 仅替换 store 实现。
