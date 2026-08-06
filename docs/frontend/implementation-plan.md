# zacp 前端壳层 UI 实施计划

> 依据：`docs/frontend/chat-shell-ui-design.md`（设计稿）  
> 状态：已批准；**P0–P2 已完成（2026-08-04），P3 待办**  
> 决策（用户确认）：**P0 假数据定型壳层；P1 直接接真实 REST**（后端 API 与 WS 已就绪，不做完整 mock 阶段）

---

## 0. 现状基线（2026-08-04 核实）

### 0.1 后端（P1/P2 已演进，见 §3/§4；基线提交 `5db1a16`）

| 能力 | 接口 | 状态 |
|------|------|------|
| Agent 列表 | `GET /api/v1/agents` → `{ agents: [...] }` | ✅ |
| Agent 状态 | `GET /api/v1/agents/:agentId/status` | ✅ |
| Workspace | `GET/POST/DELETE /api/v1/workspaces`、`GET /workspaces/:id` | ✅ POST 需 `{path}` |
| 会话 | `POST/GET/DELETE /api/v1/sessions` | ✅ P1 后 `workspaceId` 可选（回退默认工作区） |
| 按工作区列会话 | `GET /api/v1/workspaces/:id/sessions` | ✅ |
| 消息 | `POST/GET /api/v1/sessions/:id/messages`（limit/offset 分页 → `{messages,total,limit,offset}`） | ✅ |
| WebSocket | `GET /api/v1/ws` | ✅ 协议见 `internal/ws/protocol.go`：`prompt`/`cancel`/`permission`/`ping`；`session.ready`/`event`/`turn.done`/`permission.request`/`error`/`pong` |

### 0.2 前端已有设施（可复用）

- `src/api/http.ts`：fetch 薄封装（camelCase、统一错误体、AbortSignal）
- `src/config/env.ts`：`apiUrl()` / `wsUrl()`（WS 地址拼接已就绪）
- `src/stores/app.ts`：locale（Naive locale 联动）
- `src/composables/useLocaleSwitch.ts`、`src/utils/locale.ts`、`src/components/LocaleSwitch.vue`
- 顶栏式 `AppLayout`（Home/Sessions/Settings 三页）——**将被 AppShell 替换**

### 0.3 与设计文档的偏差（已核实）

| 项 | 设计文档假设 | 实际 | 处置 |
|----|--------------|------|------|
| WS | 规划中（P3） | 已实现 | P2 直接对接，无需等后端 |
| 全局会话列表 | 侧栏「最近会话」 | 仅按 workspace 列 | **P1 补后端接口** `GET /api/v1/sessions` |
| `POST /sessions` | workspace 可选、空用 default_cwd | workspaceId required | **P1 后端放宽**为可选（回退 default workspace / `session.default_cwd`） |
| `docs/API.md` | — | agents 响应格式与代码不符（文档数组、代码 `{agents}`） | 前端 types 以**代码**为准 |

---

## 1. 阶段总览

| 阶段 | 内容 | 产出 | 状态 |
|------|------|------|------|
| **P0** | 壳层静态：AppShell + 侧栏 + 空态 + Composer + 设置抽屉（假数据动线） | 视觉对齐参考图的简化版 | ✅ 完成 |
| **P1** | REST 接线：types/store/侧栏真实列表/历史消息 + 后端小改 | 真数据列表与历史 | ✅ 完成（2026-08-04） |
| **P2** | WebSocket 流式：useAcpSocket、发送/停止、流式追加 | 完整对话闭环 | ✅ 完成（2026-08-04） |
| **P3** | 增强（对应设计文档 §9 P4）：权限弹窗、工具调用卡片、侧栏折叠（展开/收起按钮） | 体验完善 | ✅ 完成（2026-08-04） |

---

## 2. P0 任务清单（完成，2026-08-04）

### 2.1 路由与骨架

- [x] 路由：`/`（空态）与 `/sessions/:sessionId`（会话中）均渲染 `ShellPage`；删除 `/settings` 路由
- [x] `src/pages/ShellPage.vue`：挂载 AppShell
- [x] `src/layouts/AppShell.vue`：`h-screen overflow-hidden`；左 260px `AppSidebar` + 右 `ChatPane`；持有 `settingsOpen` 状态渲染 `SettingsDrawer`
- [x] 删除 `AppLayout.vue`、`HomePage.vue`、`SessionsPage.vue`、`SettingsPage.vue`（逻辑并入壳层，避免双首页）

### 2.2 侧栏（components/shell/）

- [x] `AppSidebar.vue`：顶部「新建会话」按钮 + 中部 `SidebarSessionList`（`flex-1 overflow-auto`）+ 底部 `UserFooter`（`shrink-0`）；「新建会话」emit 清空选中回空态
- [x] `SidebarSessionList.vue`：两级结构（workspace 分组头 + 会话项）；P0 假数据；空态引导文案
- [x] `SessionListItem.vue`：title（无则「新会话」）、相对时间、agent 名副文案；点击路由 `/sessions/:id`、当前高亮、hover 底色
- [x] `UserFooter.vue`：头像（首字母圆占位）、显示名、齿轮按钮（emit `open-settings`）

### 2.3 主区（components/chat/）

- [x] `ChatPane.vue`：按 `route.params.sessionId` 切换空态 / 会话中；处理 Composer submit（假数据创建会话、追加消息、跳转路由）
- [x] `WelcomeHero.vue`：时段问候（早上/下午/晚上）+ 极淡水印 + 居中 `Composer`（card 模式）
- [x] `MessageList.vue` / `MessageItem.vue`：user 右 / assistant 左，样式区分；流式追加约束（追加/改最后一条）的雏形；简单贴底
- [x] `Composer.vue`：`card` / `bar` 两形态（prop）；Agent 下拉（store）+ Workspace 下拉（可选，store）+ textarea（Enter 发送、Shift+Enter 换行）+ 发送/停止；emit `submit` / `cancel`

### 2.4 设置与状态

- [x] `SettingsDrawer.vue`：语言（复用 `LocaleSwitch`）、显示名（localStorage `zacp.displayName`）、关于（应用名 + 版本占位）
- [x] `stores/app.ts`：扩展 `displayName` / `setDisplayName`
- [x] `stores/agent.ts`、`stores/session.ts`：P0 内置假数据（agents / workspaces / sessions / messagesById），P1 换真实 API

### 2.5 i18n

- [x] 新增 `shell.*` / `chat.*` / `settings.*` key，`zh-CN.ts` / `en-US.ts` 同步
- [x] 相对时间：`src/utils/relativeTime.ts`（今天 / 昨天 / N 天前，自写不引库）

### 2.6 P0 验收

- [x] `cd frontend && bun run build`（含 `vue-tsc`）通过
- [x] 动线手测：打开 `/`（空态）→ 输入并发送 → 自动创建会话进入 `/sessions/:id` → 侧栏出现/高亮 → 点击其它会话切换 → 新建会话回空态 → 齿轮打开设置（改语言/显示名生效）

---

## 3. P1 任务清单（完成）

### 3.1 前端

- [x] `src/api/types.ts`：`Agent` / `Workspace` / `Session` / `Message`（对齐 `backend/internal/model/models.go`，camelCase）→ 落地为 `src/types/models.ts`
- [x] `src/api/index.ts` 扩展业务 API 函数（agents / workspaces / sessions / messages）
- [x] `stores/agent.ts`、`stores/session.ts`：假数据替换为真实 API 调用；侧栏全局最近会话列表；消息历史按需加载
- [x] 删除会话（hover 更多 → popconfirm → DELETE）
- [x] Composer 发送动线：无当前 session → `POST /sessions` → 跳转（保持「首次发送才创建」心智）

### 3.2 后端配合（小改）

- [x] 新增 `GET /api/v1/sessions`：全局最近会话列表（含 workspace 预加载，按 updatedAt 倒序，limit 参数）——store/service/handler/router 各一层
- [x] `POST /sessions`：`workspaceId` 改为可选；为空时回退 `is_default` → `session.default_cwd` 路径 → 按 default_cwd 新建

### 3.3 验收

- [x] 前后端 `go build ./...` + `bun run build` 通过
- [x] 接口级实测（临时 `ZACP_DATA`，因沙箱根文件系统只读）：agents / workspaces / sessions 全局列表 / workspaceId 回退 / 非法 workspaceId 报错均符合预期；浏览器手测待用户环境（reasonix model 配置可用后）

---

## 4. P2 任务清单（完成）

### 4.1 后端（补齐 WS 链路缺口）

- [x] 协议 `ClientMessage` 加 `agentId`；无绑定连接 prompt/cancel 动态 `BindSession`（事件/turn.done 按 GetSessionID 广播才能回送）
- [x] `HandlePrompt` 前 `SetupEventCallback` 接通 ACP 事件流（此前无调用点，`event` 帧不会发出）
- [x] **修复 WS 连接建立后立即关闭的 bug**：`ServeHTTP` 用 `r.Context()` 作连接生命周期，HTTP handler 返回即取消 → 读写协程立即退出；改独立 `context.Background()`
- [x] WS prompt 落库：user 消息（首条生成标题）→ agent 回复 → assistant 消息 + touch 会话；repository 新增 `GetByACPSessionID` / `Touch`
- [x] 探针实测：`ping→pong`、`prompt→error` 帧回送（连接/绑定/桥接/错误路由全链路通；真实流式事件待 agent model 可用后验证）

### 4.2 前端

- [x] `src/types/ws.ts`：协议类型（client/server 消息 + WsEvent）
- [x] `src/composables/useAcpSocket.ts`：应用级单例连接（30s 心跳、指数退避重连、按 type 分发、手动 disconnect 后不再重连）
- [x] session store 流式状态机：`sendViaWs`（乐观 user + 空占位 → WS prompt）、`agent_message` 追加/改最后一条、`turn.done` 强制刷新为 DB 版本、`cancelSend`（WS cancel）、`streamError`
- [x] `useChatScroll.ts` + MessageList：自动贴底、用户上滚暂停跟随、「回到底部」按钮
- [x] ChatPane：`sendViaWs`、`:sending` 接 streaming、错误条接 streamError；AppShell 挂载时 `acpSocket.connect()`
- [x] 验收：`go build ./...` + `bun run build` 通过；真实流式对话闭环手测待 agent model 可用环境

---

## 5. P3 任务清单（完成）

> 本阶段对应设计文档 §9 的 **P4 增强**（P0–P2 与设计文档编号一致；WS 因后端已实现提前为 P3，故增强顺移为 P3）。
>
> **范围外（遵循设计文档 §1.4 非目标，首期明确不做，任何阶段都不做）**：
> 完整鉴权账号体系、深色主题切换、URL 语言前缀 / SEO 多语言路由、定时任务、技能市场、计划模式、画板、子智能体编排、复杂「项目树 / SSH 多主机」管理（zacp 用 Workspace 工作目录）。
>
> 本计划**仅覆盖前端壳层 UI**；仓库级工程（部署编排 `Dockerfile` / `scripts/`、鉴权等生产安全加固）不在本计划范围内，由 AGENTS.md 工程约定另行约束。

### 5.1 后端（权限请求链路）

- [x] `Bridge` 加 `SetPermissionHandler`：非 `autoApprove` 时把 `RequestPermission` 转发给 EventBridge（交互式授权），否则维持默认（自动放行 / 取消）
- [x] `EventBridge` 权限交互：`HandlePermissionRequest`（生成 permissionID → 广播 `permission.request`（显式挑字段的 toolCall/options）→ 等待回传，60s 超时自动取消）；`ResolvePermission`（前端 `permission` 帧回传 → 唤醒等待的 agent turn）
- [x] `hub.go` `MsgTypePermission` 分支接通 `ResolvePermission`（原 TODO 空实现）
- [x] 探针实测：未知 permissionId 的 `permission` 帧被正确忽略（log `permission not pending`），连接保持健康（ping→pong）

### 5.2 前端

- [x] `src/types/ws.ts`：`PermissionOption` / `PermissionToolCall` 类型（对齐后端广播结构）
- [x] `PermissionModal.vue`：不可遮罩关闭的弹窗，展示工具调用信息 + 选项按钮 → `resolvePermission` 回传 `permission` 帧
- [x] `ToolCallCard.vue`：工具调用卡片（状态点 running 呼吸 / completed 绿 / error 红 + 标题 + 状态文案）
- [x] 实时工具卡片：session store `activeToolCards`（`tool_call` / `tool_call_update` 事件 upsert，turn.done 清空）+ MessageList 渲染
- [x] 历史工具卡片：MessageItem 解析 assistant 消息 `events` JSON（按 toolId 去重保留末次状态）渲染
- [x] 侧栏折叠：260px ↔ 64px 图标条，展开/收起按钮，localStorage 持久化（**不做移动端抽屉**——PC 界面不兼容移动端）

### 5.3 验收

- [x] `go build ./...` + `go vet` + `bun run build`（vue-tsc + vite）通过
- [x] i18n 50 key 双语一致；dev 编译全部新组件 200
- [ ] 真实权限弹窗交互（agent 实际请求权限 → 弹窗 → 选择 → agent 继续）待 agent model 可用环境端到端手测

---

## 6. 风险与注意事项

1. **`docs/API.md` 已过期**：agents 等响应格式以 `backend/internal/api/handlers/*.go` 实际代码为准，P1 接线时同步修正 API.md。→ **已修正（2026-08-04）**：agents/workspaces/sessions 包装格式、messages limit/offset 分页、新增 GET /sessions、WS 端口 8680。
2. **WS Origin 校验**：`internal/ws/handler.go` 当前 `InsecureSkipVerify: true`（开发便利），生产前需处理——记录为后续事项，不在本次范围。
3. **提交基线**：阶段 1–4 后端成果已提交（`5db1a16 后端初步实现`，2026-08-04）；当前未提交的是 P0–P2 的前端与后端增量，建议 P3 前提交一次。
4. **会话标题**：后端 `SessionRepository.UpdateTitle` 已存在，可在 P1 由首条用户消息生成标题（设计文档 §4.2）。
5. **假数据边界**：P0 假数据只放在 store 初始状态，组件不感知真假；P1 仅替换 store 实现，组件零改动。

---

## 7. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-08-04 | 初稿：基于设计文档与现状基线的分阶段实施计划；决策 P0 假数据 + P1 直接接 REST |
| 2026-08-04 | P0 完成（壳层静态，假数据动线） |
| 2026-08-04 | P1 完成（REST 接线 + 后端 GET /sessions + workspaceId 可选；API.md 修正） |
| 2026-08-04 | P2 完成（WS 流式：协议 agentId + 动态绑定 + 事件桥接 + 落库；修复 r.Context() 连接立即关闭 bug；useAcpSocket/流式状态机/useChatScroll；API.md WS 协议同步） |
| 2026-08-04 | 范围修正：P3 明确对应设计文档 §9 P4 增强；§1.4 非目标项（鉴权账号体系、深色主题、URL 语言前缀等）标注为任何阶段都不做，从待办中移除；同步修正风险 3 的基线描述 |
| 2026-08-04 | 单一权威：AGENTS.md 移除前端壳层 UI 的重复计划维护（P3 细节、明确不做清单），统一以本文件为唯一准绳；明确本计划仅覆盖 UI，仓库级工程（部署编排等）不在内 |
| 2026-08-04 | P3 完成（权限弹窗链路：Bridge handler + EventBridge pending/回传 + hub 接通；工具调用卡片实时/历史渲染；侧栏折叠 260↔64 展开/收起按钮；**不做移动端抽屉**） |
| 2026-08-04 | 修复 P1 遗留 bug：`CreateSession` 构造 session 时误用入参 `workspaceID`（缺省 0）而非解析后的 `workspace.ID`，导致不带 workspaceId 创建会话时外键约束失败（`FOREIGN KEY constraint failed`） |
| 2026-08-04 | 修复 Ctrl+C 无法退出：`ws.Handler.CloseAll` 持有 hub 读锁遍历时逐个 Close（触发无缓冲 unregister → hub.Run 需写锁）→ 死锁；改为先快照再关闭，并加 5s 优雅关闭超时兜底。实测：有活跃 WS 连接时 SIGINT 4s 内干净退出 |
| 2026-08-04 | configOptions 双通道：除 `session/new` 响应外，补齐 `session/update` 的 `ConfigOptionUpdate` 通知接收（Bridge handler → EventBridge 落库）；前端 turn.done 后刷新配置项 |
| 2026-08-04 | 服务端重启后会话恢复：ACP session 是 agent 内存态，重启后 DB 记录仍在但 agent 端丢失（prompt 报 `unknown session`）。后端 `HandlePrompt` 检测到该错误时自动恢复（优先 ACP `session/load` 保留上下文，失败则新建 ACP session 并 `UpdateACPSessionID` 后重试一次）；前端发送前刷新会话拿最新 `acpSessionId` |
