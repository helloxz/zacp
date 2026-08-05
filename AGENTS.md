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
| 配置 | **TOML + Viper** | 运行时配置 **`~/.zacp/config.toml`**；库 **`github.com/spf13/viper`**；加载在 `internal/config` |
| 数据库 | **SQLite3 + GORM + 纯 Go 驱动** | ORM：`gorm.io/gorm`；驱动：`github.com/glebarez/sqlite`（底层 `modernc.org/sqlite`，**无 CGO**）；持久化在 `internal/store` |
| ACP | `github.com/coder/acp-go-sdk` `v0.13.5` | Client 侧连接、会话、Prompt、SessionUpdate |
| 前端 | **Vue 3 + Naive UI + Tailwind CSS** | 代码在 `frontend/`；构建建议 Vite；**包管理与脚本一律用 Bun**；实时通道用浏览器原生 `WebSocket` |
| 部署 | 根目录 `Dockerfile`、`deployments/`、`scripts/` | 镜像与运维脚本 |

**实时通信选型（已定）：**

- **选用 WebSocket**，不用 SSE 作为会话主通道（连续对话 + 权限回传 + 取消需要双向）。
- **Go 库固定为 `github.com/coder/websocket`**（原 `nhooyr/websocket`）。不要引入 `gorilla/websocket`、`gobwas/ws`、Socket.IO 等替代实现，除非有充分理由并先更新本文档。
- Gin 侧在 handler 内 `websocket.Accept` 升级连接；升级后勿再写 `c.JSON`。
- REST 仍用于健康检查、agent 列表、配置等非实时接口；会话实时交互走 WS。

**配置选型（已定）：**

- **主配置格式：TOML**。不要改用 YAML/JSON/INI 作为主配置，除非先更新本文档。
- **配置库：`github.com/spf13/viper`**（读 TOML、默认值、环境变量覆盖、可选写回）。
- **运行时路径：`$ZACP_HOME/config.toml`，默认 `ZACP_HOME=~/.zacp`**（见 §3.1、§4.1）。
- 实现集中在 `backend/internal/config`：对外强类型 `Config`；业务层 **不要** 散落 `viper.Get*`。
- 密钥、Token **不进** TOML；用环境变量（或本机 `.env`，gitignore）注入；Agent 子进程继承环境。

**数据存储选型（已定）：**

- **SQLite3** 作为默认（也是当前唯一规划的）嵌入式数据库；不要默认上 Postgres/MySQL，除非规模需要并先更新本文档。
- **ORM：GORM**（`gorm.io/gorm`）。
- **驱动：纯 Go**——通过 **`github.com/glebarez/sqlite`** 使用 **`modernc.org/sqlite`**，**禁止**默认依赖需要 CGO 的 `mattn/go-sqlite3`（除非有充分理由并更新本文档）。
- **数据文件目录：`$ZACP_HOME/data/`**（默认 `~/.zacp/data/`），主库文件建议 `zacp.db`；必须启用 **WAL**，并支持 **版本化 schema 迁移**（见 §4.2）。
- 实现集中在 `backend/internal/store`（打开 DB、PRAGMA、迁移、仓储）；业务通过 store/repository 访问，不在 handler 里直接拼 SQL。

**本仓库角色定位：**

- 后端实现 **ACP Client**（`acp.Client` / `acp.NewClientSideConnection`），**不要**默认把 zacp 做成 ACP Agent 服务端，除非有明确需求。
- 典型流程：`Initialize` → `NewSession`（或 `LoadSession` / `ResumeSession`）→ `Prompt`，并通过 `SessionUpdate` 将流式更新推给 Web UI。
- **职责分离**：TOML 管「如何启动哪些 Agent 二进制」；SQLite 管「运行时数据」（会话、消息历史、审计等）。二者不要混成全塞进配置文件。

**前端选型（已定）：**

- **框架：Vue 3**（Composition API + `<script setup>` 优先）。
- **组件库：Naive UI**（`naive-ui`）。业务组件优先用 Naive，避免再引入第二套重量级 UI 库（如 Element Plus、Ant Design Vue），除非有充分理由并先更新本文档。
- **样式：Tailwind CSS**。布局、间距、响应式以 utility class 为主；与 Naive 主题冲突时，组件外观跟 Naive，页面骨架/间距用 Tailwind。
- **构建**：推荐 **Vite + TypeScript**（脚手架时锁定；未锁定前按此方向）。
- **包管理 / 安装 / 运行：一律使用 [Bun](https://bun.sh)**（`bun install`、`bun run dev`、`bun run build` 等）。**禁止**以 npm / pnpm / yarn 作为本项目前端的标准工具链（勿提交 `package-lock.json` / `pnpm-lock.yaml` / `yarn.lock`；锁文件用 **`bun.lock`** 或 Bun 当前默认 lockfile）。
- **实时通信**：浏览器原生 `WebSocket`，与后端 `coder/websocket` 对齐；不引入 socket.io 客户端。
- 所有前端代码仅在 `frontend/`，见 §7 前端约定。

---

## 3. 目录结构（必须遵守）

### 3.1 运行时用户目录（`$ZACP_HOME`，默认 `~/.zacp`）

**状态与配置不放在 git 仓库内，而放在用户主目录下的 `.zacp`：**

```text
$ZACP_HOME/                      # 默认: ~/.zacp
├── config.toml                  # 主配置（Viper 读取；勿提交到 git）
├── data/
│   ├── zacp.db                  # SQLite 主库
│   ├── zacp.db-wal              # WAL（启用 journal_mode=WAL 后出现）
│   └── zacp.db-shm
└── logs/                        # 可选：日志文件
```

| 变量 / 路径 | 说明 |
|-------------|------|
| `ZACP_HOME` | 根目录，**默认 `~/.zacp`**（即 `$HOME/.zacp`）；可用环境变量或启动 flag 覆盖 |
| `$ZACP_HOME/config.toml` | 运行时 TOML 配置 |
| `$ZACP_HOME/data/` | SQLite 等持久化数据；目录权限建议 `0700`，库文件 `0600` |
| `ZACP_CONFIG` | 可选：直接指定配置文件路径（覆盖默认的 `$ZACP_HOME/config.toml`） |

约定：

- **不要**用进程 cwd 下的 `.zacp` 作为默认（避免 `cd` 后「数据消失」的错觉）。
- **Agent 会话工作区 cwd**（代码仓库路径）与 **`$ZACP_HOME`（zacp 自身状态）分离**；前者配在 `[[agents]]` / `session.default_cwd`，后者只服务配置与数据库。
- 首次启动：若 `$ZACP_HOME` 不存在则创建；若无 `config.toml`，可从仓库内 `backend/configs/config.example.toml`（或 embed 样例）复制生成。
- 目录名统一为 **`.zacp`**，不要使用 `.zcap` 等其它拼写。

### 3.2 仓库源码目录

```
zacp/
├── AGENTS.md                 # 本文件：Agent 协作约定
├── README.md                 # 人类可读项目说明
├── Dockerfile                # 镜像构建（优先后端）
├── backend/                  # ★ 全部后端 Go 代码
│   ├── cmd/server/           # 进程入口，保持薄：组装依赖 + 启动
│   ├── configs/              # 仅样例：config.example.toml（入库）
│   ├── internal/             # 私有业务代码（禁止被外部模块 import）
│   │   ├── acp/
│   │   │   ├── client/       # ACP Client 连接封装（stdio/管道等）
│   │   │   ├── manager/      # 多 Agent / 多会话生命周期
│   │   │   └── providers/    # 各 Agent 启动参数与适配（pi、reasonix、grok…）
│   │   ├── api/
│   │   │   ├── handlers/     # HTTP Handler
│   │   │   ├── middleware/   # 中间件
│   │   │   └── router/       # 路由注册
│   │   ├── config/           # Viper 加载 $ZACP_HOME/config.toml → 强类型 Config
│   │   ├── store/            # GORM + SQLite：打开库、WAL、迁移、仓储
│   │   ├── model/            # DTO / 领域模型 / GORM model
│   │   ├── service/          # 业务编排（对接 manager / store 等）
│   │   └── ws/               # WebSocket：向浏览器推送 session/update
│   ├── pkg/                  # 可被多处复用的轻量工具库（谨慎使用）
│   ├── go.mod
│   └── go.sum
├── frontend/                 # ★ 全部前端（Vue 3 + Naive UI + Tailwind）
│   ├── src/
│   │   ├── api/              # REST 封装
│   │   ├── assets/
│   │   ├── components/       # 可复用 UI 组件
│   │   ├── composables/      # 可复用组合式函数（含 WS 等）
│   │   ├── pages/ 或 views/  # 页面
│   │   ├── stores/           # 状态（如 Pinia，脚手架时定）
│   │   ├── styles/           # 全局样式 / Tailwind 入口
│   │   ├── types/            # TS 类型（与后端 JSON 对齐）
│   │   ├── utils/            # 纯函数工具（优先复用）
│   │   ├── App.vue
│   │   └── main.ts
│   ├── public/
│   ├── package.json
│   └── vite.config.ts        # 或等价构建配置
├── scripts/                  # Shell 脚本（开发、构建、发布）
├── deployments/              # compose / k8s 等部署清单
└── docs/                     # 设计文档、协议笔记
```

### 放置规则

| 内容 | 位置 |
|------|------|
| Go 业务与依赖 | **仅** `backend/` |
| Web UI 源码 | **仅** `frontend/`（Vue 3 + Naive UI + Tailwind） |
| 可执行脚本 | `scripts/` 或根目录（根目录仅放极少数全局脚本） |
| Docker / 部署 | 根目录 `Dockerfile` 或 `deployments/` |
| 配置样例（入库） | `backend/configs/config.example.toml` |
| 运行时配置 / 数据库 | **`$ZACP_HOME`**（默认 `~/.zacp`），**不入库** |

**禁止：**

- 在仓库根目录散落 Go 源码或 `go.mod`
- 在 `backend/` 外写业务 Go 包
- 把 `~/.zacp` 下的真实配置、数据库、密钥、前端构建产物提交进 Git

---

## 4. 后端分层约定

按依赖方向从上到下：

```
cmd/server  →  api (router/handlers)  →  service  →  acp/manager|client|providers
                     ↓                      ↓
                    ws                   config / store / model
```

- **`cmd/server`**：只做启动与依赖注入，不写复杂业务。
- **`api/handlers`**：解析请求、校验入参、调用 `service`，返回 JSON；不直接 `exec` Agent 进程。
- **`service`**：用例编排（创建会话、发 Prompt、取消、权限回传、读写历史）。
- **`acp/client`**：封装 `acp-go-sdk` 的连接与回调（实现 `acp.Client`：权限、读/写文件、终端等）。
- **`acp/manager`**：管理多个 provider 连接、session 映射、并发与清理。
- **`acp/providers`**：各工具的启动命令、参数、环境变量差异；新增 Agent 优先在此扩展，而不是改核心 manager 逻辑。
- **`store`**：GORM/SQLite 初始化、迁移、会话与消息等持久化。
- **`ws`**：把 ACP `SessionUpdate`（消息块、工具调用、计划等）转成前端可消费的事件。

新增 Agent 接入清单：

1. 在用户 `~/.zacp/config.toml` 的 `[[agents]]` 增加一项，并同步仓库 `config.example.toml`  
2. 在 `internal/acp/providers` 增加启动/连接适配（若通用 stdio 已够用可只配 command/args）  
3. 在 manager 按配置注册 / 启用  
4. 必要时扩展 API / 前端展示  

---

## 4.1 配置文件约定（TOML + Viper + `~/.zacp`）

### 文件位置

| 文件 | 是否入库 | 说明 |
|------|----------|------|
| `backend/configs/config.example.toml` | 是 | 仓库内样例与字段说明（无密钥） |
| **`$ZACP_HOME/config.toml`**（默认 **`~/.zacp/config.toml`**） | **否** | **运行时主配置**；Viper 默认读取此路径 |
| `ZACP_HOME` | — | 覆盖状态根目录（默认 `~/.zacp`） |
| `ZACP_CONFIG` 或 `-config` | — | 可选：直接指定配置文件绝对/相对路径 |

### 优先级（由低到高，实现时按此约定）

1. 代码内默认值（Viper `SetDefault`）  
2. TOML 文件（`$ZACP_HOME/config.toml` 或 `ZACP_CONFIG`）  
3. 环境变量（Viper `AutomaticEnv` / 显式 `BindEnv`，建议前缀 `ZACP_`）  
4. 命令行 flag（若使用）  

### 结构约定（示意，字段可演进）

```toml
[server]
addr = ":8680"
mode = "debug"   # debug | release

[session]
default_cwd = "."          # Agent 工作区，不是 ZACP_HOME
auto_approve = false       # 生产默认 false；开发可 true
idle_timeout = "30m"       # 空闲回收超时；0 禁用（agent 闲置超时后自动停止释放内存）

[database]
# 相对路径相对于 $ZACP_HOME；默认 data/zacp.db
path = "data/zacp.db"

# 多 Agent：每个二进制 / ACP 入口一条
[[agents]]
id = "reasonix"
name = "Reasonix"
enabled = true
transport = "stdio"    # 当前以 stdio 为主；后续可扩展
command = "reasonix"   # 或绝对路径
args = ["--acp"]
cwd = ""               # 空则用 session.default_cwd
# 不要在此写 API Key；子进程继承系统环境变量即可
```

### Viper 使用约定

- 依赖：`github.com/spf13/viper`（`SetConfigType("toml")` 或文件扩展名 `.toml`）。
- **只在 `internal/config` 内使用 Viper**；`Load()` 返回 `Config`（含 `Server`、`Session`、`Database`、`Agents` 等），其余包只依赖该结构体。
- 写回配置：经 `internal/config` 封装 `Save`，写入 **`$ZACP_HOME/config.toml`**（或 `ZACP_CONFIG`），**不要**写仓库内 example；写前校验。
- 校验：`agents[].id` 唯一、`enabled` 项 `command` 非空等；失败则启动退出。
- **不要**在 handler 里把 Viper 当全局字典用。

---

## 4.2 数据库约定（SQLite3 + GORM + 纯 Go + WAL + 迁移）

### 选型与依赖

| 项 | 选择 |
|----|------|
| 引擎 | SQLite3 |
| ORM | `gorm.io/gorm` |
| 驱动 | `github.com/glebarez/sqlite`（纯 Go，基于 `modernc.org/sqlite`，**无 CGO**） |
| 代码位置 | `backend/internal/store`（及 `model` 中的表结构） |
| 默认路径 | **`$ZACP_HOME/data/zacp.db`**（即默认 `~/.zacp/data/zacp.db`） |

### 打开库与 PRAGMA（启动时必须）

- 确保 `$ZACP_HOME/data` 存在（权限建议 `0700`）。
- 启用 **WAL**：`PRAGMA journal_mode=WAL;`
- 建议同时设置：
  - `PRAGMA foreign_keys=ON;`
  - `PRAGMA busy_timeout=5000;`（或等价 DSN 参数，降低锁等待立刻失败）
- 单进程内使用连接池即可；**避免多个 zacp 进程同时写同一 db 文件**（除非明确处理多进程锁）。

### Schema 迁移

- **必须支持可版本化的表迁移**（新建表、加列、合并/重构表等），启动时按版本顺序执行。
- **不要**仅依赖 GORM `AutoMigrate` 作为生产唯一策略（开发可辅助，但要以可复现的迁移步骤为准）。
- 推荐做法（实现任选其一，保持简单即可）：
  - 自管 `schema_migrations`（或等价）版本表 + 递增 SQL/Go 迁移函数；或
  - `golang-migrate` / `goose` 等工具，迁移文件放在仓库内（如 `backend/internal/store/migrations/`）。
- 迁移失败：进程应 **拒绝启动** 并打出明确错误，避免半新半旧 schema。
- 「表合并」类变更：用显式 migration（复制数据 → 切表 → 删旧表），不要运行时猜测合并。

### 备份注意

- WAL 模式下会有 `zacp.db-wal` / `zacp.db-shm`；热备份应使用 SQLite backup API 或先 checkpoint，**不要**只拷贝单个 `.db` 文件当作完整热备。

### 存什么 / 不存什么

| 适合进 SQLite | 适合留在 TOML / 环境 |
|---------------|----------------------|
| 会话、消息、事件摘要、审计 | Agent 二进制 command/args 列表 |
| 用户偏好、UI 状态（若需要） | API Key、Token |
| 迁移版本元数据 | `$ZACP_HOME` 本身的路径策略 |

---


## 5. 常用命令

```bash
# 启动后端
cd backend && go run ./cmd/server
# 或
./scripts/dev-backend.sh

# 健康检查
curl http://127.0.0.1:8680/healthz

# 增加依赖（在 backend 目录）
cd backend
go get <module>@<version>
go mod tidy

# 编译检查
cd backend && go build -o /tmp/zacp ./cmd/server

# 测试（有测试后）
cd backend && go test ./...

# 前端（脚手架完成后；必须用 Bun，不要用 npm/pnpm/yarn）
cd frontend
bun install
bun run dev
bun run build
```


默认后端监听端口：**8680**（后续以配置文件为准）。

---

## 6. 依赖与版本

当前 / 规划直接依赖（见 `backend/go.mod`，未加入的以实现时 `go get` 为准）：

- `github.com/gin-gonic/gin` — HTTP
- `github.com/coder/acp-go-sdk` — ACP
- `github.com/coder/websocket` — WebSocket（规划）
- `github.com/spf13/viper` — 配置（规划）
- `gorm.io/gorm` + `github.com/glebarez/sqlite` — ORM + 纯 Go SQLite（规划）

要求：

- 新增依赖需有明确用途；优先标准库与已有依赖。
- 升级 `acp-go-sdk` 时注意协议版本与 API 变更，并跑通至少一条 agent 连接路径。
- 数据库驱动保持 **无 CGO** 的 pure Go 路径，便于交叉编译与容器构建。
- 不要随意改 `go` 版本字段，除非与运行环境对齐并验证构建。

---

## 7. 编码风格

### 7.0 通用原则：可维护性、复用、性能、中文注释

写代码时 **适当** 考虑性能与可维护性，避免过度设计，也避免「能跑就行」的堆砌。

**可维护与复用**

- **能复用的逻辑尽量抽成独立函数/方法/模块**，避免复制粘贴；出现第二次相似实现时优先抽取。
- **单一职责**：一个函数做好一件事；文件过长（经验上数百行且多主题混杂）时考虑拆分。
- **依赖方向清晰**：handler 不直接 `exec` Agent；组件不塞满业务协议细节；公共逻辑放 `utils` / `composables` / `internal/pkg` 或领域小包。
- **命名表意**：见名知意；避免 `tmp1`、`handle2` 等含糊名。
- **改动面最小化**：只改与任务相关的代码；不顺手大重构无关模块（除非任务就是重构）。

**性能（适度，不为微优化牺牲可读性）**

- **热路径**（WebSocket 推送、SessionUpdate 处理、消息列表渲染、Prompt 流式拼字）避免：无界切片无限涨、每帧全量深拷贝、在循环里重复编译正则/重复打开 DB、前端无关键列表的整表重渲染。
- **I/O 与进程**：Agent stdio、DB、网络调用注意超时、取消与连接复用；不要在请求路径上做不必要的全表扫描。
- **前端列表**：长会话/多消息考虑虚拟列表或分页（需要时再上）；流式更新尽量 **追加/局部 patch**，避免每次 token 都重建整棵树。
- **并发**：Go 里共享 session/连接加锁或用 channel 约定所有权；避免泄露 goroutine。
- 先保证正确与清晰，再对瓶颈做针对性优化；**禁止**无 profiling/依据的「炫技式」过早优化。

**注释（关键路径必须写好中文注释）**

- **关键地方必须写清楚的中文注释**，说明「为什么」和「不变量」，而不仅是翻译代码。包括但不限于：
  - ACP 连接生命周期、权限回调、取消与超时
  - WebSocket 协议帧与 session 绑定
  - 数据库迁移、WAL/并发假设
  - 安全相关：路径校验、鉴权、自动批准开关
  - 非直观的性能处理或并发模型
- 导出符号（Go 导出函数/类型、前端公共 composable/组件 props）应有简明注释（Go 用中文或中英均可，**项目内关键逻辑优先中文**）。
- 显而易见的一行逻辑不必写废话注释；**禁止**大段注释掉的死代码长期留存。
- 注释与代码不一致时以代码为准，并 **立刻改注释**。

### Go

- 遵循 `gofmt` / 常规 Go 习惯；导出符号写清注释（关键逻辑用 **中文**）。
- 错误要包装上下文：`fmt.Errorf("...: %w", err)`。
- 带超时的操作使用 `context.Context`；Agent 子进程、长连接必须可取消、可清理（防僵尸进程）。
- 并发访问 session / connection 时使用合适的同步手段，并在连接关闭时释放资源。
- 重复逻辑抽到小函数或 `internal` 子包；ACP 通用能力与具体 provider 差异分开。
- `internal/` 下包名简短清晰：`handlers`、`manager`、`providers`、`store` 等。
- 日志：初期可用标准库 `log/slog`；避免在热路径刷屏敏感信息（Token、密钥、完整用户文件内容）。

### API / 前后端协作

- REST 路径建议前缀 `/api/v1/...`。
- 错误响应结构尽量统一，例如：`{ "error": { "code": "...", "message": "..." } }`。
- 前后端 JSON 字段命名统一 **camelCase**（与前端习惯对齐）。
- 类型/接口变更时同步前端 `types` 与后端 model，避免静默字段漂移。

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
- 协议解析、重连、心跳等 **前后端各自抽复用模块**（前端放 `composables`/`utils`，后端放 `internal/ws`），并在关键分支写中文注释。

### 前端（Vue 3 + Naive UI + Tailwind CSS）

- 所有 UI 代码只在 **`frontend/`**；不要在 `backend` 内嵌大型前端工程源码。
- **栈（已定）**：
  - **Vue 3** + Composition API + `<script setup>`（优先）
  - **Naive UI** 作为组件库
  - **Tailwind CSS** 作为原子化样式方案
  - 推荐 Vite + TypeScript
  - **包管理与脚本：仅 Bun**（见下）
- **Bun（强制）**：
  - 安装依赖：`bun install`
  - 开发 / 构建 / 预览：`bun run dev`、`bun run build`、`bun run preview`（以 `package.json` scripts 为准）
  - 执行一次性命令：`bunx <cmd>`（对应他处文档里的 `npx`/`pnpm dlx`）
  - 锁文件：提交 Bun 的 lockfile（如 `bun.lock`）；**不要**再维护 npm/pnpm/yarn 的 lockfile
  - CI 与文档示例中的前端命令也统一写 Bun，避免混用导致依赖树不一致
- **目录习惯**：可复用组件 → `components/`；可复用逻辑 → `composables/` 与 `utils/`；页面 → `pages/` 或 `views/`；与后端契约 → `types/` + `api/`。
- **WebSocket**：使用浏览器原生 `WebSocket`；连接、心跳、重连、消息分发抽成 composable（如 `useAcpSocket`），供页面复用。
- **样式**：Tailwind 管布局与间距；Naive 管复杂交互组件；避免再引入第二套完整 UI 框架。
- **性能**：流式对话场景注意消息列表更新方式；大列表按需优化；不在模板里做重计算而不用 `computed`。
- **注释**：权限流、WS 状态机、会话切换等关键逻辑写 **中文注释**。
- 静态资源托管与后端联调（dev proxy / 同域）在集成阶段约定；生产构建产物勿提交 git（`dist` 已在 ignore 思路中）。

---


## 8. ACP 实现注意点

- 实现 **Client** 接口时，权限请求（`RequestPermission`）应能传到 Web UI，由用户选择后再返回 outcome，**不要**在服务端无条件一律自动 allow（可配置「开发模式自动允许」）。
- 文件读写路径应限制在会话工作区（cwd）内，防止路径穿越。
- stdio 连接：正确连接子进程 stdin/stdout，处理 stderr 日志，进程退出时关闭会话并通知前端。
- 扩展方法（以 `_` 开头）按 SDK 文档处理；未知扩展通知可忽略，勿与标准方法冲突。
- 与具体 Agent（Pi / Reasonix / Grok）相关的 CLI 参数差异放在 `providers`，保持 core 协议层通用。

---

## 9. 配置、数据与安全

- 用户状态根目录：**`$ZACP_HOME`，默认 `~/.zacp`**。
- 样例配置（入库）：`backend/configs/config.example.toml`。
- 运行时配置：**`~/.zacp/config.toml`**（或 `ZACP_CONFIG`），**勿提交**。
- 运行时数据库：**`~/.zacp/data/zacp.db`**（WAL 附属文件同目录），**勿提交**。
- 配置加载：`internal/config` + **Viper**；持久化：`internal/store` + **GORM** + **glebarez/sqlite**。
- 密钥仅环境变量 / 本机 `.env`（gitignore）。
- 不在日志、错误信息、前端接口中泄露 API Key、Cookie、本机绝对路径中的敏感部分。
- Docker 中可通过 `ZACP_HOME` 挂载卷持久化；生产使用 `GIN_MODE=release` 或 `server.mode = "release"`。

---

## 10. 文档与提交

- 架构、重大设计写入 `docs/`。
- 用户可见的使用说明更新 `README.md`；**Agent 协作约定**更新本文件 `AGENTS.md`。
- 提交信息简洁说明「改了什么、为什么」；一次提交聚焦单一意图。
- 完成功能前：确保 `go build ./...`（或至少 `./cmd/server`）通过；有测试则跑 `go test ./...`。

---

## 11. 当前状态

已完成：

- monorepo 目录骨架
- Gin + acp-go-sdk 依赖安装
- reasonix `--acp` 最小 demo（终端 REPL + `POST /api/v1/chat`）
- 选型文档：WebSocket（`coder/websocket`）、配置（TOML + Viper + **`~/.zacp`**）、数据库（SQLite + GORM + 纯 Go 驱动 + WAL + 迁移）
- 前端选型：**Vue 3 + Naive UI + Tailwind CSS**；包管理/运行 **一律 Bun**
- 编码约束：可维护性 / 复用拆分 / 适度性能
- **Agent 生命周期**：启动时仅预加载配置中第一个（最顶部）enabled 的 agent；其余按需启动（前端切 agent 建会话时经 `service` 自动拉起）；`session.idle_timeout` 空闲回收（默认 30m，0 禁用），回收后经 unknown-session 自动恢复

---

## 12. 给 Agent 的简短检查清单

在动手改代码前确认：

1. 改动是否落在正确目录（backend / frontend / scripts / deployments）？
2. 运行时状态是否写在 **`$ZACP_HOME`（默认 `~/.zacp`）**，而不是仓库目录或随意 cwd？
3. 是否把 Agent 差异关在 `providers`，而不是污染通用 manager？
4. 子进程 / 连接 / DB 是否有生命周期与错误处理（含 WAL、迁移失败即退出）？
5. 是否引入了不必要的依赖或破坏了 `go.mod`（SQLite 是否仍为无 CGO 路径）？前端是否坚持 Vue3 + Naive + Tailwind，未乱加第二套 UI 库？前端命令是否用的 **Bun**（而非 npm/pnpm/yarn）？
6. 可复用逻辑是否已抽取？热路径是否有明显浪费？**关键逻辑是否有清晰中文注释**？
7. 是否更新了必要的 README / `config.example.toml` / 本 AGENTS.md？

---

*文档语言：中文。若与代码不一致，以代码与 `go.mod` 为准，并应及时回写本文档。*
