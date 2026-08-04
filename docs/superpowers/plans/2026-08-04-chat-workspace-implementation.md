# 会话工作台实现计划

> 规格：`docs/superpowers/specs/2026-08-04-chat-workspace-design.md`  
> 日期：2026-08-04  
> 原则：可并行的前后端先并行；每步可验证；改完同步 `docs/API.md` 与必要时 `AGENTS.md`

---

## 总览与依赖

```text
B0-1 模型/迁移/Workspace 归档+默认
  → B0-2 Session 创建返回 configOptions + set_config
  → B0-3 权限桥接 + manager 多会话事件路由
  → B0-4 WS 闭环（prompt/cancel/permission/落库）
  → B0-5 修 DeleteSession + agents 响应清理

F0 壳层（可与 B0-1～B0-3 并行）
  → F1 REST 接线 + D1 新建 + 三下拉
  → F2 WS 流式 + 停止 + 权限卡片
  → F3 打磨 + 文档对齐
```

| 轨道 | 内容 | 依赖 |
|------|------|------|
| **B0** | 后端契约闭环 | 无 |
| **F0** | UI 壳层 mock | 无（可与 B0 并行） |
| **F1** | REST 真数据 | B0-1、B0-2 基本可用 |
| **F2** | WS 对话 | B0-3、B0-4 |
| **F3** | 体验与文档 | F2 |

**推荐执行顺序（单人）：** B0-1 → B0-2 → F0（可穿插）→ B0-3 → B0-4 → B0-5 → F1 → F2 → F3  

**双人：** 后端 B0 全序；前端 F0→F1（mock/部分 REST）→ F2 等 B0-4。

---

## 阶段 B0 — 后端

### Task B0-1：Workspace 归档 + 默认工作区

**目标：** 列表只看未归档；删除=归档；同 path 再添加恢复；启动确保默认工作区。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/model/models.go` | `Workspace` 增加 `Archived bool`；可选 `IsDefault bool` |
| `backend/internal/store/store.go` | 新 migration（版本 +1）：加列 `archived` 默认 false |
| `backend/internal/store/repository.go` | `List` 过滤 `archived=false`；`GetByPath` 支持含归档（Unscoped 或显式参数）；`Archive` / `Unarchive` / `SetArchived` |
| `backend/internal/service/service.go` | `CreateWorkspace`：同 path 归档则解除；`DeleteWorkspace`→归档；`EnsureDefaultWorkspace(absPath, name)` |
| `backend/internal/api/handlers/workspace.go` | 删除语义改为归档；Create 支持 name；错误码对齐规格 |
| `backend/cmd/server/main.go` | 启动时 `EnsureDefaultWorkspace(abs(cfg.Session.DefaultCwd))` |
| `backend/configs/config.example.toml` | 注释说明 default_cwd 即默认工作区路径 |

**实现要点**

1. **不要**再用「仅软删 + 列表隐藏」冒充归档，除非同步实现 Unscoped 恢复；**优先 `archived` 字段**。  
2. `default_cwd` 用 `filepath.Abs`；目录必须存在，否则启动 log.Warn 或跳过并打日志（实现时二选一：**推荐**目录不存在则跳过默认工作区并 Warn，不阻断启动）。  
3. 默认工作区 `name` 固定 `"默认工作区"`（前端 i18n 可按 path/isDefault 覆盖显示）。

**验证**

```bash
cd backend && go build -o /tmp/zacp ./cmd/server
# 启动后
curl -s localhost:8680/api/v1/workspaces | jq .
# POST 一存在路径 → list 可见
# DELETE 该 id → list 不可见
# 再 POST 同 path → list 可见且 sessions 仍在（若有）
```

---

### Task B0-2：Session 创建透传 configOptions + set_config

**目标：** 新建会话立即返回 Agent 配置；可切换 model/mode/thought。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/acp/manager/manager.go` | `CreateSession` 返回 `NewSessionResponse` 关键字段（sessionId + ConfigOptions + Modes），勿丢弃；新增 `SetSessionConfigOption`、`SetSessionMode` |
| `backend/internal/service/service.go` | `CreateSession` 返回 DTO：`session` + `configOptions` + `modes?`；`SetConfig`；`DeleteSession` 暂不在此 task 修 StopAgent 也可放 B0-5 |
| `backend/internal/model/models.go` 或 `service` DTO | 定义 JSON 友好的 `ConfigOption` 视图（camelCase），避免直接暴露难序列化的 SDK union 类型时可做映射层 |
| `backend/internal/api/handlers/session.go` | Create 响应带 config；`POST /sessions/:id/config` |
| `backend/internal/api/router/router.go` | 注册 config 路由 |

**实现要点**

1. SDK `SessionConfigOption` 是 union（select/boolean），handler 层映射为前端易用的 JSON：  
   `{ id, name, description?, category?, type, currentValue, options?: [{ value, name, description? }] }`  
2. 内存可缓存 `dbSessionID → latestConfigOptions`（manager 或 service），便于 `GET` 与 WS 更新。  
3. 仅有 `modes` 无 `configOptions` 时，合成伪 config 或单独返回 `modes` 供前端回退。

**验证**

```bash
# 需本机有可用 agent（如 reasonix --acp）
curl -s -X POST localhost:8680/api/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{"workspaceId":1,"agentId":"reasonix"}' | jq .
# 期望：session + configOptions（或 modes）
curl -s -X POST localhost:8680/api/v1/sessions/1/config \
  -H 'Content-Type: application/json' \
  -d '{"configId":"model","value":"..."}' | jq .
```

---

### Task B0-3：权限桥接 + 多会话事件路由

**目标：** 非 auto 时权限等 UI；SessionUpdate 按会话推送不串流。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/acp/client/client.go` | `RequestPermission`：非 auto 时生成 permissionId，回调 `onPermissionRequest`，阻塞等 channel；`ResolvePermission`；解析 `ConfigOptionUpdate` / `CurrentModeUpdate` 推事件 |
| `backend/internal/acp/manager/manager.go` | 多 Session 状态表（勿只靠 `currentSession`）；按 acpSessionId 路由事件；Bridge 与 DB session 映射可由上层注入 |
| `backend/internal/ws/bridge.go` / `protocol.go` | 权限推送、config 更新推送接口 |

**实现要点**

1. pending map：`permissionId → chan outcome`；`cancel` 时关闭/发 reject。  
2. **不自动超时**（规格 C1）。  
3. `SetOnEvent` 需带 session 维度；Agent 级单回调时 payload 必须含 acpSessionId 再映射 DB id。  
4. 中文注释：权限等待不变量、禁止静默 cancel。

**验证**

- 单元测试（可选）：mock Bridge 权限等待与 Resolve。  
- 或集成：`auto_approve=false`，触发写文件权限，确认进程阻塞直到 HTTP/WS 注入结果（可在 B0-4 一并测）。

---

### Task B0-4：WebSocket 闭环

**目标：** 单连接多会话；prompt/cancel/permission 用 DB sessionId；落库；流式转发。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/ws/protocol.go` | 消息字段对齐规格；所有下行带 `sessionId`（DB） |
| `backend/internal/ws/hub.go` | `prompt`/`cancel`/`permission` 用 `msg.SessionID` 解析，不依赖连接级单 bind（可保留可选 bind） |
| `backend/internal/ws/bridge.go` | 注入 `SessionService`/store：查 session → agentId + acpSessionId；落库 user/assistant；Setup 事件回调映射 |
| `backend/internal/ws/handler.go` | 广播按 DB sessionId 过滤订阅者：连接可「关注」多个 session，或广播时所有连接都收、前端按 sessionId 过滤（**推荐前端过滤 + 服务端带 sessionId**，实现简单） |
| `backend/internal/service/service.go` | 抽出 `PromptTurn` / `AppendMessage` 供 WS 与 REST 复用 |
| `backend/internal/api/router/router.go` | 确保 WS 注入完整依赖 |
| `backend/cmd/server/main.go` | 组装 EventBridge 依赖 store/service |

**实现要点**

1. **对外 sessionId = `strconv.FormatUint(dbID)`**。  
2. `prompt` 流程：校验 session active → 写 user msg → `mgr.Prompt`（流式事件已推）→ turn.done 写 assistant。  
3. 流式：在 Prompt 阻塞返回前，事件已通过 onEvent 推送；注意 reset events 缓冲按轮次。  
4. `cancel`：`mgr.Cancel` + 解除 permission wait。  
5. Origin 开发期可保持 InsecureSkipVerify；注释生产需校验。

**验证**

```bash
# 使用 websocat / wscat
# 1. POST session 得 id
# 2. ws://localhost:8680/api/v1/ws
# 3. {"type":"prompt","sessionId":"1","message":"hi"}
# 4. 应收到 event / turn.done
# 5. GET /api/v1/sessions/1/messages 有记录
# 6. 生成中发 cancel 应停止
```

---

### Task B0-5：杂项修复

**改动**

| 项 | 文件 | 动作 |
|----|------|------|
| DeleteSession 勿 StopAgent | `service.go` | 仅软删会话/消息策略按产品：软删 session 即可；可选不删 messages |
| ListAgents 响应 | `manager.go` / `chat.go` | 弱化或移除误导 `sessionId`；保留 agentId/name/running |
| 创建会话失败回滚 | `service.go` | 勿在「仅创建失败」时 Stop 整个 agent |

**验证：** `go build ./...`；删一会话后同 agent 其它会话仍可用。

---

## 阶段 F0 — 前端壳层

### Task F0-1：AppShell 布局替换

**改动文件**

| 文件 | 改动 |
|------|------|
| `frontend/src/layouts/AppShell.vue` | 新建：左栏 + 主区 `h-screen` |
| `frontend/src/components/shell/*` | `AppSidebar`、`WorkspaceSessionTree`、`UserFooter`、`SettingsDrawer` |
| `frontend/src/components/chat/*` | `ChatPane`、`WelcomeHero`、`Composer`（card/bar）、`MessageList`、`MessageItem`、`PermissionCard`（可先静态） |
| `frontend/src/pages/ShellPage.vue` | 挂壳层 |
| `frontend/src/router/index.ts` | `/` + `/sessions/:id`；去掉或降级旧 Home/Sessions 顶栏布局 |
| `frontend/src/locales/zh-CN.ts` / `en-US.ts` | `shell.*` `chat.*` `settings.*` `workspace.*` |
| `frontend/src/layouts/AppLayout.vue` | 停用或仅给设置兜底 |

**Mock：** 侧栏假 workspace/session；主区空态 + Composer。

**验证**

```bash
cd frontend && bun run dev
# 视觉：侧栏分组、新建按钮、空态、设置抽屉
```

---

### Task F0-2：路由与选中态

- 点击会话 → `/sessions/:id` 高亮。  
- 「新建」在 mock 下仅清空选中或生成临时 id（F1 再接真 API）。

**验证：** 刷新路由不白屏；浏览器前进后退可用。

---

## 阶段 F1 — REST 接线

### Task F1-1：API 模块与类型

**新建**

```text
frontend/src/api/agents.ts
frontend/src/api/workspaces.ts
frontend/src/api/sessions.ts
frontend/src/types/api.ts   # Workspace, Session, ConfigOption, Agent, Message
```

统一 `http.get/post/delete`，路径 `/api/v1/...`。

### Task F1-2：Pinia stores

| store | 职责 |
|-------|------|
| `stores/workspace.ts` | fetchList、create(path)、archive、currentId（localStorage） |
| `stores/agent.ts` | fetchList |
| `stores/session.ts` | 按 workspace 拉 sessions、create(D1)、remove、current、lastConfigOptions |
| `stores/chat.ts` | loadMessages(REST)、本地 append（为 F2 预留） |

### Task F1-3：D1 新建 + 三下拉

1. 进入壳：并行 load agents + workspaces。  
2. 新建：`agents.length===1` → 直接 create；否则主区 Agent Tabs → 点击 create。  
3. create 响应写入 `configOptions`；Composer 按 category 渲染 model / mode / thought_level。  
4. 切换 config → `POST /sessions/:id/config`，用响应刷新。  
5. 添加工作区：Naive 输入对话框 → POST；错误 `ApiError` toast。  
6. 归档：确认后 DELETE/archive，刷新树。

**验证：** 端到端无 WS：建工作区、建会话、切换 config（若 agent 支持）、刷新生效。

---

## 阶段 F2 — WebSocket 对话

### Task F2-1：`useAcpSocket`

**文件：** `frontend/src/composables/useAcpSocket.ts`

- 单例连接 `wsUrl('/api/v1/ws')`  
- 心跳 ping/pong  
- 断线指数退避重连  
- `sendPrompt` / `sendCancel` / `sendPermission`  
- `onMessage` → chat store  

### Task F2-2：流式 UI

- 发送：本地先插入 user 气泡 → WS prompt → assistant 占位 → chunk 追加 **改最后一条**（禁止整表重建）  
- `turn.done`：定稿内容、isStreaming=false  
- 停止：cancel + UI interrupted  
- 进入会话：REST 拉历史 + 确保 socket 已连  

### Task F2-3：权限卡片

- 收到 `permission.request` → `pendingPermission`  
- `PermissionCard` 渲染 options  
- 点击 → `sendPermission` → 清除 pending  
- 无自动超时  

**验证：** 完整一轮对话；cancel；触发权限（auto_approve=false）点允许后继续。

---

## 阶段 F3 — 打磨与文档

| 项 | 动作 |
|----|------|
| 会话标题 | 首条 user 消息截断更新 title（PATCH 或创建时后端后续加；前端可先乐观更新） |
| 相对时间 | 侧栏简易相对时间 i18n |
| 折叠记忆 | localStorage workspace 折叠 |
| `docs/API.md` | 对齐归档、config、WS sessionId、limit/offset、删除语义 |
| `docs/frontend/chat-shell-ui-design.md` | 补「规格已定稿」链接与 Workspace-first |
| `AGENTS.md` §11 | 勾选 WS/前端会话 UI 完成态 |
| CORS / 端口 | 确认 `VITE_API_BASE_URL` 与后端 `:8680` 一致 |

**验证：** 对照规格 §10 验收标准逐条勾选。

---

## 关键路径中文注释要求（实现时）

以下位置**必须**有清晰中文注释（规格 / AGENTS.md §7.0）：

- 权限等待与 Resolve  
- WS sessionId（DB vs ACP）映射  
- 事件按会话路由  
- Workspace 归档与同 path 恢复  
- 前端 useAcpSocket 状态机、流式 append 策略  

---

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| Agent 不返回 configOptions | UI 隐藏控件，对话仍可用 |
| 多会话单 Agent 进程事件串流 | B0-3 映射表 + 事件带 sessionId |
| Prompt 阻塞与 WS 读循环 | Prompt 放独立 goroutine（现有已 go）；ctx 取消要通 |
| SDK ConfigOption JSON 难用 | service 层 DTO 映射，单测序列化 |
| 空会话变多 | 规格接受；二期再清理策略 |

---

## 建议首个 PR 切片

为降低单 PR 过大，建议拆分提交/PR：

1. **PR1：** B0-1 Workspace 归档 + 默认工作区  
2. **PR2：** B0-2 configOptions + set_config  
3. **PR3：** B0-3 + B0-4 + B0-5 WS/权限  
4. **PR4：** F0 壳层  
5. **PR5：** F1 REST  
6. **PR6：** F2 WS UI  
7. **PR7：** F3 文档与打磨  

单人可按 PR1→… 线性做；壳层 PR4 可在 PR1 后并行分支。

---

## 每步通用检查清单

- [ ] `cd backend && go build -o /tmp/zacp ./cmd/server`  
- [ ] （有测）`go test ./internal/...`  
- [ ] `cd frontend && bun run build`  
- [ ] 行为符合 `2026-08-04-chat-workspace-design.md` 对应条款  
- [ ] 不引入 axios / 第二套 UI 库 / CGO sqlite  

---

## 下一步

实现时从 **B0-1** 开始（或声明并行做 **F0-1**）。  

完成一个 Task 后在本计划文件或 PR 描述勾选验证命令输出，再进入下一 Task。
