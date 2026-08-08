# ZACP Backend API 文档

> 本文档供前端开发参考，描述后端 REST API 和 WebSocket 协议。

## 基础信息

- **Base URL**: `http://localhost:8680/api/v1`
- **WebSocket**: `ws://localhost:8680/api/v1/ws`
- **Content-Type**: `application/json`
- **时间格式**: ISO 8601 (UTC)
- **CORS**: 后端已全局启用跨域中间件（开发默认 `Access-Control-Allow-Origin: *`，支持 OPTIONS 预检）；前端跨端口直连后端（如 dev 页 `:8681` → API `:8680`）无需额外配置

---

## 1. Agent 管理

### 获取 Agent 列表

```
GET /agents
```

**响应**:
```json
{
  "agents": [
    {
      "agentId": "reasonix",
      "name": "Reasonix",
      "running": true
    }
  ]
}
```

### 获取 Agent 状态

```
GET /agents/:agentId/status
```

---

## 2. 工作目录 (Workspace)

### 获取工作目录列表

```
GET /workspaces
```

**响应**:
```json
{
  "workspaces": [
    {
      "id": 1,
      "path": "/home/user/project",
      "name": "my-project",
      "isDefault": false,
      "archived": false,
      "lastUsed": "2025-01-21T10:30:00Z",
      "createdAt": "2025-01-20T08:00:00Z",
      "updatedAt": "2025-01-21T10:30:00Z"
    }
  ]
}
```

### 创建工作目录

```
POST /workspaces
```

**请求体**:
```json
{
  "path": "/home/user/project",
  "name": "my-project"  // 可选
}
```

**说明**: 后端会验证路径是否存在，不存在返回 400。

### 获取单个工作目录

```
GET /workspaces/:id
```

### 删除工作目录

```
DELETE /workspaces/:id
```

**说明**: 同时删除该工作目录下的所有会话和消息。

---

## 3. 会话 (Session)

### 创建会话

```
POST /sessions
```

**请求体**:
```json
{
  "workspaceId": 1,
  "agentId": "reasonix"
}
```

**说明**: `workspaceId` **可选**（缺省/0 时回退默认工作区：`is_default` 标记 → `session.default_cwd` 路径 → 按 default_cwd 新建）；`agentId` 必填。

**响应**:
```json
{
  "id": 1,
  "workspaceId": 1,
  "agentId": "reasonix",
  "acpSessionId": "acp_sess_xxx",
  "title": "新会话",
  "status": "active",
  "createdAt": "2025-01-21T10:30:00Z",
  "updatedAt": "2025-01-21T10:30:00Z"
}
```

**说明**: 创建会话时会自动启动对应的 Agent 进程（如未启动）并创建 ACP Session。

### 获取会话详情

```
GET /sessions/:id
```

### 获取最近活跃会话（全局列表，侧栏数据源）

```
GET /sessions?limit=50
```

**查询参数**:
- `limit`: 每页数量，默认 50，上限 200

**响应**:
```json
{
  "sessions": [
    {
      "id": 1,
      "workspaceId": 1,
      "agentId": "reasonix",
      "acpSessionId": "acp_sess_xxx",
      "title": "会话标题",
      "status": "active",
      "createdAt": "2025-01-21T10:30:00Z",
      "updatedAt": "2025-01-21T10:35:00Z",
      "workspace": {
        "id": 1,
        "path": "/home/user/project",
        "name": "my-project"
      }
    }
  ]
}
```

### 删除会话

```
DELETE /sessions/:id
```

### 获取工作目录下的会话列表

```
GET /workspaces/:id/sessions
```

**响应**:
```json
{
  "sessions": [
    {
      "id": 1,
      "workspaceId": 1,
      "agentId": "reasonix",
      "title": "会话标题",
      "status": "active",
      "createdAt": "2025-01-21T10:30:00Z"
    }
  ]
}
```

---

## 4. 消息 (Message)

### 发送消息

```
POST /sessions/:id/messages
```

**请求体**:
```json
{
  "content": "帮我写一个函数"
}
```

**响应**:
```json
{
  "message": {
    "id": 1,
    "sessionId": 1,
    "role": "user",
    "content": "帮我写一个函数",
    "createdAt": "2025-01-21T10:30:00Z"
  }
}
```

**说明**: 
- 此接口为同步接口，会等待 Agent 响应完成后返回（返回的 `message` 为 assistant 回复）
- 实时流式响应请使用 WebSocket

### 获取会话消息列表

```
GET /sessions/:id/messages?limit=50&offset=0
```

**查询参数**:
- `limit`: 每页数量，默认 50
- `offset`: 偏移量，默认 0

**响应**:
```json
{
  "messages": [
    {
      "id": 1,
      "sessionId": 1,
      "role": "user",
      "content": "用户消息",
      "createdAt": "2025-01-21T10:30:00Z"
    },
    {
      "id": 2,
      "sessionId": 1,
      "role": "assistant",
      "content": "助手回复",
      "events": "{...}",
      "createdAt": "2025-01-21T10:30:05Z"
    }
  ],
  "total": 2,
  "limit": 50,
  "offset": 0
}
```

**消息角色 (role)**:
- `user`: 用户消息
- `assistant`: AI 助手回复
- `system`: 系统消息

---

## 4.1 会话配置项 (Config Options)

> 来自 ACP `session/new` 响应的 `configOptions`（模型 / 思考强度 / mode 等）。
> Agent 不支持时返回空数组，前端隐藏配置 UI。

### 获取会话配置项

```
GET /sessions/:id/config-options
```

**响应**:
```json
{
  "configOptions": [
    {
      "id": "model",
      "name": "模型",
      "category": "model",
      "type": "select",
      "currentValue": "gpt-4o",
      "options": [
        { "value": "gpt-4o", "name": "GPT-4o" },
        { "value": "gpt-4o-mini", "name": "GPT-4o mini" }
      ]
    },
    {
      "id": "thought_level",
      "name": "深度思考",
      "type": "boolean",
      "currentValue": true
    }
  ]
}
```

### 设置会话配置项

```
POST /sessions/:id/config-options
```

**请求体**:
```json
{ "optionId": "model", "valueId": "gpt-4o" }
```

**说明**: `select` 型传 `valueId`（选项 value）；`boolean` 型传 `"true"` / `"false"`。后端同步调 ACP `session/set_config_option` 并回写 `currentValue`。

---

## 5. WebSocket 实时通信

### 连接

```
ws://localhost:8680/api/v1/ws
```

### 消息格式

所有消息均为 JSON，包含 `type` 字段标识消息类型。

### 客户端 → 服务端

#### 发送消息 (prompt)
```json
{
  "type": "prompt",
  "sessionId": "acp_sess_xxx",
  "agentId": "reasonix",
  "message": "帮我写代码"
}
```

**说明**: `sessionId` 为 **ACP session id**（`GET /sessions/:id` 响应的 `acpSessionId`），非数据库 id；`agentId` 用于无绑定连接动态绑定。服务端会先落库用户消息（首条自动生成会话标题），Agent 完成后落库助手回复并广播 `turn.done`。

#### 取消操作 (cancel)
```json
{
  "type": "cancel",
  "sessionId": "acp_sess_xxx",
  "agentId": "reasonix"
}
```

#### 权限响应 (permission)
```json
{
  "type": "permission",
  "permissionId": "perm_xxx",
  "optionId": "allow"
}
```

#### 心跳 (ping)
```json
{
  "type": "ping"
}
```

### 服务端 → 客户端

#### 会话就绪 (session.ready)
```json
{
  "type": "session.ready",
  "sessionId": "1",
  "agentId": "reasonix"
}
```

#### 流式事件 (event)
```json
{
  "type": "event",
  "event": {
    "type": "agent_message",
    "text": "你好"
  }
}
```

**事件类型**（`event.type`，对齐 `internal/acp/client` 的简化事件）:
- `agent_message`: Agent 文本片段（流式文本，前端追加到回复末尾）
- `agent_thought`: Agent 思考片段
- `user_message`: 用户消息回显
- `tool_call`: 工具调用开始（含 `toolId` / `title` / `status`）
- `tool_call_update`: 工具调用状态更新
- `plan`: 计划步骤
- `other`: 未识别更新

#### 轮次完成 (turn.done)
```json
{
  "type": "turn.done",
  "reply": "完整回复内容",
  "stopReason": "end_turn"
}
```

#### 权限请求 (permission.request)
```json
{
  "type": "permission.request",
  "permissionId": "perm-1750000000000000000",
  "toolCall": {
    "toolCallId": "call_xxx",
    "title": "写入文件",
    "status": "running"
  },
  "options": [
    { "optionId": "allow", "name": "允许一次", "kind": "allow_once" },
    { "optionId": "deny", "name": "拒绝", "kind": "deny_once" }
  ]
}
```

**说明**: 前端弹窗展示 toolCall 与 options，用户选择后回传 `permission` 消息（`permissionId` + `optionId`）；后端 60s 未收到回传自动取消（Cancelled outcome）。非 `auto_approve` 配置下才走此交互。

#### 错误 (error)
```json
{
  "type": "error",
  "code": "PROMPT_ERROR",
  "message": "错误描述"
}
```

#### 心跳响应 (pong)
```json
{
  "type": "pong"
}
```

---

## 6. 数据模型

### Workspace
| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| path | string | 绝对路径（唯一） |
| name | string | 显示名称 |
| lastUsed | time | 最近使用时间 |
| createdAt | time | 创建时间 |
| updatedAt | time | 更新时间 |

### Session
| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| workspaceId | uint | 所属工作目录 ID |
| agentId | string | Agent 配置 ID |
| acpSessionId | string | ACP 协议层 Session ID |
| title | string | 会话标题 |
| status | string | active / closed / error |
| createdAt | time | 创建时间 |
| updatedAt | time | 更新时间 |

### Message
| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| sessionId | uint | 所属会话 ID |
| role | string | user / assistant / system |
| content | string | 消息文本内容 |
| events | string | 事件时间线 JSON（工具调用等）。**v6 起剥离工具入参/出参**：工具卡详情改由 `toolDetails` 提供；旧的未迁移数据（新前端 + 旧后端组合）仍可从 events 内嵌 input/output 回退 |
| toolDetails | string | 工具详情 JSON（`toolId → {input, output}`，每工具最终一份）。**v6 新增**：历史消息列表瘦身 ~90% 的来源；为空表示该消息无工具调用（或未迁移，前端回退 events） |
| createdAt | time | 创建时间 |

---

## 7. 错误响应

所有错误响应格式:
```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "错误描述"
  }
}
```

**常见错误码**:
- `WORKSPACE_NOT_FOUND`: 工作目录不存在
- `WORKSPACE_PATH_INVALID`: 路径无效或不存在
- `SESSION_NOT_FOUND`: 会话不存在
- `AGENT_NOT_FOUND`: Agent 不存在
- `AGENT_NOT_STARTED`: Agent 未启动

---

## 8. 典型使用流程

1. **获取 Agent 列表**: `GET /agents`
2. **选择/创建工作目录**: `POST /workspaces`
3. **创建会话**: `POST /sessions` (指定 workspaceId + agentId)
4. **建立 WebSocket 连接**: `ws://.../api/v1/ws`
5. **发送消息**: 通过 WebSocket 发送 `prompt` 消息
6. **接收流式响应**: 监听 `event` 消息，处理 `agentMessageChunk`
7. **完成一轮**: 收到 `turn.done` 消息
8. **获取历史消息**: `GET /sessions/:id/messages`

---

## 9. 配置说明

后端配置文件: `~/.zacp/config.toml`

```toml
[server]
addr = ":8680"

[session]
default_cwd = "."
auto_approve = false

[[agents]]
id = "reasonix"
name = "Reasonix"
enabled = true
command = "reasonix"
args = ["--acp"]
```

---

*文档版本: v1.0 | 最后更新: 2025-01-21*
