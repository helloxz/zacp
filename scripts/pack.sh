#!/usr/bin/env bash
# zacp 发布打包脚本：多平台矩阵编译 →（linux 平台 UPX 最高压缩）→ 组装标准 zip 发布包。
#
# 与 scripts/build.sh 分工（build.sh 保持不变）：
#   build.sh —— 本地开发快速构建单个二进制（不打包、不压缩）
#   pack.sh  —— 发布打包：多平台矩阵 + UPX + zip（供 GitHub Actions release.yml 调用，也可本机使用）
#
# 产物（backend/bin/ 下，与 build.sh 产物同目录）：
#   linux/*            -> zacp-v<版本>-linux-<GOARCH>.tar.gz
#   darwin|windows/*   -> zacp-v<版本>-<GOOS>-<GOARCH>.zip
#   包内结构（顶层目录，解压后不污染客户目录）：
#     zacp-v<版本>-<GOOS>-<GOARCH>/
#     └── zacp                      （Windows 平台为 zacp.exe）
#
# 格式选择：Linux 用 .tar.gz（tar 是所有发行版的基础工具，unzip 常未预装），
#   与 install.sh 的 Linux 下载路径对齐；macOS / Windows 保持 zip。
#
# 说明：发布包仅含二进制。README 与 config.example.toml 不随包分发——
#   config.example.toml 已由 go:embed 打进二进制（首次启动自动生成 ~/.zacp/config.toml）；
#   README 可在仓库 / Release 页面查看。
#
# 版本号单一来源：frontend/package.json 的 version 字段；
#   可用环境变量 ZACP_VERSION 覆盖（供 CI 手动触发等场景）。
#
# 用法：
#   ./scripts/pack.sh                        # 本机平台单包
#   ./scripts/pack.sh --all                  # 6 平台全量（CI 发布用）
#   GOOS=darwin GOARCH=arm64 ./scripts/pack.sh
#   ./scripts/pack.sh --skip-frontend        # 跳过前端构建（dist 已就位时）
#   ZACP_VERSION=0.1.0 ./scripts/pack.sh
#
# UPX 策略（基于实测与 UPX 官方支持状态，勿随意改动）：
#   linux/amd64、linux/arm64 → upx --best（官方支持，约 -73%~-75%）
#   darwin/* → 不压：UPX 4.2.0 起因 macOS 13+ 兼容性问题官方禁用 macOS 支持，至今（5.x）未恢复
#   windows/* → 不压：按约定规避杀软误报（UPX 亦不支持 windows/arm64）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# --- 参数解析 ---
ALL=0
SKIP_FRONTEND=0
for arg in "$@"; do
  case "$arg" in
    --all) ALL=1 ;;
    --skip-frontend) SKIP_FRONTEND=1 ;;
    *)
      echo "错误: 未知参数 $arg" >&2
      echo "用法: ./scripts/pack.sh [--all] [--skip-frontend]" >&2
      exit 2
      ;;
  esac
done

# --- 0. 前置检查 ---
# 前端构建依赖 bun（AGENTS.md 强制）；zip 打包工具按平台在打包循环内检查
# （linux 用 tar，不需要 zip；darwin/windows 需要 zip 或 python3 兜底）
if [ "$SKIP_FRONTEND" != 1 ] && ! command -v bun >/dev/null 2>&1; then
  echo "错误: 未找到 bun，请先安装 https://bun.sh（本项目前端禁止用 npm/pnpm/yarn）" >&2
  exit 1
fi

# --- 1. 版本号 / commit / 构建时间（-ldflags 注入，与 build.sh 同款） ---
# 版本号用 grep/sed 从 frontend/package.json 提取（不依赖 bun，--skip-frontend 场景也无需 bun）
VERSION="${ZACP_VERSION:-$(grep -m1 '"version":' frontend/package.json | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
  -X github.com/zacp/zacp/internal/version.Version=${VERSION} \
  -X github.com/zacp/zacp/internal/version.Commit=${COMMIT} \
  -X github.com/zacp/zacp/internal/version.BuildTime=${BUILD_TIME}"
echo "==> 版本: v${VERSION} (commit ${COMMIT}, built ${BUILD_TIME})"

# --- 2. 前端构建 + embed 拷贝（与 build.sh 相同约定） ---
# --skip-frontend 时跳过构建，但 dist 内容仍会重新拷贝（CI 中 dist 由前置步骤提供）
if [ "$SKIP_FRONTEND" != 1 ]; then
  echo "==> 编译前端 (bun install + bun run build)"
  (cd frontend && bun install && bun run build)
fi
echo "==> 拷贝前端产物 -> backend/internal/web/dist"
rm -rf backend/internal/web/dist
mkdir -p backend/internal/web/dist
cp -r frontend/dist/. backend/internal/web/dist/
# 占位文件兜底：即使构建产物为空也保证目录可入库编译（见 .gitignore）
: > backend/internal/web/dist/.gitkeep
# config.example.toml 双副本同步（仓库内 backend/configs/ 为唯一权威，见 AGENTS.md）
cp backend/configs/config.example.toml backend/internal/web/config.example.toml

# --- 3. 平台矩阵 ---
# 注意：go env GOOS 会继承 GOOS 环境变量，须剥离后取"真实本机平台"作缺省值
NATIVE_GOOS="$(env -u GOOS -u GOARCH go env GOOS)"
NATIVE_GOARCH="$(env -u GOOS -u GOARCH go env GOARCH)"
if [ "$ALL" = 1 ]; then
  PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
  echo "==> 全量平台矩阵: ${PLATFORMS}"
else
  PLATFORMS="${GOOS:-$NATIVE_GOOS}/${GOARCH:-$NATIVE_GOARCH}"
  echo "==> 单平台: ${PLATFORMS}"
fi

mkdir -p backend/bin
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# --- 4. 逐平台：编译 → UPX → 组装发布包（linux 用 tar.gz，其它平台用 zip）---
for p in $PLATFORMS; do
  GOOS="${p%/*}"
  GOARCH="${p#*/}"
  # 必须 export：go build 在子 shell 中执行，未导出的变量不会传给子进程，
  # 否则矩阵里所有平台都会被当作本机平台编译（曾导致 6 个包全是 amd64 的 bug）
  export GOOS GOARCH
  BIN="zacp"; [ "$GOOS" = "windows" ] && BIN="zacp.exe"
  DIR="zacp-v${VERSION}-${GOOS}-${GOARCH}"
  PKGDIR="${STAGE}/${DIR}"

  echo ""
  echo "==> [${GOOS}/${GOARCH}] 编译 (CGO_ENABLED=0)"
  mkdir -p "$PKGDIR"
  (cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$PKGDIR/$BIN" ./cmd/server)

  # UPX 最高压缩：仅 linux 平台（macOS 被 UPX 官方禁用，Windows 按约定不压）
  if [ "$GOOS" = "linux" ]; then
    if command -v upx >/dev/null 2>&1; then
      S_BEFORE="$(wc -c < "$PKGDIR/$BIN")"
      upx --best -q "$PKGDIR/$BIN"
      S_AFTER="$(wc -c < "$PKGDIR/$BIN")"
      echo "    UPX --best: ${S_BEFORE} -> ${S_AFTER} bytes ($((100 - S_AFTER * 100 / S_BEFORE))% 压缩)"
    else
      echo "    警告: 未找到 upx，linux 产物跳过压缩（CI 已安装；本机可 apt install upx-ucl）" >&2
    fi
  fi

  # 组装发布包内容：仅二进制（config.example.toml 已 embed 进二进制，见 backend/internal/web）
  if [ "$GOOS" = "linux" ]; then
    # Linux 用 .tar.gz：tar 是所有发行版的基础工具，unzip 常未预装，install.sh 下载路径依赖此格式
    OUT="${ROOT}/backend/bin/${DIR}.tar.gz"
    (cd "$STAGE" && tar -czf "$OUT" "$DIR")
  else
    # macOS / Windows 保持 zip；本机无 zip 命令时用 python3 标准库兜底（保留文件权限位）
    OUT="${ROOT}/backend/bin/${DIR}.zip"
    if command -v zip >/dev/null 2>&1; then
      (cd "$STAGE" && zip -qr "$OUT" "$DIR")
    elif command -v python3 >/dev/null 2>&1; then
      (cd "$STAGE" && python3 -m zipfile -c "$OUT" "$DIR")
    else
      echo "错误: 打包 ${GOOS}/${GOARCH} 需要 zip 命令或 python3" >&2
      exit 1
    fi
  fi
  echo "    package: $(basename "$OUT") ($(wc -c < "$OUT") bytes)"
done

echo ""
echo "完成: backend/bin/ 下共 $(( $(ls backend/bin/zacp-v*.tar.gz 2>/dev/null | wc -l) + $(ls backend/bin/zacp-v*.zip 2>/dev/null | wc -l) )) 个发布包"
