# ZACP Backend API 文档

> 本文档供前端开发参考，描述后端 REST API 和 WebSocket 协议。

## 基础信息

- **Base URL**: `http://localhost:8680/api/v1`
- **WebSocket**: `ws://localhost:8680/api/v1/ws`
- **Content-Type**: `application/json`
- **时间格式**: ISO 8601 (UTC)

---

## 1. Agent 管理

### 获取 Agent 列表

```
GET /agents
```

**响应**:
```json
[
  {
    "agentId": "reasonix",
    "name": "Reasonix",
    "running": true,
    "sessionId": "sess_xxx"
  }
]
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
[
  {
    "id": 1,
    "path": "/home/user/project",
    "name": "my-project",
    "lastUsed": "2025-01-21T10:30:00Z",
    "createdAt": "2025-01-20T08:00:00Z",
    "updatedAt": "2025-01-21T10:30:00Z"
  }
]
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
  "agentId": "reasonix",
  "title": "新会话"  // 可选
}
```

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
[
  {
    "id": 1,
    "workspaceId": 1,
    "agentId": "reasonix",
    "title": "会话标题",
    "status": "active",
    "createdAt": "2025-01-21T10:30:00Z"
  }
]
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
  "id": 1,
  "sessionId": 1,
  "role": "user",
  "content": "帮我写一个函数",
  "createdAt": "2025-01-21T10:30:00Z"
}
```

**说明**: 
- 此接口为同步接口，会等待 Agent 响应完成后返回
- 实时流式响应请使用 WebSocket

### 获取会话消息列表

```
GET /sessions/:id/messages?page=1&pageSize=50
```

**查询参数**:
- `page`: 页码，默认 1
- `pageSize`: 每页数量，默认 50

**响应**:
```json
[
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
]
```

**消息角色 (role)**:
- `user`: 用户消息
- `assistant`: AI 助手回复
- `system`: 系统消息

---

## 5. WebSocket 实时通信

### 连接

```
ws://localhost:8080/api/v1/ws
```

### 消息格式

所有消息均为 JSON，包含 `type` 字段标识消息类型。

### 客户端 → 服务端

#### 发送消息 (prompt)
```json
{
  "type": "prompt",
  "sessionId": "1",
  "message": "帮我写代码"
}
```

#### 取消操作 (cancel)
```json
{
  "type": "cancel",
  "sessionId": "1"
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
    "type": "agentMessageChunk",
    "content": { "text": "你好" }
  }
}
```

**事件类型**:
- `agentMessageChunk`: Agent 消息片段（流式文本）
- `toolCall`: 工具调用开始
- `toolCallUpdate`: 工具调用更新
- `plan`: 计划步骤
- `turn_complete`: 一轮对话完成

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
  "permissionId": "perm_xxx",
  "toolCall": { "name": "write_file", "args": {...} },
  "options": [
    { "id": "allow", "label": "允许" },
    { "id": "deny", "label": "拒绝" }
  ]
}
```

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
| events | string | 完整事件 JSON（工具调用等） |
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
addr = ":8080"

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
