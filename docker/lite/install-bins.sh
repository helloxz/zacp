#!/usr/bin/env bash
# scripts/install-bins.sh — Docker 构建阶段安装脚本
#
# 一次性把最新 release 的 zacp 与 zlite 安装到 /usr/bin。
# 由 Dockerfile 以 root 身份调用：bash /tmp/install-bins.sh [--arch amd64|arm64]
#
# 与仓库根目录 install.sh / zlite 的 install.sh 差异（容器场景简化）：
#   - 不做版本化多份保留/回滚：镜像本身是不可变快照，回滚 = 换镜像 tag
#   - 不生成符号链接：直接落盘为 /usr/bin/<bin> 真实文件
#   - 不依赖 jq/python：GitHub API 用 grep/sed 纯文本解析
#
# 外部可覆盖的环境变量（需要镜像源时设置）：
#   BASE_URL(默认 https://github.com)、API_BASE(默认 https://api.github.com)
set -euo pipefail

# --- 可配置默认值 ---
BASE_URL="${BASE_URL:-https://github.com}"      # 资产下载域名（可换 ghproxy 等镜像）
API_BASE="${API_BASE:-https://api.github.com}"  # GitHub API 域名（解析 latest 用）

FORCE_ARCH=""
STAGE_DIR=""  # 临时目录（由 EXIT trap 兜底清理）

# 任何退出路径都清理临时目录，避免镜像构建层里残留垃圾
cleanup() {
  if [ -n "${STAGE_DIR:-}" ] && [ -d "$STAGE_DIR" ]; then
    rm -rf "$STAGE_DIR"
  fi
}
trap cleanup EXIT

# --- 架构归一化：uname -m 输出 -> release 包命名使用的 linux 架构名 ---
detect_arch() {
  local m
  m="$(uname -m)"
  case "$m" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "error: unsupported CPU architecture ${m} (release 包仅提供 amd64 / arm64)" >&2
      exit 1
      ;;
  esac
}

# --- 网络拉取：curl 优先，wget 兜底；out 为空时 body 输出到 stdout ---
# 失败时打印 HTTP 状态码（便于区分限流 403/429 与网络不通 000/超时）；
# 设 GH_TOKEN/GITHUB_TOKEN 可将匿名 API 限额(60/h)提升到 5000/h，规避限流。
# 注意：body 与状态码必须分离——body 先入临时文件，状态码单独打 stderr，
#      成功后 body 才进 stdout/out 文件。切勿用 `>&2` 挂整条 curl（会把 body
#      一并转走，导致命令替换拿到空串而误报 no tag_name）。
fetch() {
  local url="${1:-}" out="${2:-}" auth=() code rc body
  # 认证参数（可选）：提升 GitHub API 速率上限
  if [ -n "${GH_TOKEN:-}" ]; then
    auth=(-H "Authorization: Bearer ${GH_TOKEN}")
  fi
  if command -v curl >/dev/null 2>&1; then
    body="$(mktemp)"
    # -w 把状态码输出到 stdout，由命令替换捕获；body 落临时文件，互不干扰
    code="$(curl -fsSL --max-time 60 "${auth[@]}" "$url" -o "$body" -sS -w '%{http_code}')"
    rc=$?
    printf '  fetch: HTTP %s\n' "$code" >&2
    if [ "$rc" -ne 0 ]; then
      echo "  error: curl failed (exit ${rc}): ${url}" >&2
      rm -f "$body"
      return 1
    fi
    if [ -n "$out" ]; then
      mv "$body" "$out"
    else
      cat "$body"
      rm -f "$body"
    fi
  elif command -v wget >/dev/null 2>&1; then
    if [ -n "$out" ]; then wget -q "$url" -O "$out"; else wget -qO- "$url"; fi
  else
    echo "error: curl or wget is required" >&2
    exit 1
  fi
}

# --- 解析 GitHub 最新 release：输出两行 "tag\nurl" ---
# /releases/latest 只返回非 prerelease；若仓库只有 prerelease 该接口会 404，
# 退回 releases 列表取最新一条（与 install.sh 行为一致）。
resolve_latest() {
  local repo="$1" arch="$2"
  local api api_url tag url
  local os="linux"
  api_url="${API_BASE}/repos/${repo}/releases/latest"
  # 首次试探 latest：静默（失败可能是网络不通或无 stable release，需回退判断）
  api="$(fetch "$api_url" 2>/dev/null || true)"
  if [ -z "$api" ] || ! printf '%s' "$api" | grep -q '"tag_name"'; then
    echo "==> No stable release for ${repo}, falling back to the newest release..." >&2
    # 回退请求不再吞错误：下方 fetch 的 HTTP 状态行会指出是限流(403/429)还是网络不通(000/超时)
    api="$(fetch "${API_BASE}/repos/${repo}/releases?per_page=1")" || {
      echo "error: failed to fetch release info for ${repo}（见上方 fetch 状态行：限流则设 GH_TOKEN，网络不通则检查代理/DNS）" >&2
      exit 1
    }
  fi
  # 纯文本提取，无 jq/python 依赖：字段形如 "key": "value"
  tag="$(printf '%s' "$api" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  url="$(printf '%s' "$api" | grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' | grep "${os}-${arch}.tar.gz" | head -1 | sed -E 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  [ -n "$tag" ] || {
    echo "error: no tag_name found in the latest release of ${repo}" >&2
    exit 1
  }
  [ -n "$url" ] || {
    echo "error: no linux-${arch}.tar.gz asset found for ${repo}（缺包或 API 响应异常）" >&2
    exit 1
  }
  printf '%s\n%s\n' "$tag" "$url"
}

# --- 安装单个工具：解析 latest -> 下载 -> 解包 -> 落盘 /usr/bin/<bin> ---
install_one() {
  local repo="$1" bin="$2" arch="$3"
  local latest_out tag url pkgfile binfile
  echo "==> Installing ${bin} (${repo}) ..."
  latest_out="$(resolve_latest "$repo" "$arch")"
  tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
  url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
  echo "==> ${bin} version: ${tag}"

  STAGE_DIR="$(mktemp -d)"
  pkgfile="${STAGE_DIR}/pkg.tar.gz"
  fetch "$url" "$pkgfile" || {
    echo "error: download failed（${url}）" >&2
    exit 1
  }
  [ -s "$pkgfile" ] || {
    echo "error: download failed or empty file（${url}）" >&2
    exit 1
  }
  echo "==> Downloaded: $(wc -c < "$pkgfile") bytes"

  # Linux 发行包为 .tar.gz，解包后是顶层目录 <bin>-v<version>-linux-<arch>/，
  # 其中二进制文件名固定为 <bin>（与 install.sh 的查找方式一致）
  tar -xzf "$pkgfile" -C "${STAGE_DIR}"
  binfile="$(find "${STAGE_DIR}" -maxdepth 2 -type f -name "$bin" | head -1)"
  [ -n "$binfile" ] || {
    echo "error: no ${bin} executable found in the release package" >&2
    exit 1
  }

  # 直接安装为真实文件（容器内不做版本化/符号链接）
  install -m 0755 "$binfile" "/usr/bin/${bin}"
  rm -rf "$STAGE_DIR"
  STAGE_DIR=""

  # 通过真实路径校验（镜像构建失败即退出，行为可预期）
  "/usr/bin/${bin}" --version
  echo "==> Installed /usr/bin/${bin}"
}

main() {
  # 参数解析：--arch 由 Dockerfile 注入 docker buildx 的 TARGETARCH；
  # 传入空值或未指定时回退到 uname -m 自动检测（兼容非 buildx 构建）
  while [ $# -gt 0 ]; do
    case "$1" in
      --arch) FORCE_ARCH="${2:-}"; shift 2 ;;
      -h|--help)
        echo "usage: $0 [--arch amd64|arm64]" >&2
        exit 0
        ;;
      *)
        echo "error: unknown argument $1 (use --help for usage)" >&2
        exit 2
        ;;
    esac
  done

  local arch
  arch="${FORCE_ARCH:-$(detect_arch)}"
  echo "==> Target platform: linux/${arch}"

  # 安装清单：zacp 与 zlite
  install_one "helloxz/zacp" "zacp" "$arch"
  install_one "helloxz/zlite" "zlite" "$arch"

  echo ""
  echo "All binaries installed: /usr/bin/zacp, /usr/bin/zlite"
}

main "$@"