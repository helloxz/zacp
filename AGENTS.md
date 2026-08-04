# AGENTS.md — zacp 项目协作指南

本文档面向在本仓库中工作的编码 Agent / 开发者，说明项目目标、目录约定、技术选型与实现边界。修改代码前请先阅读。

---

## 1. 项目是什么

**zacp** 是一个 **ACP（Agent Client Protocol）多 Agent Web 网关**：

- 通过 Web UI 接入多种支持 ACP 协议的 Agent 工具（如 Pi Agent、Reasonix、Grok 等）
- 后端以 **ACP Client** 身份连接各 Agent（本地 stdio 子进程或远程通道）
- 向前端提供 HTTP API + WebSocket，用于会话管理、消息流式输出、权限确认等

协议文档：https://agentclientprotocol.com  
Go SDK：https://github.com/coder/acp-go-sdk（模块路径 `github.com/coder/acp-go-sdk`）

---

## 2. 技术栈

| 层级 | 技术 | 说明 |
|------|------|------|
| 后端 | Go（当前 go.mod 为 1.25.x） | 模块名 `github.com/zacp/zacp` |
| HTTP | Gin `v1.12.x` | REST API、路由、中间件 |
| WebSocket | **`github.com/coder/websocket`** | 浏览器会话主通道（流式输出、权限、取消）；实现放在 `internal/ws` |
| ACP | `github.com/coder/acp-go-sdk` `v0.13.5` | Client 侧连接、会话、Prompt、SessionUpdate |
| 前端 | 待脚手架（放在 `frontend/`） | 框架选定后再写具体约定；浏览器原生 `WebSocket` 即可 |
| 部署 | 根目录 `Dockerfile`、`deployments/`、`scripts/` | 镜像与运维脚本 |

**实时通信选型（已定）：**

- **选用 WebSocket**，不用 SSE 作为会话主通道（连续对话 + 权限回传 + 取消需要双向）。
- **Go 库固定为 `github.com/coder/websocket`**（原 `nhooyr/websocket`）。不要引入 `gorilla/websocket`、`gobwas/ws`、Socket.IO 等替代实现，除非有充分理由并先更新本文档。
- Gin 侧在 handler 内 `websocket.Accept` 升级连接；升级后勿再写 `c.JSON`。
- REST 仍用于健康检查、agent 列表、配置等非实时接口；会话实时交互走 WS。

**本仓库角色定位：**

- 后端实现 **ACP Client**（`acp.Client` / `acp.NewClientSideConnection`），**不要**默认把 zacp 做成 ACP Agent 服务端，除非有明确需求。
- 典型流程：`Initialize` → `NewSession`（或 `LoadSession` / `ResumeSession`）→ `Prompt`，并通过 `SessionUpdate` 将流式更新推给 Web UI。

---

## 3. 目录结构（必须遵守）

```
zacp/
├── AGENTS.md                 # 本文件：Agent 协作约定
├── README.md                 # 人类可读项目说明
├── Dockerfile                # 镜像构建（优先后端）
├── backend/                  # ★ 全部后端 Go 代码
│   ├── cmd/server/           # 进程入口，保持薄：组装依赖 + 启动
│   ├── configs/              # 配置样例（如 config.example.yaml）
│   ├── internal/             # 私有业务代码（禁止被外部模块 import）
│   │   ├── acp/
│   │   │   ├── client/       # ACP Client 连接封装（stdio/管道等）
│   │   │   ├── manager/      # 多 Agent / 多会话生命周期
│   │   │   └── providers/    # 各 Agent 启动参数与适配（pi、reasonix、grok…）
│   │   ├── api/
│   │   │   ├── handlers/     # HTTP Handler
│   │   │   ├── middleware/   # 中间件
│   │   │   └── router/       # 路由注册
│   │   ├── config/           # 配置加载与校验
│   │   ├── model/            # DTO / 领域模型
│   │   ├── service/          # 业务编排（对接 manager / 持久化等）
│   │   └── ws/               # WebSocket：向浏览器推送 session/update
│   ├── pkg/                  # 可被多处复用的轻量工具库（谨慎使用）
│   ├── go.mod
│   └── go.sum
├── frontend/                 # ★ 全部前端代码
├── scripts/                  # Shell 脚本（开发、构建、发布）
├── deployments/              # compose / k8s 等部署清单
└── docs/                     # 设计文档、协议笔记
```

### 放置规则

| 内容 | 位置 |
|------|------|
| Go 业务与依赖 | **仅** `backend/` |
| Web UI 源码 | **仅** `frontend/` |
| 可执行脚本 | `scripts/` 或根目录（根目录仅放极少数全局脚本） |
| Docker / 部署 | 根目录 `Dockerfile` 或 `deployments/` |
| 含密钥的本地配置 | **不要提交**；参考 `configs/config.example.yaml`，本地用 `config.yaml` / `.env`（已在 `.gitignore`） |

**禁止：**

- 在仓库根目录散落 Go 源码或 `go.mod`
- 在 `backend/` 外写业务 Go 包
- 把前端构建产物、密钥、个人 `config.yaml` 提交进 Git

---

## 4. 后端分层约定

按依赖方向从上到下：

```
cmd/server  →  api (router/handlers)  →  service  →  acp/manager|client|providers
                     ↓                      ↓
                    ws                   config / model
```

- **`cmd/server`**：只做启动与依赖注入，不写复杂业务。
- **`api/handlers`**：解析请求、校验入参、调用 `service`，返回 JSON；不直接 `exec` Agent 进程。
- **`service`**：用例编排（创建会话、发 Prompt、取消、权限回传）。
- **`acp/client`**：封装 `acp-go-sdk` 的连接与回调（实现 `acp.Client`：权限、读/写文件、终端等）。
- **`acp/manager`**：管理多个 provider 连接、session 映射、并发与清理。
- **`acp/providers`**：各工具的启动命令、参数、环境变量差异；新增 Agent 优先在此扩展，而不是改核心 manager 逻辑。
- **`ws`**：把 ACP `SessionUpdate`（消息块、工具调用、计划等）转成前端可消费的事件。

新增 Agent 接入清单：

1. 在 `configs` 增加 provider 配置项  
2. 在 `internal/acp/providers` 增加启动/连接适配  
3. 在 manager 注册  
4. 必要时扩展 API / 前端展示  

---

## 5. 常用命令

```bash
# 启动后端
cd backend && go run ./cmd/server
# 或
./scripts/dev-backend.sh

# 健康检查
curl http://127.0.0.1:8080/healthz

# 增加依赖（在 backend 目录）
cd backend
go get <module>@<version>
go mod tidy

# 编译检查
cd backend && go build -o /tmp/zacp ./cmd/server

# 测试（有测试后）
cd backend && go test ./...
```

默认监听端口：**8080**（后续以配置文件为准）。

---

## 6. 依赖与版本

当前直接依赖（见 `backend/go.mod`）：

- `github.com/gin-gonic/gin` — HTTP
- `github.com/coder/acp-go-sdk` — ACP

要求：

- 新增依赖需有明确用途；优先标准库与已有依赖。
- 升级 `acp-go-sdk` 时注意协议版本与 API 变更，并跑通至少一条 agent 连接路径。
- 不要随意改 `go` 版本字段，除非与运行环境对齐并验证构建。

---

## 7. 编码风格

### Go

- 遵循 `gofmt` / 常规 Go 习惯；导出符号写清注释。
- 错误要包装上下文：`fmt.Errorf("...: %w", err)`。
- 带超时的操作使用 `context.Context`；Agent 子进程、长连接必须可取消、可清理（防僵尸进程）。
- 并发访问 session / connection 时使用合适的同步手段，并在连接关闭时释放资源。
- `internal/` 下包名简短清晰：`handlers`、`manager`、`providers` 等。
- 日志：初期可用标准库 `log/slog` 或 `log`；避免在热路径刷屏敏感信息（Token、密钥、完整用户文件内容）。

### API / 前端协作

- REST 路径建议前缀 `/api/v1/...`。
- 错误响应结构尽量统一，例如：`{ "error": { "code": "...", "message": "..." } }`。
- 前后端 JSON 字段命名统一 **camelCase**（与前端习惯对齐）。

### WebSocket 约定（`internal/ws` + `github.com/coder/websocket`）

- **入口路由建议**：`GET /api/v1/ws`（Gin 注册，handler 内调用 `websocket.Accept`）。
- **依赖**：`go get github.com/coder/websocket`；读写优先使用带 `context.Context` 的 API（`Read` / `Write`），与会话取消、超时一致。
- **载荷**：文本帧 + JSON 消息；自行定义 `type` 字段，不引入 Socket.IO 等上层协议。
- **建议消息类型（可演进，实现时对齐前后端）**：
  - 客户端 → 服务端：`prompt`、`cancel`、`permission`（权限选择结果）、`ping`
  - 服务端 → 客户端：`session.ready`、`event`（对应 ACP `session/update`）、`turn.done`、`permission.request`、`error`、`pong`
- **会话模型（最小）**：一条浏览器 WS 连接绑定一个后端 ACP session；多轮对话在同一连接上多次 `prompt`。
- **生产注意**：校验 `Origin`、鉴权（token / ticket）、心跳与断线重连；关闭时释放 ACP 会话与 agent 子进程相关资源。
- **禁止**：用长轮询或「等整轮 Prompt 结束再一次性 HTTP 返回」作为 Web UI 主路径（demo API 可以保留，UI 必须以流式事件为准）。

### 前端（脚手架后补充细则）

- 所有 UI 代码只在 `frontend/`。
- 浏览器使用原生 `WebSocket` 即可，无需额外 WS SDK。
- 不要在 `backend` 内嵌大型前端工程源码；静态资源托管可在集成阶段再定。

---

## 8. ACP 实现注意点

- 实现 **Client** 接口时，权限请求（`RequestPermission`）应能传到 Web UI，由用户选择后再返回 outcome，**不要**在服务端无条件一律自动 allow（可配置「开发模式自动允许」）。
- 文件读写路径应限制在会话工作区（cwd）内，防止路径穿越。
- stdio 连接：正确连接子进程 stdin/stdout，处理 stderr 日志，进程退出时关闭会话并通知前端。
- 扩展方法（以 `_` 开头）按 SDK 文档处理；未知扩展通知可忽略，勿与标准方法冲突。
- 与具体 Agent（Pi / Reasonix / Grok）相关的 CLI 参数差异放在 `providers`，保持 core 协议层通用。

---

## 9. 配置与安全

- 样例配置：`backend/configs/config.example.yaml`
- 本地真实配置勿提交（见 `.gitignore`）。
- 不在日志、错误信息、前端接口中泄露 API Key、Cookie、本机绝对路径中的敏感部分。
- Docker 镜像默认最小权限；生产环境使用 `GIN_MODE=release`。

---

## 10. 文档与提交

- 架构、重大设计写入 `docs/`。
- 用户可见的使用说明更新 `README.md`；**Agent 协作约定**更新本文件 `AGENTS.md`。
- 提交信息简洁说明「改了什么、为什么」；一次提交聚焦单一意图。
- 完成功能前：确保 `go build ./...`（或至少 `./cmd/server`）通过；有测试则跑 `go test ./...`。

---

## 11. 当前状态（脚手架阶段）

已完成：

- monorepo 目录骨架
- Gin + acp-go-sdk 依赖安装
- reasonix `--acp` 最小 demo（终端 REPL + `POST /api/v1/chat`）
- 实时通道选型：WebSocket + **`github.com/coder/websocket`**（见上文）

尚未完成（后续实现时请按本文分层落地）：

- 基于 `coder/websocket` 的 `internal/ws` 与 `/api/v1/ws` 流式会话
- 多 agent manager 与完整 REST/WS 消息契约
- 前端脚手架与会话 UI
- 配置加载、鉴权、部署编排

---

## 12. 给 Agent 的简短检查清单

在动手改代码前确认：

1. 改动是否落在正确目录（backend / frontend / scripts / deployments）？
2. 是否把 Agent 差异关在 `providers`，而不是污染通用 manager？
3. 子进程 / 连接是否有生命周期与错误处理？
4. 是否引入了不必要的依赖或破坏了 `go.mod`？
5. 是否更新了必要的 README / 配置样例 / 本 AGENTS.md？

---

*文档语言：中文。若与代码不一致，以代码与 `go.mod` 为准，并应及时回写本文档。*
