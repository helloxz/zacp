# zacp 前端开发说明

> 面向后续实现与联调的参考文档。  
> 工程约定总览见仓库根目录 `AGENTS.md`；本文聚焦 **环境变量、HTTP 封装库、WebSocket 地址、调用约定**。

---

## 1. 技术栈与命令

| 项 | 选择 |
|----|------|
| 框架 | Vue 3 + TypeScript + Vite |
| UI | Naive UI（按需：`unplugin-vue-components` + `NaiveUiResolver`） |
| 样式 | Tailwind CSS v4 |
| 状态 / 路由 | Pinia + Vue Router |
| 国际化 | vue-i18n（`zh-CN` / `en-US`） |
| 包管理 | **仅 Bun**（勿用 npm / pnpm / yarn） |
| HTTP | 原生 `fetch` 封装（`src/api`），**不上 axios** |
| 实时 | 浏览器原生 `WebSocket` |

```bash
cd frontend
bun install
bun run dev       # 0.0.0.0:8681
bun run build
bun run preview   # 0.0.0.0:8681
```

路径别名：`@/*` → `src/*`。

---

## 2. 环境变量与后端域名

### 2.1 文件

| 文件 | 何时加载 | 是否入库 |
|------|----------|----------|
| `.env.development` | `bun run dev` | 是（默认开发配置） |
| `.env.production` | `bun run build` | 是（生产默认） |
| `.env.example` | — | 是（字段说明） |
| `.env.development.local` 等 | 覆盖对应 mode | **否**（本机覆盖） |

Vite **只把以 `VITE_` 开头的变量**暴露给客户端代码。

### 2.2 `VITE_API_BASE_URL`

后端 **HTTP 基础地址**，**不要末尾斜杠**。

| 环境 | 典型值 | 含义 |
|------|--------|------|
| 开发 | `http://192.168.50.20:8680` | 浏览器直连该后端（需后端 CORS） |
| 生产 | *空* | 同源相对路径，由 Nginx 等反代 `/api` |

本机临时覆盖（不改仓库文件）：

```bash
echo 'VITE_API_BASE_URL=http://127.0.0.1:8680' > .env.development.local
```

### 2.3 地址拼接工具（`src/config/env.ts`）

| 导出 | 作用 |
|------|------|
| `apiBaseUrl` | 规范化后的 HTTP 基础 URL（可能为空串） |
| `apiUrl(path)` | `基础URL + /api/v1/...` |
| `wsBaseUrl()` | WebSocket 基础 URL（`http→ws` / `https→wss`；空则用当前页面 host） |
| `wsUrl(path)` | WebSocket 完整地址 |

**业务代码不要手写 host**，只写以 `/` 开头的路径。

---

## 3. HTTP 封装库（`src/api`）

### 3.1 设计目标

1. **自动拼接后端域名**：读取 `VITE_API_BASE_URL`，调用方只传 `/api/v1/xxx`。
2. **薄封装原生 fetch**：JSON、query、取消、统一错误；不引入 axios。
3. **与后端错误约定对齐**：`{ "error": { "code": "...", "message": "..." } }`。
4. **便于扩展**：后续按资源拆 `agents.ts` / `sessions.ts` 等，内部统一走 `http`。

### 3.2 源码位置

```text
frontend/src/api/
├── index.ts      # 统一导出
├── http.ts       # request / http.get|post|put|patch|delete
└── types.ts      # ApiError、RequestOptions
```

### 3.3 导入方式

```ts
import { http, request, ApiError } from '@/api'
// 或
import type { RequestOptions } from '@/api'
```

### 3.4 路径规则

| 写法 | 结果（开发且 base=`http://192.168.50.20:8680`） |
|------|--------------------------------------------------|
| `http.get('/api/v1/agents')` | `http://192.168.50.20:8680/api/v1/agents` |
| `http.get('api/v1/agents')` | 同上（库会补前导 `/`） |

| 写法 | 结果（生产 base 为空） |
|------|------------------------|
| `http.get('/api/v1/agents')` | `/api/v1/agents`（当前站点同源） |

**禁止：**

```ts
// ❌ 不要拼死域名
await fetch('http://192.168.50.20:8680/api/v1/agents')

// ❌ 不要重复拼 base
await http.get(apiBaseUrl + '/api/v1/agents')
```

**推荐：**

```ts
// ✅ 只写后端路径
await http.get('/api/v1/agents')
```

### 3.5 快捷方法

```ts
http.get<T>(path, options?)
http.post<T>(path, options?)
http.put<T>(path, options?)
http.patch<T>(path, options?)
http.delete<T>(path, options?)
```

底层通用方法：

```ts
request<T>(method, path, options?)
// method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
```

泛型 `T` 为**成功时 JSON 解析后的类型**；`204` 或空 body 时为 `undefined`。

### 3.6 `RequestOptions`

| 字段 | 类型 | 说明 |
|------|------|------|
| `query` | `Record<string, string \| number \| boolean \| null \| undefined>` | 查询参数；`null`/`undefined` 的键会忽略 |
| `body` | `unknown` | 默认按 JSON 序列化；`FormData` 则原样发送且不强制 `Content-Type` |
| `headers` | `Record<string, string>` | 额外请求头 |
| `signal` | `AbortSignal` | 取消请求（路由切换、组件卸载、用户停止） |
| `json` | `boolean` | 默认 `true` 解析 JSON；`false` 时返回原始 `Response`（少用） |

### 3.7 使用示例

#### GET 列表

```ts
import { http } from '@/api'

interface AgentItem {
  id: string
  name: string
  enabled: boolean
}

const data = await http.get<{ agents: AgentItem[] }>('/api/v1/agents')
console.log(data.agents)
```

#### GET 带分页 query

```ts
const page = await http.get<{ messages: unknown[]; total: number }>(
  '/api/v1/sessions/1/messages',
  {
    query: {
      page: 1,
      pageSize: 50,
      // 未定义字段不会出现在 URL 上
      cursor: undefined,
    },
  },
)
```

#### POST JSON

```ts
const created = await http.post<{ session: { id: number } }>(
  '/api/v1/sessions',
  {
    body: {
      agentId: 'reasonix',
      workspaceId: 1,
    },
  },
)
```

#### DELETE

```ts
await http.delete(`/api/v1/workspaces/${id}`)
// 成功且无 body 时返回 undefined
```

#### 可取消请求

```ts
const controller = new AbortController()

const p = http.get('/api/v1/agents', { signal: controller.signal })

// 用户离开页面 / 切换会话
controller.abort()

try {
  await p
} catch (e) {
  if (e instanceof DOMException && e.name === 'AbortError') {
    // 主动取消，一般不必弹错误
    return
  }
  throw e
}
```

在 Vue 中常见写法：

```ts
import { onUnmounted } from 'vue'
import { http } from '@/api'

const controller = new AbortController()

onUnmounted(() => controller.abort())

const data = await http.get('/api/v1/agents', { signal: controller.signal })
```

#### 错误处理

后端约定：

```json
{
  "error": {
    "code": "workspace_not_found",
    "message": "workspace not found"
  }
}
```

前端抛出 `ApiError`：

```ts
import { http, ApiError } from '@/api'

try {
  await http.get('/api/v1/workspaces/999')
} catch (e) {
  if (e instanceof ApiError) {
    // e.status  — HTTP 状态码；网络失败时为 0
    // e.code    — 业务 code，如 workspace_not_found；或 http_404 / network_error
    // e.message — 可读信息
    // e.body    — 原始响应体（若有）
    console.error(e.status, e.code, e.message)
  } else {
    throw e
  }
}
```

| 场景 | `status` | `code` 示例 |
|------|----------|-------------|
| 业务 4xx/5xx 且返回约定 JSON | HTTP 码 | 后端 `error.code` |
| HTTP 错误但无约定 JSON | HTTP 码 | `http_502` 等 |
| 断网 / DNS 失败 | `0` | `network_error` |
| 响应非合法 JSON | HTTP 码 | `invalid_json` |
| `AbortController.abort()` | — | 抛出 `DOMException`（`AbortError`），**不是** `ApiError` |

### 3.8 行为细节（实现约定）

1. **默认 `Accept: application/json`**（可被 `headers` 覆盖）。
2. **非 GET 且 body 为普通对象**：自动 `Content-Type: application/json` 并 `JSON.stringify`。
3. **body 为 `FormData`**：不设置 `Content-Type`，由浏览器带 boundary。
4. **`res.ok === false`**：进入错误解析并 `throw ApiError`。
5. **`204` 或 `Content-Length: 0` 或空 body**：成功返回 `undefined`。
6. **不在封装层做鉴权刷新 / 全局 toast**：由调用方或后续拦截层处理，保持库职责单一。

### 3.9 推荐业务分层

```text
src/api/
├── http.ts / types.ts / index.ts   # 基础设施（本库）
├── agents.ts                       # 示例：export function listAgents() { return http.get(...) }
├── sessions.ts
└── workspaces.ts
```

```ts
// src/api/agents.ts
import { http } from '@/api'

export function listAgents(signal?: AbortSignal) {
  return http.get<{ agents: { id: string; name: string }[] }>('/api/v1/agents', {
    signal,
  })
}
```

页面 / Pinia 只依赖 `listAgents()`，不直接散落路径字符串（路径变更时改一处即可）。

### 3.10 与 Vite 开发代理的关系

`vite.config.ts` 中可配置将 `/api` 代理到本机后端。

- 当 **`VITE_API_BASE_URL` 为完整 URL**（当前开发默认）：请求**直连该地址**，不经过 Vite 代理。
- 当 **`VITE_API_BASE_URL` 为空**：请求相对路径 `/api/...`，开发期可走 Vite 代理，生产走同域反代。

两种模式都通过同一套 `http.get('/api/v1/...')` 调用，**业务代码无需分支**。

---

## 4. WebSocket 地址

会话流式、权限、取消等走 **原生 WebSocket**，不经过 `http` 封装。

```ts
import { wsUrl } from '@/config/env'

// 开发：ws://192.168.50.20:8680/api/v1/ws
// 生产（base 空）：ws(s)://当前页面host/api/v1/ws
const ws = new WebSocket(wsUrl('/api/v1/ws'))

ws.onmessage = (ev) => {
  const msg = JSON.parse(String(ev.data))
  // 按 type 分发…
}

ws.send(JSON.stringify({ type: 'prompt', /* ... */ }))
```

协议字段、心跳与重连建议抽到 `composables/useAcpSocket.ts`（实现阶段再写）。  
**约定**：与 HTTP 使用同一套 `VITE_API_BASE_URL` 推导逻辑，避免 REST 打 A 机、WS 打 B 机。

---

## 5. 类型与 JSON 约定

- 前后端 JSON 字段统一 **camelCase**。
- 后端错误结构优先解析 `error.code` / `error.message`。
- 前端 `types/` 与后端 model 变更时同步更新，避免静默字段漂移。

---

## 6. 相关文档索引

| 文档 | 内容 |
|------|------|
| 本文 `docs/frontend/development.md` | 环境变量、HTTP 库、WS 地址 |
| `docs/frontend/chat-shell-ui-design.md` | 侧栏 + 对话壳层 UI 设计（简化版） |
| `frontend/README.md` | 脚手架、命令、目录速览 |
| `AGENTS.md` | 全仓库技术选型与分层约定 |

---

## 7. 变更记录

| 日期 | 说明 |
|------|------|
| 2026-08-04 | 初版：环境变量 + `src/api` HTTP 封装使用说明 + WebSocket 地址约定 |

---

*实现以代码为准；行为变更时请同步更新本文。*
