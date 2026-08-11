#!/usr/bin/env bash
# zacp 清理脚本：删除项目下所有 *_test.go 测试文件（backend/ 内）。
#
# 用途：需要移除测试文件（例如交付时剔除测试代码）时，可重复执行本脚本。
#
# 安全设计：
#   - 默认 dry-run：只列出将删除的文件，不实际删除；
#   - 加 -f/--force 后直接删除（不再询问）；
#   - 始终排除 node_modules 与 .git 目录，避免误删依赖/历史。
#
# 用法：
#   ./scripts/clean-test-files.sh              # 预览：列出将删除的 *_test.go
#   ./scripts/clean-test-files.sh -f           # 执行：直接删除（不询问）
#   ./scripts/clean-test-files.sh --root <dir> # 指定项目根目录（默认仓库根）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FORCE=0

# 解析参数：支持 -f/--force（强制删除）与 --root <dir>（覆盖根目录）
while [ $# -gt 0 ]; do
  case "$1" in
    -f|--force) FORCE=1 ;;
    --root)
      [ $# -ge 2 ] || { echo "错误: --root 需要跟一个目录参数" >&2; exit 1; }
      ROOT="$2"; shift ;;
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "错误: 未知参数 '$1'（-h 查看用法）" >&2
      exit 1 ;;
  esac
  shift
done

# 校验根目录存在，否则后续 find 会失败
[ -d "$ROOT" ] || { echo "错误: 根目录不存在: $ROOT" >&2; exit 1; }

# 收集目标文件：项目下的 *_test.go，排除 node_modules（依赖）与 .git（历史）
# 使用 -print0 保持文件名含空格/换行时仍安全；sort 固定输出顺序便于 diff。
mapfile -d '' FILES < <(
  find "$ROOT" -name '*_test.go' \
    -not -path '*/node_modules/*' \
    -not -path '*/.git/*' \
    -print0 | sort -z
)

COUNT="${#FILES[@]}"
if [ "$COUNT" -eq 0 ]; then
  echo "未找到任何 *_test.go 文件（根目录: $ROOT），无需清理。"
  exit 0
fi

echo "发现 $COUNT 个 *_test.go 文件（根目录: $ROOT）："
for f in "${FILES[@]}"; do
  echo "  - $f"
done
echo ""

if [ "$FORCE" -eq 0 ]; then
  echo "[dry-run] 未实际删除。确认无误后请加 -f/--force 执行删除。"
  exit 0
fi

# 正式删除（force 模式不再询问；如需要交互确认可去掉 -f 后手动二次确认）
echo "==> 正在删除 $COUNT 个文件 ..."
rm -v -- "${FILES[@]}"
echo "完成：已删除 $COUNT 个 *_test.go 文件。"
