# zacp Web TTY 设计规格

> 状态：设计已获批准，待文档审阅  
> 日期：2026-08-11  
> 范围：工作区 Web TTY、xterm.js、多终端 Tab、独立 WebSocket 传输  
> 相关文档：`AGENTS.md`、`docs/superpowers/specs/2026-08-04-chat-workspace-design.md`

---

## 1. 背景与目标

zacp 已经具备工作区管理、文件浏览和 ACP 对话 WebSocket。本规格新增一个独立的 Web TTY 页面，让用户在浏览器中以工作区为初始目录启动本机 shell，并通过 xterm.js 进行交互。

### 1.1 目标

1. 支持 Linux、macOS、Windows，后端使用 `github.com/aymanbagabas/go-pty`。
2. 新增独立前端路由 `/tty?workspaceId={id}`，页面不混入聊天主区。
3. TTY 启动时以指定工作区路径作为 shell 的当前工作目录。
4. 用户可以通过 `+` 创建多个独立终端，并使用 Tab 切换、关闭终端。
5. 终端输入、ANSI 控制序列、中文、粘贴、基本鼠标报告和 resize 正常工作。
6. 复用项目现有的 `github.com/coder/websocket` 和认证子协议，但不复用 ACP 的消息协议与丢弃式消息队列。
7. 终端关闭、页面离开、WebSocket 断开时，PTY、shell 进程和相关 goroutine 都能清理。
8. 前端兼容现有 Naive UI、Tailwind CSS 和深色/浅色主题。

### 1.2 首期范围外

- 不做终端数据库持久化。
- 不做页面刷新后的终端恢复。
- 不做 WebSocket 短暂断线后的原进程恢复。
- 不保存终端输出、键盘输入或 shell 历史到 zacp 数据库。
- 不允许前端传入任意 shell 命令或任意 cwd。
- 不做 SSH、容器、远程主机或多 Server 终端。
- 不修改现有 ACP `/api/v1/ws` 的业务协议。
- 不提供终端录制、下载和分享功能。

---

## 2. 产品决策一览

| 主题 | 定稿 |
|------|------|
| 页面路由 | `/tty?workspaceId={id}` |
| 后端 WebSocket | `/api/v1/tty/ws?workspaceId={id}` |
| 终端生命周期 | 临时终端；Tab、页面或连接关闭即关闭 PTY |
| 连接模型 | 每个 Tab 一条独立 WebSocket |
| WebSocket 库 | 复用 `github.com/coder/websocket` |
| ACP 通道 | 与 TTY 完全隔离，继续使用现有 `/api/v1/ws` |
| PTY | `github.com/aymanbagabas/go-pty` |
| 数据帧 | PTY 输入/输出使用 Binary；控制消息使用 Text JSON |
| 工作目录 | 后端根据 `workspaceId` 查询 `Workspace.Path`，不信任前端路径 |
| Linux/macOS shell | `$SHELL`，无效或为空时回退 `/bin/sh` |
| Windows shell | `%COMSPEC%`，无效或为空时回退 `cmd.exe` |
| 初始尺寸 | 后端先以 `80x25` 创建，前端连接后立即发送真实尺寸 |
| 终端输出持久化 | 不持久化 |
| 终端 Tab 上限 | 单页面首期最多 6 个活动终端 |
| 后端活动终端上限 | 全进程最多 32 个，超限返回 429 |
| shell 自然退出 | 保留 Tab，显示已退出状态，允许用户关闭 |
| 关闭确认 | 运行中的 Tab 关闭前显示确认框 |
| 认证 | 复用现有 `zacp-auth.<token>` WebSocket 子协议 |
| 主题 | xterm 主题跟随现有 Naive UI 深色/浅色模式 |

---

## 3. 安全与不变量

Web TTY 等价于为浏览器提供当前 zacp 进程权限下的 shell，因此安全边界优先于 UI 便利性。

### 3.1 请求边界

1. WebSocket 握手必须经过与现有 ACP WebSocket 相同的主认证校验。
2. 客户端只提交 `workspaceId`，不得提交任意绝对路径、命令路径或命令参数。
3. 服务端必须重新查询 workspace，并确认记录未被软删除、路径存在且仍为目录。
4. `workspaceId` 非法、workspace 不存在或路径失效时，不得创建 PTY。
5. shell 环境继承服务端进程环境；不得把终端输入、输出、token 或完整路径写入普通日志。
6. TTY WebSocket 不得复用 ACP Hub 的“队列满则丢消息”行为。
7. Origin 校验不得为了 TTY 新功能进一步放宽；生产部署不能依赖 `InsecureSkipVerify` 作为安全措施。
8. 当前项目认证关闭时，TTY 行为与应用现有本机模式一致；远程部署必须启用认证并配置可信 Origin/访问边界。

### 3.2 生命周期不变量

1. 一个 TTY Session 只拥有一个 PTY、一个 shell 进程和一个 WebSocket。
2. Session 的关闭操作幂等，所有关闭路径最终汇聚到同一个清理函数。
3. WebSocket 断开后，临时模式下不得保留 shell 进程。
4. Manager 移除 Session 前必须完成资源关闭或进入明确的 closing 状态。
5. 终端输出不得静默丢弃；客户端无法持续接收时必须施加背压或关闭 Session。
6. 终端 Tab 切换不得销毁 xterm 实例；只有关闭 Tab 或离开页面才销毁。

---

## 4. 领域模型

TTY 不进入 SQLite。运行时模型只存在于内存中：

```text
TtyPageState（前端页面级）
  workspaceId, workspace, activeTabId
  └── TtyTab（前端）
        id, title, status, Terminal, FitAddon, WebSocket

TtyManager（后端进程级）
  └── TtySession
        terminalId, workspaceId, workspacePath
        pty, command, websocket, status, closeOnce
```

| 概念 | 说明 |
|------|------|
| TtyPageState | `/tty` 页面生命周期内的状态，不跨刷新保存 |
| TtyTab | 一个浏览器 Tab 对应一个 shell/PTY/WebSocket |
| TtySession | 后端单个 PTY 运行实例 |
| terminalId | 服务端生成的短期终端 ID，仅用于 ready/exit/error 关联 |
| workspaceId | 现有数据库 Workspace 主键 |
| workspacePath | 后端查询得到的绝对路径，只作为 shell 初始 cwd |

---

## 5. 前端信息架构

### 5.1 路由

新增独立路由：

```text
/tty?workspaceId=123
```

进入页面后：

1. 读取 query 中的 `workspaceId`。
2. 校验它是正整数。
3. 调用 `GET /api/v1/workspaces/:id`。
4. 成功后显示工作区名称，并自动创建第一个 TTY Tab。
5. 缺少或非法 workspaceId 时展示错误空态，不启动终端。

现有全局认证守卫继续覆盖该路由。TTY 页面不挂载 `AppShell`，避免启动不必要的聊天主区和 ACP socket 业务状态。

### 5.2 组件职责

```text
TtyPage.vue
  ├── TtyTabs.vue
  └── TtyTerminal.vue（每个活动 Tab 一个实例）
```

#### `TtyPage.vue`

- 解析 workspaceId。
- 加载工作区信息。
- 持有页面级 `useTtyManager()`。
- 管理 activeTabId。
- 监听路由离开并关闭全部临时终端。
- 展示加载、错误、空态和全局通知。

#### `TtyTabs.vue`

- 使用 Naive UI Tab 能力展示终端标题和状态。
- 提供新增按钮。
- 提供关闭按钮。
- 运行中的终端关闭前调用 `useDialog()` 确认。
- 达到 6 个活动 Tab 后禁用新增并提示用户。

#### `TtyTerminal.vue`

- 创建并销毁 xterm `Terminal`。
- 加载 `FitAddon`。
- 打开 WebSocket。
- 将 xterm `onData`/`onBinary` 转成 Binary 输入帧。
- 将 Binary 输出帧交给 `terminal.write()`。
- 监听 `onResize`，发送 `resize` 控制帧。
- 使用 `ResizeObserver` 和 Tab 激活事件触发 fit。
- 暴露 focus、fit、close 等页面级操作。

### 5.3 Tab 状态

```text
creating
connecting
connected
exited
closing
closed
error
```

Tab 标题首期使用 `终端 1`、`终端 2` 等稳定名称。shell 通过 OSC 设置标题的能力不作为首期依赖，避免把不可信终端标题直接写入 HTML。

### 5.4 主题与布局

- 终端区域使用 `flex-1 min-h-0 overflow-hidden`，避免页面出现双滚动条。
- 终端背景和前景色根据 `appStore.isDark` 更新。
- 字体使用系统等宽字体，并保留合理行高。
- 所有 xterm 实例保持挂载；非活动实例隐藏但不销毁。
- Tab 激活后在下一次渲染周期执行 `fit()`，不向后端发送 `0x0` 尺寸。

---

## 6. 前端状态与通信

建议 `useTtyManager()` 为页面级 composable，不新增全局 Pinia store。临时终端不需要跨路由或跨刷新共享状态。

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

内部使用 `Map<string, TtyTabState>` 保存 Tab，使用 `activeTabId` 控制显示。

### 6.1 创建 Tab

1. 检查当前活动 Tab 数是否达到 8。
2. 生成前端临时 Tab ID。
3. 创建 xterm 实例和 FitAddon。
4. 建立 `/api/v1/tty/ws?workspaceId=...`。
5. WebSocket 打开后发送当前尺寸。
6. 收到 `ready` 后状态变为 connected。
7. 将终端聚焦。

### 6.2 关闭 Tab

1. 如果状态为 connected，弹出确认框。
2. 标记 closing，禁止重复点击。
3. 主动关闭 WebSocket。
4. 销毁 xterm 与所有 addon/listener。
5. 从 Tab Map 删除。
6. 关闭最后一个 Tab 后展示空态，不自动创建；页面初次打开时才自动创建首个 Tab。

### 6.3 页面离开

使用路由离开守卫和组件卸载兜底关闭所有 WebSocket。后端以连接关闭作为最终清理触发，不依赖前端一定能发送 close 控制帧。

---

## 7. 后端模块设计

### 7.1 `internal/tty/manager.go`

`Manager` 负责：

- 注册和删除活动 Session；
- 生成 terminalId；
限制活动 Session 数量，进程级上限为 32 个；
- 服务关闭时关闭全部 Session；
- 提供活动数量和调试级状态，不返回终端输入输出。

Manager 不负责 workspace 路径解析，不直接读取 Gin 参数。

### 7.2 `internal/tty/session.go`

`Session` 负责单个终端：

- 创建 PTY；
- 启动 shell；
- 读取 PTY 输出；
- 将 WebSocket binary 输入写入 PTY；
- 处理 resize；
- 监听 command Wait；
- 幂等关闭和资源回收。

Session 内部的中文关键注释必须说明：

- PTY 与 shell 的所有权关系；
- 读写 goroutine 如何退出；
- 为什么关闭顺序不能颠倒；
- Unix 进程组和 Windows ConPTY 的差异。

### 7.3 `internal/tty/handler.go`

Handler 负责：

1. 读取和校验 query workspaceId。
2. 使用共享 WebSocket 认证逻辑完成握手校验。
3. 通过 service/repository 查询 Workspace。
4. 重新确认路径存在且为目录。
5. 检查 Manager 的进程级活动 Session 上限。
6. 完成 WebSocket Accept。
7. 调用 Manager 创建 Session；PTY 或 shell 启动失败时发送 `error` 并关闭连接。
8. 启动 WebSocket read loop 和 write loop。
9. 连接结束后触发 Session.Close。

Handler 不直接接受客户端 cwd、shell 或 command 参数。

### 7.4 工作区解析

TTY 业务应通过服务层或注入的 workspace resolver 获取 Workspace，遵循现有分层约定：

```text
router → tty handler → tty/service → workspace repository
```

TTY 不复用文件编辑的相对路径解析函数，因为 TTY 需要的是已登记 Workspace 的根目录，而不是用户提交的文件相对路径。

---

## 8. PTY 启动与平台行为

### 8.1 Unix

- 优先读取 `$SHELL`。
- 路径为空、不可执行或启动失败时回退 `/bin/sh`。
- 设置 `Cmd.Dir = workspace.Path`。
- 保留现有环境，并补充终端需要的环境变量时避免覆盖 `os.Environ()`。
- 通过 `pty.Resize(cols, rows)` 更新尺寸。
- 关闭时同时处理 shell 进程和其进程组，防止子进程残留。

### 8.2 Windows

- 优先使用 `%COMSPEC%`。
- 空值或启动失败时回退 `cmd.exe`。
- 使用 go-pty 的 ConPTY 实现。
- 通过 `pty.Resize(cols, rows)` 调用 `ResizePseudoConsole`。
- 关闭时验证 ConPTY、输入输出 pipe 和子进程的释放顺序。
- Windows cmd 和 PowerShell 不作为同一 shell 处理；首期默认只保证 `%COMSPEC%`。

### 8.3 尺寸

服务端以 `80x25` 创建 PTY，收到前端首个 `resize` 后立即更新。服务端限制：

```text
1 <= cols <= 1000
1 <= rows <= 500
```

超出范围的 resize 直接返回协议错误，不调用 PTY。

---

## 9. TTY WebSocket 协议

**端点**：

```text
GET /api/v1/tty/ws?workspaceId={id}
```

认证方式与现有 `/api/v1/ws` 相同：

```text
Sec-WebSocket-Protocol: zacp-auth.<token>
```

### 9.1 客户端 → 服务端

| 帧类型 | 结构 | 说明 |
|--------|------|------|
| Binary | 原始 bytes | 键盘、粘贴、鼠标报告等 PTY 输入 |
| Text | `{"type":"resize","cols":N,"rows":N}` | 更新 PTY 尺寸 |

服务端使用 WebSocket Ping 进行连接保活；TTY 业务协议不定义应用层 ping/pong 帧。

未知 Text 控制类型返回 `unknown_message` 错误并关闭当前 Session，不影响其它 Tab。

### 9.2 服务端 → 客户端

| 帧类型 | 结构 | 说明 |
|--------|------|------|
| Binary | 原始 bytes | PTY 原始输出 |
| Text | `{"type":"ready","terminalId":"..."}` | PTY 和 shell 已启动 |
| Text | `{"type":"exit","code":N}` | shell 已退出 |
| Text | `{"type":"error","code":"...","message":"..."}` | 当前终端错误 |

### 9.3 帧处理不变量

1. Binary 输入不得经过 JSON、base64 或强制 UTF-8 转换。
2. Binary 输出交给 xterm.js 的 `write`，不得在后端重写 ANSI 序列。
3. 一个连接使用单一 outbound writer，保证控制帧和 Binary 输出有序。
4. 单个 Binary 输入帧最大 1 MiB，超过上限关闭当前 Session，不影响其它 Tab。
5. 输出队列总容量为 4 MiB；达到高水位时阻塞读取，禁止静默丢弃。
6. `exit` 发送后连接进入关闭流程，前端保留 Tab 状态但不能继续发送输入。

---

## 10. 背压与资源限制

xterm.js 的写入是非阻塞的，PTY 可能以远高于浏览器渲染能力的速度产生数据。后端使用有界 outbound 缓冲：

```text
PTY reader → bounded output queue → single WebSocket writer → browser xterm
```

策略：

- 正常速度：批量读取并发送 Binary 帧。
- 队列接近 4 MiB 高水位：暂停继续读取，借助 PTY/OS 背压。
- WebSocket writer 持续阻塞超过 10 秒：尝试发送 `error`，随后关闭当前 Session。
- 队列不得超过 4 MiB；超限不得丢弃旧数据或无限扩容。
- 单页面最多 6 个活动 Tab，进程全局最多 32 个活动 Session；超限只影响新建请求。

不使用当前 ACP `Client.send` 的 256 长度丢弃队列。

---

## 11. 错误处理

### 11.1 HTTP/握手阶段

| 场景 | 行为 |
|------|------|
| workspaceId 缺失 | 返回 400 |
| workspaceId 非数字 | 返回 400 |
| workspace 不存在/已删除 | 返回 404 |
| workspace 路径不存在或不是目录 | 返回 409 |
| 认证失败 | 返回 401 |
| 活动终端达到上限 | 返回 429 |
| PTY 创建失败 | WebSocket 已建立时发送 `error` 并关闭 Session |
| shell 启动失败 | WebSocket 已建立时发送 `error` 并关闭 Session |

### 11.2 运行阶段

- PTY 读取错误：发送 `error`（如果连接仍可写），随后关闭 Session。
- WebSocket 读取错误：关闭 Session，不重启原进程。
- WebSocket 写入超时：关闭 Session，避免 goroutine 和进程泄漏。
- shell 非零退出：发送 `exit` 并保留退出码。
- resize 参数非法：发送协议错误，不改变旧尺寸。

前端错误消息展示给用户时应避免直接泄露系统内部绝对路径、命令行参数或完整底层堆栈。

---

## 12. 视觉与交互规范

1. 页面采用与现有应用一致的 sky 主色、surface、divider、ink token。
2. 深色模式使用深色 xterm 配色，浅色模式使用浅色 xterm 配色。
3. Tab 使用紧凑标题、状态点和明确的关闭按钮。
4. 连接中显示轻量 loading，不遮挡已经存在的其它终端。
5. 新建终端后自动聚焦输入区。
6. 关闭确认使用 Naive UI dialog，不使用浏览器原生 confirm。
7. 空工作区、错误工作区和 shell 退出分别展示明确文案。
8. 页面和 Tab 操作支持键盘可达，关闭按钮具备 aria-label。
9. 终端输出只通过 xterm.js 渲染，不使用 `innerHTML`。
10. 终端标题、链接或 OSC 数据均视为不可信内容。

---

## 13. 测试与验收

### 13.1 后端单元测试

- workspaceId 解析；
- workspace 路径重新校验；
- shell 选择与回退；
- resize 边界；
- Text 控制帧解析；
- unknown message 处理；
- Session.Close 幂等性；
- Manager 上限；
- output queue 高水位行为。

### 13.2 后端集成测试

Linux 环境至少验证：

1. 创建终端后执行 `pwd`，输出为 workspace.Path。
2. 执行带 ANSI 颜色的命令，Binary 输出完整到达客户端。
3. 输入中文和换行，PTY 能正确回显。
4. 执行长时间命令后关闭 Tab，进程和 goroutine 都退出。
5. 发送 resize 后，`stty size` 反映新尺寸。
6. 执行 `yes` 后发送 Ctrl+C，连接仍能及时响应。
7. WebSocket 异常断开后，shell 不残留。

### 13.3 前端浏览器验证

1. 进入 `/tty?workspaceId=id` 自动创建首个 Tab。
2. 点击 `+` 创建多个终端。
3. 每个终端执行不同命令，输出不串台。
4. 切换 Tab 后光标、滚动位置和输出保留。
5. 关闭运行中的 Tab 需要确认，关闭后 shell 退出。
6. shell 自然退出后 Tab 显示退出状态。
7. 浏览器 resize 后终端布局正确。
8. 深色/浅色切换不破坏终端可读性。
9. workspace 不存在时显示错误空态。
10. 页面离开后所有临时终端都关闭。

### 13.4 跨平台验证

必须在真实系统上运行：

- Linux：`$SHELL` 和 `/bin/sh` 回退；
- macOS：zsh 或用户默认 shell；
- Windows：`%COMSPEC%`/cmd.exe 和 ConPTY resize；
- Windows 编码、Ctrl+C、中文输出和进程退出。

交叉编译只作为构建检查，不能替代实际 PTY 运行验证。

---

## 14. 预计改动文件

后端：

```text
backend/go.mod
backend/go.sum
backend/cmd/server/main.go
backend/internal/api/router/router.go
backend/internal/tty/*.go
backend/internal/ws/*        # 抽取共享 WebSocket 认证辅助逻辑
```

新增服务层文件：

```text
backend/internal/service/tty.go
```

前端：

```text
frontend/package.json
frontend/bun.lock
frontend/src/router/index.ts
frontend/src/pages/TtyPage.vue
frontend/src/components/tty/*.vue
frontend/src/composables/useTtyManager.ts
frontend/src/composables/useTtySocket.ts
frontend/src/types/tty.ts
frontend/src/styles/main.css
```

首期不修改 `model`、数据库迁移和 SQLite 表结构。

---

## 15. 参考资料

- [go-pty](https://github.com/aymanbagabas/go-pty)
- [go-pty v0.2.3](https://github.com/aymanbagabas/go-pty/releases/tag/v0.2.3)
- [coder/websocket](https://github.com/coder/websocket)
- [xterm.js Importing](https://xtermjs.org/docs/guides/import/)
- [xterm.js Addons](https://xtermjs.org/docs/guides/using-addons/)
- [xterm.js Flow Control](https://xtermjs.org/docs/guides/flowcontrol/)
- [xterm.js Security](https://xtermjs.org/docs/guides/security/)
- [Microsoft ConPTY](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session)

实现以本规格和代码为准；若实现需要偏离本规格，应先更新本文件并重新审阅。

