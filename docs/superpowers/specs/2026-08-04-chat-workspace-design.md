# zacp 会话工作台设计规格

> 状态：已定稿（待实现）  
> 日期：2026-08-04  
> 范围：工作区 + 多会话窗口 + Agent/配置选择 + WebSocket 对话 + 权限卡片  
> 相关文档：`AGENTS.md`、`docs/API.md`、`docs/frontend/chat-shell-ui-design.md`、`docs/frontend/development.md`

---

## 1. 背景与目标

**zacp** 是 ACP 多 Agent Web 网关。本规格定义 Web 会话工作台的产品行为与前后端契约，使开发者能在**指定后端工作目录**下与 Agent 多轮对话，并支持多窗口、多工作区。

### 1.1 目标

1. 用户可选择/添加后端绝对路径作为工作区；后端校验目录存在。
2. 每个工作区下可有多个会话窗口；窗口内多轮对话。
3. 新建会话时选择 Agent；会话创建后锁定 Agent，可切换该会话下的模型 / mode / 思考等级（若 Agent 提供）。
4. 实时对话走 WebSocket；支持停止生成；权限以聊天气泡卡片确认。
5. 工作区「删除」为归档，历史会话可恢复。

### 1.2 首期范围外（避免范围膨胀）

- 不做登录 / 多用户（本机单用户网关）。
- Mode / Model / Thought **仅来自 ACP**，禁止前端写死 plan/build 等枚举。
- 工具调用以简单展示为主；富卡片、虚拟列表、深色主题等后置。
- 工作区只做归档，不做「连会话一起物理清空」。
- 空会话暂不自动清理（允许存在）。
- 不做定时任务、技能市场、SSH 多主机等。

---

## 2. 产品决策一览

| 主题 | 定稿 |
|------|------|
| 默认工作区 | `config.toml` 的 `session.default_cwd`；UI 展示「默认工作区」 |
| 路径语义 | **后端机器**上的路径；相对路径由后端解析为绝对路径 |
| 建 Session | **新建时立即创建**（可马上展示 model / mode / thought_level） |
| 新建交互 **D1** | 侧栏「新建会话」：仅一个可用 Agent 则直接创建；多个则主区出 Agent Tab，点选后立即 `POST /sessions` |
| Agent | 仅新建时可选；**已有会话不可换 Agent** |
| 会话配置 UI | 展示 **model + mode + thought_level**（ACP 有则显，无则藏） |
| 权限 UI | 聊天流内 **权限卡片 + 按钮**；**不自动超时** |
| 工作区删除 | **归档隐藏**；同路径再添加 → 解除归档并展示历史会话 |
| 侧栏 | 多 Workspace 分组；**默认全部展开**（可本地记折叠） |
| WebSocket | **单连接** + 消息携带 **DB `sessionId`** |
| 停止生成 | 首期支持（WS `cancel`） |

---

## 3. 领域模型

```text
Workspace（工作区 = Agent cwd）
  id, path, name, lastUsed, archived, timestamps
  └── Session（对话窗口）
        id, workspaceId, agentId（创建后锁定）, acpSessionId
        title, status
        └── Message
              role: user | assistant | system
              content, events?, timestamps
```

| 概念 | 说明 |
|------|------|
| Workspace | 代码项目目录，**不是** `$ZACP_DATA` |
| Session | 侧栏一个「窗口」= DB 一行 + 一个 ACP session |
| Agent | `config.toml` `[[agents]]` 中 enabled 的 ACP 入口 |
| configOptions | ACP `session/new` 返回；含 model / mode / thought_level 等 category |
| 对外 sessionId | 一律使用 **数据库 Session.id**（字符串化）；内部映射 `acpSessionId` |

---

## 4. 信息架构与交互

### 4.1 布局

在 `docs/frontend/chat-shell-ui-design.md` 壳层基础上，侧栏改为 **Workspace 分组 + 其下 Session 列表**。

```text
┌──────────────┬──────────────────────────────────────┐
│ 侧栏         │ 主区                                  │
│ [添加工作区]  │ Header: 标题 · Agent(只读) · 状态    │
│ [新建会话]    │ MessageList（流式 + 权限卡片）        │
│              │ Composer:                             │
│ Workspace A▼ │   [Model] [Mode] [Thought]            │
│   · 会话 1   │   输入框 · [停止|发送]                │
│   · 会话 2   │                                      │
│ Workspace B▼ │                                      │
│   · 会话 3   │                                      │
│ ─────────    │                                      │
│ 用户 / 设置  │                                      │
└──────────────┴──────────────────────────────────────┘
```

### 4.2 路由

| 路径 | 含义 |
|------|------|
| `/` | 壳层；无选中会话时欢迎/空态（可含多 Agent 时的选择 UI） |
| `/sessions/:id` | 当前会话主区 |

设置：侧栏底部抽屉；不强绑独立路由。

### 4.3 主路径

1. 进入应用 → `GET /agents`、`GET /workspaces`（含默认工作区、仅未归档）。
2. 当前工作区：`localStorage` 记忆，否则默认工作区。
3. **新建会话（D1）**  
   - 可用 Agent 数 = 1 → 立即 `POST /sessions`（当前 workspace + 该 agent）。  
   - 可用 Agent 数 > 1 → 主区展示 Agent Tabs，用户点击某一 Tab → 立即 `POST /sessions`。  
   - 创建成功 → 路由 `/sessions/:id`，用返回的 `configOptions` 渲染三个下拉。
4. 发送消息 → WS `prompt` → `event` 流式 → `turn.done`；消息落库。
5. 权限 → `permission.request` → 流内卡片 → 用户点选项 → WS `permission`。
6. 停止 → WS `cancel`；若存在 pending 权限，按取消/拒绝结束等待。
7. 添加工作区 → 用户输入后端路径 → `POST /workspaces` → 校验失败提示；成功则侧栏出现（或解除归档）。
8. 归档工作区 → 侧栏移除该组；同路径再添加 → 恢复并列出历史会话。

### 4.4 Composer 配置项（B1 + 思考等级）

从会话的 `configOptions` 中按 `category` 筛选：

| category | UI |
|----------|-----|
| `model` | Model 下拉 |
| `mode` | Mode 下拉 |
| `thought_level` | 思考等级下拉 |

- 无对应项或 options 为空 → **隐藏**该控件。  
- 优先使用 ACP **Session Config Options**；若仅有旧版 `modes` 而无 `configOptions`，Mode 控件回退到 `modes.availableModes`。  
- 切换时调用 set_config（REST 或 WS）；以返回的**完整** `configOptions` 刷新 UI。  
- 其它 category（如 `model_config`）首期不展示。

### 4.5 权限卡片

- 插入消息流（或贴在列表底部上方），不挡全屏的居中 Modal。
- 按钮文案与 `options[]` 一致（用 Agent 返回的 name）。
- 点击后卡片禁用，发送 `permission`；**不设自动超时**（C1）。
- `session.auto_approve = true` 时后端直接放行，可不推卡片。

### 4.6 侧栏展开（D）

- 默认 **全部 Workspace 分组展开**。  
- 折叠状态可写 `localStorage`（实现时可选）。

---

## 5. 后端契约

### 5.1 Workspace

| 操作 | 行为 |
|------|------|
| 确保默认工作区 | 启动或首次 list 时：`path = abs(session.default_cwd)` 的记录存在；可标记或命名为默认工作区 |
| `GET /workspaces` | 默认仅 `archived = false`，按 `lastUsed` 降序 |
| `POST /workspaces` | body: `{ "path", "name?" }`；校验存在且为目录；同 path 已归档 → **解除归档 + Touch**；同 path 活跃 → Touch 返回 |
| 归档 | `DELETE /workspaces/:id` **或** 显式 `POST /workspaces/:id/archive`：只设 archived，**不删** Session/Message |
| 说明 | 文档不得再写「删除工作区级联删除会话消息」 |

建议模型字段：`archived bool`（或沿用软删 + Unscoped 恢复；**推荐显式 `archived`**，语义更清晰）。

### 5.2 Agents

| 操作 | 行为 |
|------|------|
| `GET /agents` | 返回 registry 中 enabled 的 agent：`agentId`, `name`, `running` 等 |
| 不暴露 | 误导性的「当前唯一 sessionId」字段（多会话下无意义）；或明确文档为调试用 |

### 5.3 Session

| 操作 | 行为 |
|------|------|
| `POST /sessions` | `{ workspaceId, agentId, title? }` → 启动 agent（若需要）→ ACP `session/new`(cwd=workspace.path) → 落库 → **响应含 `session` + `configOptions`（+ 兼容 `modes`）** |
| `GET /sessions/:id` | 详情；可附带当前缓存的 config 状态（若有） |
| `GET /workspaces/:id/sessions` | 该工作区下会话列表 |
| `DELETE /sessions/:id` | 软删会话；**禁止**因此 `StopAgent` 整个进程 |
| 配置切换 | `POST /sessions/:id/config` body: `{ configId, value }` → ACP `session/set_config_option`；响应完整 `configOptions`。仅 modes 时走 `set_mode` |

### 5.4 Messages（REST）

| 操作 | 行为 |
|------|------|
| `GET /sessions/:id/messages` | 历史；分页参数与实现统一为 `limit`/`offset`（或统一改 page，**以代码为准并改文档**） |
| 发送 | **主路径不走**同步 `POST .../messages` 等整轮；以 WS 为准。REST 发送可保留兼容 demo |

### 5.5 WebSocket

**端点**：`GET /api/v1/ws`（单连接，多会话复用）

**客户端 → 服务端**

| type | 字段 | 说明 |
|------|------|------|
| `prompt` | `sessionId`（DB）, `message` | 发一轮用户消息 |
| `cancel` | `sessionId` | 停止当前生成 |
| `permission` | `permissionId`, `optionId` | 权限选择结果 |
| `set_config` | `sessionId`, `configId`, `value` | 可选；与 REST 二选一或并存 |
| `ping` | — | 心跳 |

**服务端 → 客户端**

| type | 字段 | 说明 |
|------|------|------|
| `event` | `sessionId`, `event` | 流式/工具等 |
| `turn.done` | `sessionId`, `reply`, `stopReason` | 一轮结束 |
| `permission.request` | `sessionId`, `permissionId`, `toolCall`, `options` | 渲染权限卡片 |
| `config_option_update` | `sessionId`, `configOptions` | Agent 主动变更 |
| `error` | `sessionId?`, `code`, `message` | 错误 |
| `pong` | — | 心跳响应 |

**不变量**

1. 对外 `sessionId` = DB id；内部解析 `agentId` + `acpSessionId`。  
2. `prompt`：落库 user 消息；流式推送；`turn.done` 时落库 assistant（及 events 摘要）。  
3. `RequestPermission`：非 auto_approve 时 **阻塞等待** 对应 `permission`（或 cancel 导致的拒绝）；禁止静默 cancel。  
4. 事件路由按会话隔离，禁止多会话串流。  
5. `cancel` 取消 ACP 当前 prompt，并解除权限等待（拒绝/取消语义）。

### 5.6 权限桥接（后端实现要点）

- `acp/client.Bridge.RequestPermission`：分配 `permissionId`，经 WS 推送 `permission.request`，在 channel/map 上等待结果。  
- 超时：**首期不自动超时**；进程关闭或 WS 断开时取消等待并 deny/cancel。  
- `auto_approve`：保持现有自动选 allow 逻辑。

### 5.7 ACP 配置透传

| 步骤 | 行为 |
|------|------|
| `session/new` | 保留 `NewSessionResponse.ConfigOptions` 与 `Modes`，返回给前端 |
| `session/set_config_option` | manager 封装 SDK 方法 |
| `session/set_mode` | 兼容仅支持旧 modes 的 Agent |
| `config_option_update` / `current_mode_update` | SessionUpdate 中解析并转发 WS |

---

## 6. 前端模块

| 模块 | 职责 |
|------|------|
| `stores/workspace` | 列表、当前 id、添加、归档、默认工作区 |
| `stores/session` | 按 workspace 分组、当前、创建（D1）、删除 |
| `stores/chat` | messagesBySessionId、流式尾、pendingPermission、isStreaming |
| `stores/agent` | agents 缓存 |
| `composables/useAcpSocket` | 单例 WS、重连、按 type 分发到 store |
| `api/workspaces.ts` 等 | REST，路径经 `http` 封装 |
| 组件 | AppShell、侧栏分组、Composer、PermissionCard、MessageList、SettingsDrawer |

技术约束（已有约定）：

- Vue 3 + Naive UI + Tailwind + Pinia + vue-i18n  
- 包管理仅 Bun  
- HTTP 仅 `src/api` 的 fetch 封装；WS 原生 WebSocket + `wsUrl()`

---

## 7. 错误与边界

| 场景 | 处理 |
|------|------|
| 路径无效/不存在 | 400 + 可读错误，不创建工作区 |
| 工作区目录后来被删 | 创建会话/发送时失败提示 |
| Agent 启动失败 | 创建会话失败，toast/错误条 |
| WS 断线 | 自动重连；进行中提示；历史仍 REST 可拉 |
| 无 configOptions | 三个配置控件全藏，仍可对话 |
| 空会话 | 允许保留（A2） |
| 切换 config 失败 | toast，保持旧 UI 状态 |

---

## 8. 实现分期

| 阶段 | 内容 | 产出 |
|------|------|------|
| **B0** | 默认工作区、归档/恢复、创建会话返回 configOptions、set_config、WS 闭环（session 映射、落库、权限等待、cancel）、修 DeleteSession 误停 Agent、事件按会话隔离 | 后端可联调 |
| **F0** | AppShell + 多 Workspace 侧栏 + 路由 | 静态/半静态壳 |
| **F1** | REST：agents / workspaces / sessions / config 下拉；D1 新建 | 真数据导航 |
| **F2** | WS 流式 + 停止 + 权限卡片 | 完整对话闭环 |
| **F3** | 标题截断、折叠记忆、API 文档与代码对齐、体验打磨 | 可发布体验 |

建议 **B0 与 F0 并行**；F1/F2 依赖 B0 关键接口就绪。

---

## 9. 与现有代码差距（实现清单摘要）

| 区域 | 现状 | 规格要求 |
|------|------|----------|
| 默认工作区 | 仅有 `default_cwd` 配置，无 DB 默认记录 | 确保 Workspace 行存在 |
| 删除工作区 | GORM 软删，再添加同 path 可能无法恢复会话 | 归档 + 同 path 恢复 |
| CreateSession 响应 | 无 configOptions | 透传 ACP 配置 |
| set_config / set_mode | 无 | 新增 |
| WS | 无可靠 DB session 绑定；prompt 未落库；permission TODO | 全闭环 |
| RequestPermission | 非 auto 直接 cancel | 等待 UI |
| DeleteSession | 会 StopAgent | 只处理该会话 |
| API 文档 | page/pageSize、级联删除等与代码不一致 | 实现时同步文档 |

---

## 10. 验收标准（首期）

1. 配置 `default_cwd` 后，侧栏出现默认工作区，其 path 正确。  
2. 输入存在的后端路径可添加工作区；不存在则失败提示。  
3. 归档工作区后侧栏消失；同 path 再添加可见历史会话。  
4. D1：单 Agent 一键新建；多 Agent Tab 点选即建会话，并显示 Agent 返回的 model/mode/thought（有则显）。  
5. 会话内不可换 Agent；可切换 model/mode/thought（Agent 支持时）。  
6. WS 多轮流式对话；停止按钮中断生成。  
7. 权限以流内卡片确认后 Agent 继续；不点则保持等待（不自动超时）。  
8. 刷新后工作区/会话/历史消息可恢复（依赖 REST + DB）。

---

## 11. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-08-04 | 初稿定稿：工作区归档、A2 立即建会话、D1 新建、权限卡片 B、configOptions 三控件、单 WS、停止生成 |

---

*实现以本规格与代码为准；若实现偏离，先更新本文件再改代码。*
