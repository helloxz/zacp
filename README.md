# zacp

Web UI gateway for multiple **Agent Client Protocol (ACP)** agents (e.g. Pi, Reasonix, Grok).

- **Backend**: Go + [Gin](https://github.com/gin-gonic/gin) + [acp-go-sdk](https://github.com/coder/acp-go-sdk)
- **Frontend**: Web UI (to be scaffolded under `frontend/`)

## Repository layout

```
zacp/
├── backend/                 # Go API server
│   ├── cmd/server/          # process entrypoint
│   ├── configs/             # YAML examples / runtime config
│   ├── internal/
│   │   ├── acp/             # ACP client, manager, provider adapters
│   │   │   ├── client/      # Client-side ACP connection helpers
│   │   │   ├── manager/     # multi-agent session lifecycle
│   │   │   └── providers/   # pi / reasonix / grok / ...
│   │   ├── api/             # HTTP layer (handlers, middleware, router)
│   │   ├── config/          # load & validate config
│   │   ├── model/           # DTOs / domain types
│   │   ├── service/         # business logic
│   │   └── ws/              # WebSocket streaming to Web UI
│   ├── pkg/                 # optional shared libraries
│   ├── go.mod
│   └── go.sum
├── frontend/                # Web UI (framework TBD)
│   ├── public/
│   └── src/
├── scripts/                 # dev / build / release shell scripts
├── deployments/             # Docker, compose, k8s manifests
├── docs/                    # design notes & docs
├── Dockerfile               # (optional) multi-stage image
└── README.md
```

## Demo: 接入 reasonix ACP 并对话

本仓库已包含 **最小可跑 demo**：拉起 `reasonix --acp`，用终端 REPL 或 HTTP API 对话。

### 前置

- 本机可执行 `reasonix`（或设置 `REASONIX_BIN=/path/to/reasonix`）
- 已配置 reasonix 模型密钥（`reasonix doctor` 可检查）

### 方式 A：终端 REPL

```bash
./scripts/demo-chat.sh
# 或
cd backend && go run ./cmd/chat
```

输入消息回车发送；`:exit` 退出，`:cancel` 取消当前回合。

### 方式 B：HTTP API

```bash
./scripts/demo-api.sh
# 默认监听 :8680

curl -s http://127.0.0.1:8680/healthz

curl -s http://127.0.0.1:8680/api/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"你好，用一句话介绍你自己"}'
```

响应示例字段：`sessionId`、`reply`（汇总的 agent 文本）、`events`（含 thought / tool 等）、`stopReason`、`durationMs`。

常用环境变量 / flag：

| 变量 / flag | 说明 |
|-------------|------|
| `REASONIX_BIN` / `-command` | reasonix 二进制路径 |
| `ZACP_CWD` / `-cwd` | Agent 工作目录 |
| `ZACP_ADDR` / `-addr` | HTTP 监听地址（默认 `:8680`） |
| `-yolo` | 自动批准工具权限（默认 true） |

## Backend quick start

```bash
cd backend
go run ./cmd/server
# health: curl http://127.0.0.1:8680/healthz
```

启动参数（均可选，**命令行优先级最高**，缺省按回退链取值）：

| 参数 | 说明 | 回退链 |
|------|------|--------|
| `--addr IP:PORT` | 监听地址（`:8680` 表示所有网卡） | `ZACP_ADDR` → TOML `server.addr` → `:8680` |
| `--data-dir DIR` | `$ZACP_DATA` 状态根目录（数据/配置所在） | `ZACP_DATA` → `~/.zacp` |
| `--config FILE` | 配置文件路径 | `ZACP_CONFIG` → `$ZACP_DATA/config.toml` |

示例：`go run ./cmd/server --addr 127.0.0.1:9000 --data-dir /var/lib/zacp`

Dependencies already pinned in `go.mod`:

| Package | Role |
|---------|------|
| `github.com/gin-gonic/gin` | HTTP API |
| `github.com/coder/acp-go-sdk` | ACP client/agent protocol |

## ACP agents

Backend acts as an **ACP Client**: spawns or connects to agent processes (stdio / remote), runs `Initialize` → `NewSession` → `Prompt`, and streams `session/update` events to the Web UI (typically over WebSocket).

当前 demo 使用 **stdio + 单 session**；权限默认自动 approve（`-yolo`）。

Protocol reference: https://agentclientprotocol.com

## Frontend

Vue 3 + Naive UI + Tailwind CSS，代码在 `frontend/`，包管理与构建一律使用 [Bun](https://bun.sh)（`bun install` / `bun run dev` / `bun run build`）。

## Build & release（单二进制）

```bash
./scripts/build.sh
```

一键构建，产出**单一二进制**（前端产物 + 配置示例全部打包进可执行文件）：

- **前端**：`bun install && bun run build`（产物 `frontend/dist`）
- **打包**：前端产物与配置示例（`backend/configs/config.example.toml`）由 `scripts/build.sh` 拷入 `backend/internal/web/` 后经 `go:embed` 打进后端；未运行 build.sh 时（裸 `go build` / dev 模式）后端自动跳过静态路由，前端由 vite dev server（:8681）独立提供
- **产物**：`backend/bin/zacp-v<版本>-<GOOS>-<GOARCH>`（可用 `GOOS`/`GOARCH` 环境变量交叉编译；Windows 追加 `.exe`）
- **版本号单一来源**：`frontend/package.json` 的 `version` 字段（发布时手动修改），构建时经 `-ldflags` 注入后端 `internal/version`，同时驱动：
  - 二进制包名
  - `zacp --version`（命令行查看版本）
  - `GET /api/v1/version`（前端设置页展示，不再硬编码）

后端启动后访问 `http://<host>:8680/` 即可使用内置 Web UI；未匹配的 API 路径返回 JSON 404，其余路径（history 路由深链）回首页。

## Scripts & deploy

Put shell helpers in `scripts/`, container / compose files in `deployments/` or repo root as preferred.
