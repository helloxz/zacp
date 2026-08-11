# zacp Web TTY 实现计划

> 规格：`docs/superpowers/specs/2026-08-11-web-tty-design.md`  
> 日期：2026-08-11  
> 状态：计划已批准规格约束，待执行  
> 原则：后端协议先行；前后端可在契约固定后并行；每个任务完成后立即执行该任务的局部验证；不把 TTY 接入现有 ACP 消息队列。

---

## 总览与依赖

```text
B0-1 依赖与共享 WS 认证
  → B0-2 Workspace/Platform service
  → B0-3 TTY Manager/Session/PTY 生命周期
  → B0-4 TTY WebSocket 协议与流控
  → B0-5 Router/Main wiring 与后端测试

B0-4 定义并冻结协议
  → F0-1 xterm 依赖、类型、socket composable
  → F0-2 Terminal 组件
  → F1-1 Page/Route + Workspace API
  → F1-2 Tab Manager/Tabs
  → F2-1 主题、错误态与交互打磨

B0-5 + F2-1
  → V0-1 后端构建与测试
  → V0-2 前端构建
  → V0-3 Linux 浏览器闭环
  → V0-4 macOS/Windows 原生烟测与交叉编译
```

| 轨道 | 内容 | 依赖 |
|------|------|------|
| **B0** | go-pty、PTY 生命周期、TTY WebSocket、路由和清理 | 无 |
| **F0** | xterm.js、页面、Tab、socket composable、主题 | B0-4 的协议字段 |
| **V0** | 单元、集成、浏览器和跨平台验证 | B0 + F0 |

**推荐单人执行顺序：** B0-1 → B0-2 → B0-3 → B0-4 → F0-1 → F0-2 → B0-5 → F1-1 → F1-2 → F2-1 → V0。

**可并行点：** B0-5 之前，F0-1/F0-2 可以在 B0-4 协议冻结后开始；但页面只有在 B0-5 路由和 F1-1/F1-2 完成后才能做完整闭环。


**首期不做：** 数据库模型/迁移、终端恢复、断线重连、SSH、容器、远程主机、录制、输出持久化。

---

## 阶段 B0 — 后端

### Task B0-1：加入 go-pty 并抽取共享 WebSocket 认证

**目标：** 把 TTY 依赖和现有 WebSocket 认证握手变成可复用基础，不改变 ACP 业务协议。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/go.mod` | 添加 `github.com/aymanbagabas/go-pty`，固定已验证版本；保持纯 Go/`CGO_ENABLED=0` 构建策略 |
| `backend/go.sum` | 由 Go module 命令更新校验和 |
| `backend/internal/ws/handler.go` | 将 `wsAuthProtocolPrefix`、`firstSubprotocol`、认证校验逻辑抽为包级共享函数；现有 `Handler` 改用该函数 |
| `backend/internal/ws/auth.go` | 新增共享认证子协议辅助函数；保持 token 不进入 URL 的现有安全语义 |

**实现步骤**

1. 添加 go-pty 依赖并运行依赖整理。
2. 把现有 `Handler.authSubprotocol` 的逻辑拆成可供 TTY handler 调用的包级函数，例如：
   - `AuthSubprotocol(r *http.Request, authSvc *auth.Service) (string, bool)`；
   - 保留首个子协议解析和未启用认证时的回显行为。
3. 现有 ACP `ServeHTTP`、`ServeHTTPWithSession` 改用共享函数，行为必须不变。
4. 不在此任务修改 `AcceptOptions.InsecureSkipVerify` 的现有聊天行为；TTY handler 后续不得复制不必要的 ACP Hub 逻辑。
5. 只创建 TTY 包边界，不添加空实现路由或 no-op handler。

**实现要点**

- 不能让 `internal/ws` import `internal/tty`，避免包循环；路由层负责同时组装两种 handler。
- 认证失败仍返回 HTTP 401，认证未启用时不要求本地 token。
- 共享函数只负责子协议解析/认证，不负责 Accept、连接生命周期或业务消息。
- go-pty 版本必须与当前 Go 1.25 工具链兼容。

**验证**

```bash
cd backend && go test ./internal/ws ./internal/auth
cd backend && go build -o /tmp/zacp ./cmd/server
```

检查现有 ACP WebSocket 的认证启用、关闭、有效 token、无效 token 行为未改变。

---

### Task B0-2：实现 Workspace 解析和跨平台 shell 选择服务

**目标：** 让 TTY 只根据 workspaceId 获得有效目录，并集中处理 Unix/Windows shell 选择。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/service/tty.go` | 新增 `TTYService`，解析 workspaceId、校验路径、选择平台 shell |
| `backend/internal/service/tty_test.go` | 覆盖 workspace 和尺寸/输入边界相关纯逻辑；不依赖真实 WebSocket |
| `backend/internal/model/models.go` | 不修改；复用已有 `Workspace` |
| `backend/internal/store/repository.go` | 不修改；复用现有 `WorkspaceRepository.GetByID` |

**接口方向**

```go
type TTYService struct { ... }

func NewTTYService(workspaceRepo *store.WorkspaceRepository) *TTYService
func (s *TTYService) ResolveWorkspace(workspaceID uint) (*model.Workspace, error)
func (s *TTYService) Shell() (path string, args []string, err error)
```

`Shell()` 只负责读取环境变量、解析可执行 shell 并返回路径/参数；启动失败后的 fallback 由 B0-3 Session 启动流程负责。

**实现步骤**

1. `ResolveWorkspace` 调用现有 workspace repository 查询记录；GORM 默认过滤已删除记录。
2. 拒绝 `workspaceID == 0`。
3. 对 `Workspace.Path` 执行 `os.Stat`，确认存在且是目录；失败返回可被 handler 映射到 404/409 的业务错误。
4. Unix 使用 `$SHELL`，通过 `exec.LookPath` 或等价检查确认可执行；为空或解析失败时返回 `/bin/sh`。
5. Windows 使用 `%COMSPEC%`；为空或解析失败时返回 `cmd.exe`。
6. shell 选择函数不能读取前端参数，也不能接受任意命令覆盖。
7. 尺寸校验集中在 `internal/tty/protocol.go` 或 service 的纯函数中：`1..1000` 列、`1..500` 行。
8. 不使用文件服务的相对路径校验，因为 TTY 的输入是已登记 workspace 根目录。

**实现要点**

- 返回错误必须包装上下文，例如 `resolve tty workspace %d: %w`。
- 不把完整系统路径写入普通错误日志；handler 可以将通用错误码返回前端。
- Unix 的 fallback 检查不能因为测试环境没有用户 shell 就阻断包级单元测试；shell 路径选择应可测试。
- Windows 专用代码使用 build tags；不能在 Linux 编译路径引用 Windows-only 类型。

**验证**

```bash
cd backend && go test ./internal/service
```

覆盖：workspace 不存在、路径被删除、路径变成文件、Unix shell fallback、尺寸上下界。

---

### Task B0-3：实现 TTY Manager、Session 和进程清理

**目标：** 建立一个 Session 对应一个 PTY/shell/WebSocket 的内存生命周期模型。

**新建文件**

```text
backend/internal/tty/manager.go
backend/internal/tty/session.go
backend/internal/tty/process_unix.go
backend/internal/tty/process_windows.go
backend/internal/tty/session_test.go
backend/internal/tty/manager_test.go
```

**Manager 接口方向**

```go
type Manager struct { ... }

func NewManager(log *slog.Logger, maxSessions int) *Manager
func (m *Manager) Create(ctx context.Context, workspace *model.Workspace, shellPath string, shellArgs []string, conn *websocket.Conn) (*Session, error)
func (m *Manager) Count() int
func (m *Manager) CloseAll()
```

`maxSessions` 首期传入 32。Manager 负责并发保护、terminalId 生成、注册/移除和关闭全部 Session；不解析 Gin 参数，不查询数据库。

**Session 实现步骤**

1. 定义 Session 状态：`starting`、`running`、`exited`、`closing`、`closed`、`error`。
2. 创建 `pty.New()`，以 `80x25` 初始尺寸创建命令。
3. 设置 `Cmd.Dir = workspace.Path`；环境继承 `os.Environ()`，必要时补充终端变量但不能覆盖既有环境。
4. 启动命令；若平台默认 shell 启动失败，仅重试一次固定 fallback（Unix `/bin/sh`、Windows `cmd.exe`），不接受客户端命令覆盖。
5. 启动 PTY reader：读取原始字节，进入有界 outbound 队列。
6. 启动 process waiter：等待 `Cmd.Wait()`，记录退出码并发送 `exit`。
7. WebSocket reader 收到 Binary 后写入 PTY；收到 resize 后调用 PTY Resize。
8. `Close(reason)` 使用 `sync.Once` 或等价机制保证幂等：取消 context、停止进程、关闭 PTY、等待内部 goroutine、从 Manager 移除。
9. Session 关闭时禁止继续接受输入，避免关闭期间向已失效 pipe 写入。

**平台清理**

- Unix：利用 go-pty 创建的会话组语义，补充平台代码杀死 shell 进程组，避免只杀父 shell 后留下子进程。
- Windows：使用 go-pty ConPTY、`Cmd.Cancel` 和 process wait；验证关闭 ConPTY 后 cmd.exe 及其子进程退出。
- 两个平台的清理 helper 使用同一抽象接口，调用方不分支平台细节。

**并发不变量**

- 一个 Session 只有一个 outbound writer。
- Manager map 的读写加锁。
- `CloseAll` 先快照再逐个关闭，不在 Manager 锁内等待 Session，避免死锁。
- PTY reader、WebSocket reader、process waiter 的退出都能触发或观察同一个关闭上下文。
- 不能在 `Close` 中无限等待一个已经阻塞的 WebSocket writer；writer 使用有界 context 超时。

**验证**

```bash
cd backend && go test ./internal/tty -run 'Test(Manager|Session)' -count=1
```

至少覆盖：Manager 32 上限、重复 Close、Create 失败回滚、shell 退出后 Session 移除、关闭后不再写入。

---

### Task B0-4：实现 TTY 协议、WebSocket Handler 与流控

**目标：** 用独立 Handler 把 Browser WebSocket 和单个 PTY 双向连接起来，使用 Binary 数据，禁止静默丢输出。

**新建/修改文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/tty/flow.go` | 实现有界 4 MiB outbound 队列和 10 秒 writer 超时 |
| `backend/internal/tty/handler.go` | 认证、workspace 解析、Accept、Session 创建、读写循环 |
| `backend/internal/tty/protocol_test.go` | 控制帧解析、未知类型、尺寸、输入大小测试 |
| `backend/internal/tty/handler_test.go` | HTTP/WS 握手和错误映射测试 |

**协议实现**

客户端 → 服务端：

- Binary：原始 PTY 输入；单帧最大 1 MiB。
- Text JSON：仅支持 `resize`，字段 `cols`、`rows` 必须是整数并通过尺寸范围校验。

服务端 → 客户端：

- Binary：PTY 原始输出。
- Text JSON：`ready`、`exit`、`error`。

不要定义应用层 ping/pong；服务端使用 `coder/websocket.Conn.Ping` 做保活。

**Handler 顺序**

1. 解析 query `workspaceId`，缺失/非数字返回 400。
2. 调用共享认证子协议函数；失败返回 401。
3. 调用 `TTYService.ResolveWorkspace`；不存在返回 404，路径失效返回 409。
4. 检查 Manager 32 个进程级上限；超限返回 429。
5. 用 `websocket.Accept` 升级连接，回显认证子协议。
6. 创建 Session；PTY 或 shell 启动失败发送 `error`，关闭连接并回滚 Manager 注册。
7. 启动单一 outbound writer、PTY reader、WebSocket reader 和 process waiter。
8. 任一读写/进程终止事件进入 Session.Close；不得只关闭某一个 goroutine。

**流控实现**

- outbound queue 总容量 4 MiB；队列项携带 Binary 或 Text 帧类型和数据。
- PTY reader 达到高水位时阻塞，不丢弃旧输出，不无限扩容。
- writer 每次使用独立的 10 秒 context 写入；持续阻塞则尝试发送 error 并关闭 Session。
- reader 对 Browser Binary 输入设置 1 MiB 读限制/长度校验。
- 输出控制帧和 Binary 帧由同一个 writer 串行发送。

**共享认证改动约束**

TTY Handler 调用 B0-1 抽取的认证函数；不得读取 URL token，不得复制 `authSubprotocol` 的旧实现。

**验证**

```bash
cd backend && go test ./internal/tty -run 'Test(Protocol|Handler|Flow)' -count=1
```

使用 httptest/WebSocket 测试：未认证、非法 workspace、上限、ready、resize、Binary echo、unknown message、exit 和连接关闭。

---

### Task B0-5：接入 Router、main 和优雅关闭

**目标：** 使 TTY 端点可被真实服务器访问，并在进程退出时清理 PTY。

**改动文件**

| 文件 | 改动 |
|------|------|
| `backend/internal/api/router/router.go` | `New` 增加 TTY Handler 参数；在 `/api/v1` 下注册 `/tty/ws`；与 ACP `/ws` 一样不走 Bearer middleware，由 handler 完成子协议认证 |
| `backend/cmd/server/main.go` | 创建 `TTYService`、`tty.Manager`、TTY Handler；注入 Router；shutdown 顺序加入 `ttyManager.CloseAll()` |
| `backend/internal/ws/handler.go` | 仅保留 ACP Handler 对共享 auth helper 的调用 |
| `backend/internal/api/router/router_test.go` 或现有测试位置 | 验证路由注册和错误响应（若当前 router 无测试则新增最小测试） |

**实现步骤**

1. `main` 创建 workspace repository 后创建 `TTYService`。
2. 用 logger 和 `maxSessions=32` 创建 TTY Manager。
3. 创建 TTY Handler，注入 Manager、TTYService、logger、auth service。
4. 更新 `router.New` 调用方和参数顺序，避免遗漏编译调用点。
5. 注册 `GET /api/v1/tty/ws`。
6. 进程收到 SIGINT/SIGTERM 时先关闭 TTY Manager，再关闭 ACP WebSocket/Agent，确保 shell 不残留。
7. 不新增数据库迁移，不修改 ACP `protocol.go`。

**验证**

```bash
cd backend && go test ./...
cd backend && go build -o /tmp/zacp ./cmd/server
```

启动后使用已有工作区对 `/api/v1/tty/ws?workspaceId=...` 做一次实际握手检查。

---

## 阶段 F0 — 前端基础与传输

### Task F0-1：引入 xterm.js、TTY 类型和逐 Tab socket composable

**目标：** 建立不依赖 ACP 单例的 TTY 客户端传输层。

**改动文件**

| 文件 | 改动 |
|------|------|
| `frontend/package.json` | 添加 `@xterm/xterm`、`@xterm/addon-fit` |
| `frontend/bun.lock` | 由 Bun 更新 |
| `frontend/src/types/tty.ts` | 定义 Tab 状态、控制帧、服务端帧和 handler 类型 |
| `frontend/src/composables/useTtySocket.ts` | 每个 Tab 一个 socket 实例；复用 `wsUrl`、auth token 和认证状态读取 |
| `frontend/src/styles/main.css` | 引入 xterm CSS，确保基础样式进入构建产物 |

**类型方向**

```ts
type TtyTabStatus =
  | 'creating'
  | 'connecting'
  | 'connected'
  | 'exited'
  | 'closing'
  | 'closed'
  | 'error'

type TtyClientControl = {
  type: 'resize'
  cols: number
  rows: number
}

type TtyServerControl =
  | { type: 'ready'; terminalId: string }
  | { type: 'exit'; code: number }
  | { type: 'error'; code: string; message: string }
```

**socket 实现步骤**

1. 用 `wsUrl('/api/v1/tty/ws?workspaceId=...')` 生成 URL；workspaceId 使用 `encodeURIComponent`。
2. 调用 `useAuthStore().ensureStatus()`；认证启用且无 token 时不创建连接。
3. 有 token 时传 `zacp-auth.<token>` 子协议；认证关闭时不传子协议。
4. 设置 `socket.binaryType = 'arraybuffer'`。
5. `onmessage` 按 `typeof event.data` 区分 Text JSON 和 ArrayBuffer；Blob 通过 `arrayBuffer()` 转换后交给 xterm。
6. `sendInput(Uint8Array)` 只在 OPEN 状态发送 Binary。
7. `sendResize(cols, rows)` 发送 JSON Text。
8. 不实现 ACP composable 的全局重连；TTY 是临时 Session，连接关闭后标记当前 Tab error/closed，不恢复旧进程。
9. `close()` 清理 handler、timer 和 WebSocket，重复调用安全。

**验证**

```bash
cd frontend && bun run build
```

先用最小临时组件或浏览器控制台确认 Text/Binary 分支和 auth 子协议生成正确。

---

### Task F0-2：实现 xterm Terminal 组件

**目标：** 把一个 TTY Tab 映射到一个完整 xterm 实例，并正确处理 resize、输入和销毁。

**新建文件**

```text
frontend/src/components/tty/TtyTerminal.vue
```

**实现步骤**

1. 接收 props：`workspaceId`、`tabId`、`active`；组件内部为该 Tab 创建唯一 `useTtySocket` 实例，管理层只接收状态事件。
2. `onMounted` 创建 `Terminal`，配置 cursor、font、scrollback、theme 等基础选项。
3. 创建并加载 `FitAddon`，打开 terminal 容器。
4. 注册 `onData` 和 `onBinary`，转换为 Uint8Array/Binary 帧。
5. 注册 xterm `onResize`，向后端发送受限 cols/rows。
6. 注册 `ResizeObserver`；容器可见且尺寸大于 0 时执行 `fit()`。
7. 收到后端 Binary 输出时调用 `terminal.write()`，不要自行解析 ANSI。
8. 收到 `ready`/`exit`/`error` 时通过 emits 更新 `useTtyManager` 中的 Tab 元数据。
9. `active` 从 false 切 true 时 `nextTick` 后 fit 并 focus。
10. `onBeforeUnmount` 释放 xterm listener、ResizeObserver、FitAddon、Terminal 和该 Tab 的 socket。

**实现要点**

- xterm 容器必须有明确的 `min-height: 0` 和可计算高度。
- 隐藏 Tab 不销毁 Terminal，避免滚动缓冲和光标状态丢失。
- 非活动 Tab 不发送零尺寸。
- 终端数据不经过 `innerHTML`。
- `terminal.options.theme` 由页面主题变化更新。

**验证**

浏览器中执行：

```text
echo hello
printf '\033[31mred\033[0m\n'
中文输入
```

确认输出、颜色、粘贴和 Ctrl+C 能到达后端。

---

## 阶段 F1 — 页面、Tab 和路由

### Task F1-1：增加 Workspace 单项 API 和 TTY 路由

**目标：** 页面刷新时根据 workspaceId 独立加载工作区，不依赖聊天 store 首屏状态。

**改动文件**

| 文件 | 改动 |
|------|------|
| `frontend/src/api/index.ts` | 添加 `fetchWorkspace(workspaceId)`，调用已有 `GET /api/v1/workspaces/:id` |
| `frontend/src/router/index.ts` | 添加 `/tty` → `TtyPage.vue`；更新路由注释；不让 home 守卫处理 tty |
| `frontend/src/pages/TtyPage.vue` | 新增页面骨架、query 校验、workspace 加载和页面 manager |

**实现步骤**

1. `fetchWorkspace` 解包 `{ workspace: Workspace }`，ID 使用数字并进行 URL 编码。
2. 增加 `/tty` 路由，保持现有认证 `beforeEach` 生效。
3. `TtyPage` 将 route.query.workspaceId 转成单值字符串并用严格正整数校验。
4. 缺少/非法 ID 显示错误空态，不建立 WebSocket。
5. API 404/409/网络错误使用现有 ApiError 语义展示。
6. Workspace 成功后渲染 Tabs 和 Terminal 容器，自动创建首个 Tab。

**验证**

```bash
cd frontend && bun run build
```

浏览器验证有效、缺失、非数字和不存在 workspaceId 的页面状态。

---

### Task F1-2：实现 TTY Manager composable 和 Tab 栏

**目标：** 支持最多 6 个独立 Tab 的创建、切换、关闭和状态显示。

**新建文件**

```text
frontend/src/composables/useTtyManager.ts
frontend/src/components/tty/TtyTabs.vue
```

**Manager 状态**

```ts
interface TtyTabState {
  id: string
  title: string
  status: TtyTabStatus
  terminal: Terminal | null
  fitAddon: FitAddon | null
  socket: WebSocket | null
  exitCode?: number
  error?: string
}
```

**实现步骤**

1. 使用页面级 `Map<string, TtyTabState>` 和 `activeTabId`。
2. `createTab()` 检查 6 个活动 Tab 上限，创建稳定标题 `终端 N`，连接 socket。
3. 新 Tab 建立后自动设为 active 并 focus。
4. `selectTab(id)` 只切换可见 pane，不销毁 Terminal。
5. `closeTab(id)` 对 connected/connecting 状态显示 Naive UI dialog；closing 状态禁止重复操作。
6. 关闭时先关闭 socket，再销毁 terminal/addon/listeners，从 Map 删除。
7. 关闭最后一个 Tab 后显示空态，不自动创建；页面首次加载时才自动创建首个 Tab。
8. `onBeforeRouteLeave`/`onBeforeUnmount` 关闭所有 Tab。
9. Tab 状态点区分 connecting、connected、exited、error。
10. 页面不渲染额外顶部 Header；Tab 栏直接作为页面首个交互区域。

**验证**

浏览器验证：

- `+` 连续创建 6 个 Tab；第 7 个被拒绝并提示；
- 每个 Tab 执行不同 `echo`，输出不串台；
- 切换后滚动内容和光标保留；
- 关闭最后一个后显示空态；
- 离开页面后后端进程退出。

---

## 阶段 F2 — 视觉、交互和可访问性

### Task F2-1：完成主题、布局和错误态

**目标：** TTY 页面达到现有 Naive UI/Tailwind 应用的视觉和交互质量。

**改动文件**

| 文件 | 改动 |
|------|------|
| `frontend/src/pages/TtyPage.vue` | 全屏布局、加载/错误/空态、主题传递 |
| `frontend/src/components/tty/TtyTabs.vue` | Tab 状态点、关闭按钮、上限提示 |
| `frontend/src/components/tty/TtyTerminal.vue` | xterm 主题和尺寸细节 |
| `frontend/src/locales/zh-CN.ts` / `en-US.ts` | 添加全部 TTY 加载、错误、空态、Tab 和上限文案；不在组件内散落中英文 |

**实现步骤**

1. 使用现有 `bg-surface`、`bg-surface-raised`、`border-divider`、`text-ink` 等 token。
2. 深色模式设置深色 xterm theme，浅色模式设置浅色 xterm theme。
3. Tab bar、Terminal pane 使用 `h-screen` 内部 flex，禁止 body 双滚动。
4. 连接中只在对应 Tab 显示 loading；不遮挡其它终端。
5. workspace 失效、连接失败、shell 退出分别显示不同文案。
6. 关闭按钮添加 `aria-label`，Tab 可通过键盘切换。
7. 不把 shell title、链接或输出内容插入 DOM HTML。

**验证**

```bash
cd frontend && bun run build
```

使用浏览器分别验证深色/浅色、窄窗口、Tab 较多、错误和空态。

---

## 阶段 V0 — 集成验证

### Task V0-1：后端测试与构建

**命令**

```bash
cd backend && go test ./...
cd backend && go build -o /tmp/zacp ./cmd/server
```

**行为验证**

1. 创建真实 workspace。
2. 启动后端和前端。
3. 进入 `/tty?workspaceId={id}`。
4. 确认 `pwd` 输出 workspace 路径。
5. 确认 ANSI、中文、粘贴、Ctrl+C、resize。
6. 执行 `yes` 后 Ctrl+C，确认无明显卡死或无限内存增长。
7. 关闭 Tab，确认 shell 进程退出。
8. 离开页面，确认所有临时 Session 退出。

### Task V0-2：前端构建和浏览器烟测

**命令**

```bash
cd frontend && bun install
cd frontend && bun run build
```

使用 Browser 工具验证：

- 路由直达和刷新；
- 首个终端；
- 新增/切换/关闭 Tab；
- 深色/浅色；
- 错误 workspace；
- 页面离开清理。

### Task V0-3：跨平台构建检查

```bash
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/server
cd backend && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/server
cd backend && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./cmd/server
cd backend && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/server
cd backend && CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./cmd/server
```

交叉编译只检查构建；必须在真实 Linux、macOS、Windows 上补充 PTY 运行烟测：

- Linux `$SHELL` 与 `/bin/sh` fallback；
- macOS zsh；
- Windows cmd.exe/ConPTY；
- Windows 中文、Ctrl+C、resize、关闭进程。

### Task V0-4：发布与文档对齐

**改动/检查**

| 项 | 动作 |
|----|------|
| `README.md` | 补充 Web TTY 使用方式、安全提醒和 `/tty?workspaceId=` 路由 |
| `CHANGE.md` | 记录新增 Web TTY、xterm.js、跨平台 PTY |
| `docs/API.md` | 新增 TTY endpoint 和 Binary/Text 帧说明 |
| `AGENTS.md` | 仅当实现改变现有协作约定时更新；不为单个功能重复记录实现细节 |
| `scripts/build.sh` | 确认 Bun 构建和 Go 交叉编译自然包含新前端资源；无额外拷贝步骤 |

**验证**

```bash
./scripts/build.sh
```

确认前端构建产物被嵌入，单二进制启动后可访问 `/tty` 和 `/api/v1/tty/ws`。

---

## 关键路径中文注释要求

实现过程中必须补充中文注释，说明不变量和原因，而不是翻译代码：

- TTY Session 的 PTY、shell、WebSocket 所有权和关闭顺序；
- Unix 进程组与 Windows ConPTY 的清理差异；
- Binary PTY 数据为何不能经过 JSON/UTF-8 转换；
- outbound queue 背压为何不能复用 ACP 丢弃队列；
- WebSocket 认证子协议为何不能把 token 放 query；
- 前端 Tab 隐藏但不销毁 xterm 的原因；
- 页面离开时临时 Session 必须关闭的原因。

---

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 现有 ACP WebSocket 与 TTY 协议耦合 | 独立 `/api/v1/tty/ws`、独立 Handler/Manager；只共享底层 websocket 和认证辅助 |
| 终端高速输出压垮浏览器 | 4 MiB 有界输出队列、PTY 背压、10 秒 writer 超时、`yes`/Ctrl+C 烟测 |
| shell/子进程残留 | Session 幂等关闭、Unix process group helper、Windows ConPTY 原生烟测 |
| WebSocket 断线后孤儿进程 | 临时模式下连接断开即 Close；后端以 conn 关闭为最终清理触发 |
| Binary 数据被编码破坏 | Binary frame + ArrayBuffer；禁止 JSON/base64/TextDecoder 路径 |
| workspace 路径越权 | 客户端只传 ID；服务层查询记录并重新 Stat 目录 |
| 终端暴露远程 shell | 复用认证、限制 Origin、默认资源上限、不记录输入输出；远程部署必须启用认证 |
| xterm 隐藏容器尺寸为 0 | 所有实例保留挂载；激活后 nextTick + ResizeObserver fit |
| 路由/页面离开遗漏清理 | manager 统一 closeAll，路由守卫和卸载双重触发，Session.Close 幂等 |
| 跨平台只交叉编译未运行 | Linux/macOS/Windows 原生 smoke test，发布前逐项记录结果 |

---

## 建议提交切片

1. **PR1：** go-pty 依赖、共享 WS 认证、TTY service、shell/尺寸纯逻辑。
2. **PR2：** TTY Manager/Session、Unix/Windows 清理、协议和流控。
3. **PR3：** Router/main/shutdown wiring、后端测试、Linux 闭环。
4. **PR4：** xterm 依赖、类型、socket composable、Terminal 组件。
5. **PR5：** TTY 页面、Tab、路由、workspace API、错误态。
6. **PR6：** 主题/交互打磨、浏览器验证、跨平台构建与文档。

单人执行时仍按 B0-1 → B0-5 → F0-1 → F0-2 → F1-1 → F1-2 → F2-1 → V0 顺序；每个 PR 切片完成后先跑该切片验证，再进入下一切片。

---

## 每步通用检查清单

- [ ] 改动前重新读取目标文件和相关调用点；导出符号变更前检查引用。
- [ ] 后端关键路径包含中文不变量注释。
- [ ] `cd backend && go test ./...`（完成后端切片时）。
- [ ] `cd backend && go build -o /tmp/zacp ./cmd/server`。
- [ ] `cd frontend && bun run build`（完成前端切片时）。
- [ ] 不引入第二套 WebSocket 库、UI 库或 CGO SQLite 驱动。
- [ ] 不把 TTY 数据写进 ACP Hub、SQLite 或普通日志。
- [ ] 不在未验证 Linux/macOS/Windows 行为前宣称跨平台完成。
- [ ] 最终执行 Browser TTY smoke test 和 `./scripts/build.sh`。

---

## 下一步

从 **B0-1** 开始：先落地 go-pty 依赖和共享认证辅助，再实现 TTY service 与平台 shell 选择。完成 B0-4 后冻结实际协议字段，再进入前端 xterm 实现。
