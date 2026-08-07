#!/usr/bin/env bash
# zacp 一键构建：bun 编译前端 → 产物拷入后端 embed 目录 → go build 单一二进制。
#
# 产物：
#   backend/bin/zacp-v<版本>-<GOOS>-<GOARCH>   （Windows 追加 .exe）
#
# 版本号单一来源：frontend/package.json 的 version 字段（手动维护），
# 经 -ldflags 注入后端 internal/version 包，同时驱动：
#   - 二进制包名（本脚本）
#   - 后端 --version 输出
#   - GET /api/v1/version（前端设置页显示）
#
# 环境变量：
#   GOOS / GOARCH   交叉编译目标平台（缺省取本机 go env）
#   VITE_*          透传给 vite build 的构建参数（见 vite.config.ts）
#
# 用法：
#   ./scripts/build.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 0. 前置检查：AGENTS.md 规定前端包管理/构建一律用 bun，版本号读取也依赖 bun
if ! command -v bun >/dev/null 2>&1; then
  echo "错误: 未找到 bun，请先安装 https://bun.sh（本项目前端禁止用 npm/pnpm/yarn）" >&2
  exit 1
fi

# 1. 版本号 / commit / 构建时间（用于 -ldflags 注入）
# 版本号单一来源：frontend/package.json 的 version 字段（手动维护）
VERSION="$(bun -e "console.log(JSON.parse(await Bun.file('frontend/package.json').text()).version)")"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
  -X github.com/helloxz/zacp/internal/version.Version=${VERSION} \
  -X github.com/helloxz/zacp/internal/version.Commit=${COMMIT} \
  -X github.com/helloxz/zacp/internal/version.BuildTime=${BUILD_TIME}"
echo "==> 版本: v${VERSION} (commit ${COMMIT}, built ${BUILD_TIME})"

# 2. 编译前端
#     （bun 存在性已在上方检查）
echo "==> 编译前端 (bun install + bun run build)"
(cd frontend && bun install && bun run build)

# 3. 拷贝产物到后端 embed 目录（go:embed 不能引用 backend 外的文件，必须拷入）
#    dist 目录内容已被 .gitignore 忽略（仅 .gitkeep 入库），不影响仓库状态
echo "==> 拷贝前端产物 -> backend/internal/web/dist"
rm -rf backend/internal/web/dist
mkdir -p backend/internal/web/dist
cp -r frontend/dist/. backend/internal/web/dist/
#    保留 .gitkeep 占位（已入库）：保证未跑 build.sh 的裸 go build 也能编译，
#    且避免每次构建后 git status 出现 dist/.gitkeep 删除
: > backend/internal/web/dist/.gitkeep
#    同步配置示例（backend/configs 为权威，embed 副本仅供编译，构建时总是刷新）
cp backend/configs/config.example.toml backend/internal/web/config.example.toml

# 4. 编译后端（交叉编译支持：GOOS/GOARCH 环境变量；无 CGO 保证单二进制可移植）
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
OUT="zacp-v${VERSION}-${GOOS}-${GOARCH}"
[ "$GOOS" = "windows" ] && OUT="${OUT}.exe"
echo "==> 编译后端 (${GOOS}/${GOARCH}, CGO_ENABLED=0)"
(cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "bin/${OUT}" ./cmd/server)

echo ""
echo "完成: backend/bin/${OUT}"
# 交叉编译产物无法在本机执行，仅目标平台=本机平台时展示 --version 输出。
# 注意：go env GOOS 会继承 GOOS 环境变量，必须先剥离再取"真实本机平台"。
NATIVE_GOOS="$(env -u GOOS -u GOARCH go env GOOS)"
NATIVE_GOARCH="$(env -u GOOS -u GOARCH go env GOARCH)"
if [ "$GOOS" = "$NATIVE_GOOS" ] && [ "$GOARCH" = "$NATIVE_GOARCH" ]; then
  echo "      --version 输出: $(cd backend && ./bin/${OUT} --version)"
else
  echo "      （交叉编译产物 ${GOOS}/${GOARCH}，无法在本机执行验证）"
fi
