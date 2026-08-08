# zacp

一个基于 **ACP（Agent Client Protocol）** 协议的多 Agent Web 网关，让你在浏览器里同时使用多种 AI Agent 工具（如 Reasonix、Grok、Omp 等）进行对话，让原本不具备 Web 前端的 CLI Agent 也能在浏览器中使用。

![CleanShot 2026-08-06 at 15.52.53@2x.png](https://img.rss.ink/2026/08/06/xqyCT2Wq.png)

![CleanShot 2026-08-06 at 15.54.43@2x.png](https://img.rss.ink/2026/08/06/NhxMDQR8.png)

![CleanShot 2026-08-06 at 15.56.52@2x.png](https://img.rss.ink/2026/08/06/NgJ6OCzi.png)

## 实现原理

- 后端以 **ACP Client** 的身份连接各种支持 ACP 协议的 Agent（本地 stdio 子进程），负责 Agent 的启动、会话生命周期管理、消息流转发；
- 前端是一个浏览器 Web UI（Vue 3），通过 WebSocket 与后端实时通信，把 Agent 的流式输出、工具调用、权限请求实时展示出来，并支持多 Agent 切换、多会话管理；
- 你不需要为每个 Agent 单独安装一套客户端或记住不同的 CLI 用法，打开浏览器就能和所有 Agent 对话。

**技术栈**：后端 Go（Gin + acp-go-sdk + SQLite），前端 Vue 3 + Naive UI + Tailwind CSS，发布为**单个二进制**（前端页面已内嵌，无需安装 Node.js 环境）。

## 功能特性

- **多 Agent 接入**：内置 Reasonix、Grok、Omp、Qoder 等 Agent 适配，只需在配置文件
- **浏览器 Web UI**：会话式聊天界面，支持多会话、多 Agent 切换，流式输出实时展示（思考过程、工具调用、执行计划等）；
- **实时通信**：基于 WebSocket 的流式消息推送与权限回传，交互延迟低；
- **权限确认**：Agent 请求执行操作（读写文件、执行命令等）时，权限请求会推送到 Web UI 由你亲自确认，而不是服务端无脑自动放行（开发模式可配置自动批准）；
- **多Agent并行任务**：支持最多三个任务同时并行。
- **会话空闲回收**：Agent 空闲超过设定时间（默认 30 分钟）自动停止释放内存，下次使用自动恢复，不占用系统资源；
- **智能体管理**：设置页可随时启用/禁用某个 Agent，热更新配置，无需重启服务；
- **会话与消息持久化**：历史会话、消息记录存入本地 SQLite（WAL 模式），重启不丢失；
- **单二进制发布**：前端页面与默认配置全部内嵌进一个可执行文件，`--version` 查看版本，即下即用；
- **一键安装与更新**：提供 `install.sh` / `update.sh`（macOS / Linux）与 `install.ps1` / `update.ps1`（Windows）脚本，自动识别操作系统与 CPU 架构（amd64 / arm64），安装、升级、回滚一条命令搞定。
- **文件浏览器** ：支持文件上传、编辑、重命名等操作
- **Git面板** ：支持查看 Git 状态

## 安装

### macOS / Linux 一键安装（推荐）

自动检测操作系统与 CPU 架构，从 GitHub Releases 下载最新版本并安装：

```bash
curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/install.sh | bash
```

> 注意：如果您需要对接第三方Agent，比如Grok、Omp等需要先自行安装，然后在设置里面启用。

### Windows 一键安装

在 PowerShell 中执行（推荐）：

```powershell
irm https://raw.githubusercontent.com/helloxz/zacp/main/install.ps1 | iex
```

或在 CMD 中执行（下载到本地后运行，可指定版本）：

```cmd
curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/install.ps1 -o "%TEMP%\zacp-install.ps1" && powershell -ExecutionPolicy Bypass -File "%TEMP%\zacp-install.ps1"
```

安装脚本会把 bin 目录加入用户 PATH（`irm ... | iex` 方式安装后**当前终端立即可用**；`-File` 方式运行则新终端生效），并将最新版本复制为 `zacp.exe`，旧版本保留一份用于回滚。升级请用下面的 [一键更新](#一键更新推荐) 命令，不必重跑安装。

> 提示：`irm ... | iex` 会直接执行来自网络的脚本，请确认来源可信；首次启动若弹出 Windows 防火墙提示，允许后即可通过 `http://127.0.0.1:8680/` 访问。

### 首次启动

安装完成后直接运行 `zacp` 即可启动服务（默认监听 `:8680`），然后浏览器访问 `http://127.0.0.1:8680/` 使用 Web UI：

```bash
zacp
```

首次启动会自动生成配置文件 `~/.zacp/config.toml`，按需编辑其中 `[[agents]]` 的 Agent 命令等；运行时数据（SQLite 数据库）存放在 `~/.zacp/data/`。

## 更新

### 一键更新（推荐）

**Linux & macOS**

自动检测已安装的 zacp 并升级到最新版本：

```bash
curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/update.sh | bash
```

如果 zacp 安装在 `/usr/local/bin`（需要 root 权限），请用 `sudo` 执行：

```bash
curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/update.sh | sudo bash
```

**Windows**

Windows 用户在 PowerShell 中更新（自动检测已安装的 zacp 并升级到最新版本；需先停止正在运行的 `zacp` 进程，更新脚本不会自动结束它）：

```powershell
irm https://raw.githubusercontent.com/helloxz/zacp/main/update.ps1 | iex
```

或下载到本地后执行（可指定版本 / 强制重装）：

```cmd
curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/update.ps1 -o "%TEMP%\zacp-update.ps1" && powershell -ExecutionPolicy Bypass -File "%TEMP%\zacp-update.ps1"
```


## 联系作者

- 博客：<https://blog.xiaoz.org/>
- X（Twitter）：<https://x.com/xiaozblog>

欢迎反馈问题、提建议或参与贡献！
