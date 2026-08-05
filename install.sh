#!/usr/bin/env bash
# zacp 一键安装脚本：自动检测 macOS / Linux 与 CPU 架构，
# 从 GitHub Releases 下载对应平台的发布包并安装到用户目录。
#
# 设计要点：
#   - 自适应平台：uname 检测 OS（Darwin→darwin / Linux→linux）与架构（amd64/arm64），
#     也支持 --os/--arch 手动覆盖（如 Apple Silicon 上经 Rosetta 跑 amd64 版）。
#   - 版本策略：默认安装 latest release（经 GitHub API 解析 tag 与附件地址）；
#     也可 -v 指定版本（tag 约定 v 前缀，传 0.1.0 或 v0.1.0 均可，脚本自动规范化）。
#   - 安装位置：默认 ~/.local/bin（root 时 /usr/local/bin），可用 --dir 覆盖；
#     装完自动运行 zacp --version 验证，并在目录不在 PATH 时给出提示。
#   - 不生成配置：$ZACP_DATA/config.toml 由 zacp 首次启动自动创建（backend/internal/config/init.go），
#     安装脚本只负责放好二进制，不做额外配置。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/install.sh | bash
#   bash install.sh                            # 安装最新版
#   bash install.sh -v 0.1.0                   # 安装指定版本（v 前缀可省略）
#   bash install.sh --dir /usr/local/bin       # 指定安装目录（一般需 sudo）
#   bash install.sh --arch amd64               # 覆盖架构检测（如 Rosetta）
#   bash install.sh --base-url https://ghproxy.net/https://github.com  # 走加速镜像
#
# 环境变量（与参数等价，便于 CI / 管道使用）：
#   ZACP_REPO、ZACP_BASE_URL、ZACP_API_BASE
set -euo pipefail

# --- 可配置项（默认值） ---
REPO="${ZACP_REPO:-helloxz/zacp}"                              # GitHub owner/name
BASE_URL="${ZACP_BASE_URL:-https://github.com}"                # 附件下载域名（镜像加速时覆盖）
API_BASE="${ZACP_API_BASE:-https://api.github.com}"            # GitHub API 域名（latest 解析用）

VERSION=""        # 空 = latest
INSTALL_DIR=""    # 空 = 自动选择
FORCE_OS=""
FORCE_ARCH=""
STAGE_DIR=""       # 临时目录（全局，供 EXIT trap 清理；main 返回后仍可引用）

# EXIT 时清理临时目录。注意：不能把 trap 注册在 main 内引用局部变量，
# main 返回后局部变量销毁，set -u 下会报“未绑定变量”并污染退出码。
cleanup() {
  if [ -n "${STAGE_DIR:-}" ] && [ -d "$STAGE_DIR" ]; then
    rm -rf "$STAGE_DIR"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
zacp 一键安装脚本（自适应 macOS / Linux）

用法:
  bash install.sh [选项]

选项:
  -v, --version <版本>   安装指定版本（tag 约定 v 前缀，可传 0.1.0 或 v0.1.0）；缺省安装 latest
  -d, --dir <目录>       安装目录（缺省: ~/.local/bin，root 时 /usr/local/bin）
      --os <os>          覆盖系统检测: darwin | linux
      --arch <arch>      覆盖架构检测: amd64 | arm64
      --repo <owner/repo> 覆盖仓库（缺省: helloxz/zacp）
      --base-url <前缀>   下载域名前缀（如镜像 https://ghproxy.net/https://github.com）
      --api-base <url>    GitHub API 地址（latest 解析用；镜像场景一般与 --base-url 同加）
  -h, --help             显示本帮助

示例:
  bash install.sh
  bash install.sh -v 0.1.0 --dir /usr/local/bin
  bash install.sh --os darwin --arch amd64          # Apple Silicon 上装 amd64 版（Rosetta）
EOF
}

# --- 平台检测（可被 --os/--arch 覆盖） ---
detect_os() {
  local s
  s="$(uname -s)"
  case "$s" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "错误: 不支持的系统 ${s}（zacp 发布包仅提供 macOS / Linux）" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  local m
  m="$(uname -m)"
  case "$m" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "错误: 不支持的 CPU 架构 ${m}（zacp 发布包仅提供 amd64 / arm64）" >&2
      exit 1
      ;;
  esac
}

# --- 网络获取：curl 优先，wget 兜底；out 为空时输出到 stdout ---
fetch() {
  local url="${1:-}" out="${2:-}"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$out" ]; then curl -fsSL "$url" -o "$out"; else curl -fsSL "$url"; fi
  elif command -v wget >/dev/null 2>&1; then
    if [ -n "$out" ]; then wget -q "$url" -O "$out"; else wget -qO- "$url"; fi
  else
    echo "错误: 需要 curl 或 wget 之一" >&2
    exit 1
  fi
}

# --- 解压：unzip 优先，python3 标准库兜底（macOS / Linux 至少具备其一） ---
extract_zip() {
  local zipfile="$1" dest="$2"
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "$zipfile" -d "$dest"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m zipfile -e "$zipfile" "$dest"
  else
    echo "错误: 需要 unzip 或 python3 之一" >&2
    exit 1
  fi
}

# --- tag 规范化：0.1.0 → v0.1.0（项目约定 tag 必须 v 前缀） ---
norm_tag() {
  case "$1" in
    v*) echo "$1" ;;
    *) echo "v$1" ;;
  esac
}

# --- 解析 latest release：输出 "tag\nurl" 两行 ---
resolve_latest() {
  local os="$1" arch="$2" api_url api tag url
  api_url="${API_BASE}/repos/${REPO}/releases/latest"
  echo "==> 解析 latest release: ${api_url}" >&2
  api="$(fetch "$api_url" 2>/dev/null)" || api=""
  if [ -z "$api" ] || ! printf '%s' "$api" | grep -q '"tag_name"'; then
    # /releases/latest 只返回「非 prerelease、非 draft」的正式版；
    # 若仓库目前只有 prerelease（如 Beta 版），该接口会 404，
    # 此时回退到 releases 列表接口取最新一条（含 prerelease）。
    echo "==> 仓库暂无正式 release，回退到最新 release（含 prerelease）..." >&2
    api="$(fetch "${API_BASE}/repos/${REPO}/releases?per_page=1" 2>/dev/null)" || {
      echo "错误: 获取 release 列表失败（网络或 API 限流？）" >&2
      exit 1
    }
  fi
  # 纯文本提取，不依赖 jq/python：字段格式为 "key": "value"
  tag="$(printf '%s' "$api" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  url="$(printf '%s' "$api" | grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' | grep "${os}-${arch}.zip" | head -1 | sed -E 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  [ -n "$tag" ] || {
    echo "错误: latest release 响应中未找到 tag_name" >&2
    exit 1
  }
  [ -n "$url" ] || {
    echo "错误: latest release 未找到 ${os}/${arch} 平台附件（发布包缺失或 API 响应异常）" >&2
    exit 1
  }
  printf '%s\n%s\n' "$tag" "$url"
}

main() {
  local os arch tag url file dir bin

  # --- 参数解析 ---
  while [ $# -gt 0 ]; do
    case "$1" in
      -v|--version) VERSION="${2:?--version 需要一个值}"; shift 2 ;;
      -d|--dir) INSTALL_DIR="${2:?--dir 需要一个值}"; shift 2 ;;
      --os) FORCE_OS="${2:?--os 需要一个值}"; shift 2 ;;
      --arch) FORCE_ARCH="${2:?--arch 需要一个值}"; shift 2 ;;
      --repo) REPO="$2"; shift 2 ;;
      --base-url) BASE_URL="$2"; shift 2 ;;
      --api-base) API_BASE="$2"; shift 2 ;;
      -h|--help) usage; exit 0 ;;
      *) echo "错误: 未知参数 $1（用 --help 查看用法）" >&2; exit 2 ;;
    esac
  done

  # --- 平台检测 ---
  os="${FORCE_OS:-$(detect_os)}"
  arch="${FORCE_ARCH:-$(detect_arch)}"
  echo "==> 目标平台: ${os}/${arch}"

  # --- 确定 tag 与下载地址 ---
  if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
    # 一次请求拿 tag 与下载地址（resolve_latest 输出两行：tag / url）
    local latest_out
    latest_out="$(resolve_latest "$os" "$arch")"
    tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
    url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
  else
    tag="$(norm_tag "$VERSION")"
    file="zacp-${tag}-${os}-${arch}.zip"
    url="${BASE_URL}/${REPO}/releases/download/${tag}/${file}"
    echo "==> 版本: ${tag}"
  fi

  # --- 下载到临时目录 ---
  STAGE_DIR="$(mktemp -d)"
  zipfile="${STAGE_DIR}/pkg.zip"
  echo "==> 下载: ${url}"
  fetch "$url" "$zipfile" || {
    echo "错误: 下载失败（${url}）" >&2
    echo "请确认：1) 版本号/仓库名正确；2) 该平台（${os}/${arch}）发布包存在；3) 网络可访问 GitHub" >&2
    exit 1
  }
  [ -s "$zipfile" ] || {
    echo "错误: 下载失败或文件为空（${url}）" >&2
    exit 1
  }
  echo "==> 下载完成: $(wc -c < "$zipfile") bytes"

  # --- 解压并定位二进制 ---
  extract_zip "$zipfile" "${STAGE_DIR}/unpacked"
  # 发布包顶层目录为 zacp-v<版本>-<os>-<arch>/，二进制固定名为 zacp
  bin="$(find "${STAGE_DIR}/unpacked" -maxdepth 2 -type f -name zacp | head -1)"
  [ -n "$bin" ] || {
    echo "错误: 发布包内未找到 zacp 可执行文件" >&2
    exit 1
  }

  # --- 安装 ---
  local dir="$INSTALL_DIR"
  if [ -z "$dir" ]; then
    if [ "$(id -u)" = 0 ]; then
      dir="/usr/local/bin"
    else
      dir="${HOME}/.local/bin"
    fi
  fi
  if ! mkdir -p "$dir" 2>/dev/null && [ ! -w "$dir" ]; then
    echo "错误: 无法写入安装目录 ${dir}" >&2
    echo "可尝试: sudo bash $0 --dir ${dir}（或指定 --dir 到你有写权限的目录）" >&2
    exit 1
  fi
  install -m 0755 "$bin" "${dir}/zacp"
  echo "==> 已安装: ${dir}/zacp"

  # --- 验证 ---
  "${dir}/zacp" --version

  # --- PATH 提示（非 root 用户态安装常见） ---
  case ":$PATH:" in
    *":${dir}:"*) ;;
    *)
      echo ""
      echo "提示: ${dir} 不在当前 PATH 中，可执行以下之一使其生效："
      echo "  export PATH=\"${dir}:\$PATH\"    # 临时生效"
      if [ "$(uname -s)" = "Darwin" ]; then
        echo '  echo '"'"'export PATH="'${dir}':$PATH"'"'"' >> ~/.zshrc && source ~/.zshrc'
      else
        echo "  echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
      fi
      ;;
  esac

  echo ""
  echo "安装完成。首次运行会创建 ~/.zacp/config.toml，请按需编辑（如 [[agents]] 的 command）。"
}

main "$@"
