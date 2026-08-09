#!/usr/bin/env bash
# zacp self-updater (macOS / Linux): updates an existing zacp installation to
# the latest release, keeping the upgrade-friendly layout.
#
# IMPORTANT: this script SHARES its core logic with install.sh (fetch, pkg_ext,
# extract_pkg, norm_tag, sort_ver, resolve_latest and the versioned-install
# steps). Keep both scripts in sync when the package format or install layout
# changes — edit install.sh first, then mirror the change here.
#
# Design notes:
#   - Requires an existing installation: if zacp is not found (on PATH or at the
#     default locations), it prints an install hint and exits.
#   - Entry detection: a symlink means the versioned layout (normal upgrade);
#     a plain binary means a legacy install, which is migrated automatically
#     (ln -sfn replaces the plain file with a symlink).
#   - Version check: compares the local version with the latest remote release
#     and skips the download when already up to date; --force bypasses.
#     Local version comes from the symlink target dir name (zacp-<version>) or,
#     for a legacy plain binary, from `zacp --version` (version 2 of the output).
#   - Safety: the new version is fully placed under <bin-dir>/zacp-<version>
#     BEFORE the entry symlink is switched, so a failed download/extract never
#     breaks the current installation (no "remove first, download later").
#   - Runtime state (config / db under $ZACP_DATA, default ~/.zacp) is never
#     touched by an update; only binaries are replaced. Legacy installs whose
#     binary dir is the historical default ~/.acp/bin are migrated to
#     ~/.zacp/bin and the old dir removed, so future updates land in the
#     right place (a custom ZACP_BIN_DIR is never touched).
#
# Usage:
#   bash update.sh                            # update to the latest release
#   bash update.sh --force                    # reinstall even when already latest
#   bash update.sh -v 0.1.0                   # update to a specific version
#   sudo bash update.sh                       # when installed to /usr/local/bin
#   bash update.sh --base-url https://ghproxy.net/https://github.com  # use a mirror
#
# Environment variables (equivalent to the flags, handy for CI / pipelines):
#   ZACP_REPO, ZACP_BASE_URL, ZACP_API_BASE, ZACP_BIN_DIR
set -euo pipefail

# --- Configurable defaults (must match install.sh) ---
REPO="${ZACP_REPO:-helloxz/zacp}"                              # GitHub owner/name
BASE_URL="${ZACP_BASE_URL:-https://github.com}"                # asset download domain (override for mirrors)
API_BASE="${ZACP_API_BASE:-https://api.github.com}"            # GitHub API domain (for resolving latest)

VERSION=""        # empty = latest
FORCE=0           # 1 = skip the version check and reinstall
INSTALL_DIR=""    # empty = detect from the existing install
FORCE_OS=""
FORCE_ARCH=""
STAGE_DIR=""      # temp dir (global, referenced by the EXIT trap; must survive main's return)
MIGRATED_ENTRY=""  # set by migrate_old_bin_dir when a legacy entry is relocated (read by main)

# Clean up the temp dir on EXIT. Note: the trap must not be registered inside main
# referencing local variables: after main returns the locals are gone, and with
# `set -u` that would report "unbound variable" and pollute the exit code.
cleanup() {
  if [ -n "${STAGE_DIR:-}" ] && [ -d "$STAGE_DIR" ]; then
    rm -rf "$STAGE_DIR"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
zacp updater (macOS / Linux)

Usage:
  bash update.sh [options]

Options:
  -f, --force           Update even when the local version is already the latest
  -v, --version <ver>   Update to a specific version (tags use a v prefix; pass 0.1.0 or v0.1.0); default: latest
  -d, --dir <dir>       Command (symlink) directory (default: detected from the existing install)
      --os <os>         Override OS detection: darwin | linux
      --arch <arch>     Override architecture detection: amd64 | arm64
      --repo <owner/repo>  Override repository (default: helloxz/zacp)
      --base-url <prefix>  Download URL prefix (e.g. mirror https://ghproxy.net/https://github.com)
      --api-base <url>  GitHub API URL (for resolving latest; usually set together with --base-url)
  -h, --help            Show this help

Install layout (managed by install.sh; update.sh keeps it):
  <dir>/zacp                  symlink to the current version (the command on PATH)
  ~/.zacp/bin/zacp-<version>   versioned binary (e.g. ~/.zacp/bin/zacp-0.1.0; override with ZACP_BIN_DIR)
  The previous version is kept for rollback; older ones are pruned.

Examples:
  bash update.sh
  bash update.sh --force
  bash update.sh -v 0.1.0
  sudo bash update.sh          # when installed to /usr/local/bin
EOF
}

# --- Platform detection (overridable via --os/--arch) ---
detect_os() {
  local s
  s="$(uname -s)"
  case "$s" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "error: unsupported OS ${s} (zacp release packages are only provided for macOS / Linux)" >&2
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
      echo "error: unsupported CPU architecture ${m} (zacp release packages are only provided for amd64 / arm64)" >&2
      exit 1
      ;;
  esac
}

# --- Network fetch: curl preferred, wget as fallback; empty out means stdout ---
fetch() {
  local url="${1:-}" out="${2:-}"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$out" ]; then curl -fsSL "$url" -o "$out"; else curl -fsSL "$url"; fi
  elif command -v wget >/dev/null 2>&1; then
    if [ -n "$out" ]; then wget -q "$url" -O "$out"; else wget -qO- "$url"; fi
  else
    echo "error: curl or wget is required" >&2
    exit 1
  fi
}

# --- Package format by platform: Linux -> .tar.gz (tar is a base tool on every
#     distro, unzip often missing); macOS/Windows -> .zip ---
pkg_ext() {
  local os="$1"
  case "$os" in
    linux) echo "tar.gz" ;;
    *) echo "zip" ;;
  esac
}

# --- Extract: Linux uses tar; others unzip preferred, python3 stdlib as fallback ---
extract_pkg() {
  local pkgfile="$1" dest="$2" os="$3"
  mkdir -p "$dest"
  if [ "$os" = "linux" ]; then
    tar -xzf "$pkgfile" -C "$dest"
  elif command -v unzip >/dev/null 2>&1; then
    unzip -q "$pkgfile" -d "$dest"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m zipfile -e "$pkgfile" "$dest"
  else
    echo "error: unzip or python3 is required" >&2
    exit 1
  fi
}

# --- Tag normalization: 0.1.0 -> v0.1.0 (project convention: tags must have a v prefix) ---
norm_tag() {
  case "$1" in
    v*) echo "$1" ;;
    *) echo "v$1" ;;
  esac
}

# --- Version sort: GNU `sort -V` preferred, dot-segment numeric sort as fallback (BSD/macOS) ---
sort_ver() {
  if sort -V </dev/null >/dev/null 2>&1; then
    sort -V "$@"
  else
    sort -t. -k1,1n -k2,2n -k3,3n "$@"
  fi
}

# --- Resolve the latest release: prints "tag\nurl" two lines ---
resolve_latest() {
  local os="$1" arch="$2" api_url api tag url
  api_url="${API_BASE}/repos/${REPO}/releases/latest"
  echo "==> Resolving latest release: ${api_url}" >&2
  api="$(fetch "$api_url" 2>/dev/null)" || api=""
  if [ -z "$api" ] || ! printf '%s' "$api" | grep -q '"tag_name"'; then
    # /releases/latest only returns non-prerelease, non-draft releases;
    # if the repo only has prereleases (e.g. a Beta), that endpoint 404s,
    # so fall back to the releases list and take the newest one (incl. prereleases).
    echo "==> No stable release yet, falling back to the newest release (including prereleases)..." >&2
    api="$(fetch "${API_BASE}/repos/${REPO}/releases?per_page=1" 2>/dev/null)" || {
      echo "error: failed to fetch the release list (network issue or API rate limit?)" >&2
      exit 1
    }
  fi
  # Plain-text extraction, no jq/python dependency: fields look like "key": "value"
  tag="$(printf '%s' "$api" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  url="$(printf '%s' "$api" | grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' | grep "${os}-${arch}.$(pkg_ext "$os")" | head -1 | sed -E 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/')" || true
  [ -n "$tag" ] || {
    echo "error: no tag_name found in the latest release response" >&2
    exit 1
  }
  [ -n "$url" ] || {
    echo "error: no ${os}/${arch} asset found in the latest release (missing package or unexpected API response)" >&2
    exit 1
  }
  printf '%s\n%s\n' "$tag" "$url"
}

# --- Locate the installed entry: --dir first, then PATH, then the defaults ---
find_entry() {
  if [ -n "$INSTALL_DIR" ]; then
    if [ -e "$INSTALL_DIR/zacp" ] || [ -L "$INSTALL_DIR/zacp" ]; then
      echo "$INSTALL_DIR/zacp"; return 0
    fi
    return 1
  fi
  local cmd p
  cmd="$(type -P zacp 2>/dev/null)" || true
  if [ -n "$cmd" ] && { [ -e "$cmd" ] || [ -L "$cmd" ]; }; then
    echo "$cmd"; return 0
  fi
  for p in "${HOME}/.local/bin/zacp" "/usr/local/bin/zacp"; do
    if [ -e "$p" ] || [ -L "$p" ]; then
      echo "$p"; return 0
    fi
  done
  return 1
}

# --- Resolve a symlink to an absolute path without GNU `readlink -f`
#     (macOS's BSD readlink has no -f). Handles absolute and relative targets. ---
resolve_link() {
  local link="$1" t
  t="$(readlink "$link")"
  case "$t" in
    /*) printf '%s\n' "$t" ;;
    *)  (cd "$(dirname "$link")" && printf '%s\n' "$(pwd)/$t") ;;
  esac
}

# --- Local version: symlink target dir name (zacp-<ver>) preferred (works even
#     if the binary is broken); legacy plain binary falls back to `--version` ---
local_version() {
  local entry="$1" real ver
  if [ -L "$entry" ]; then
    real="$(resolve_link "$entry")"
    ver="$(basename "$real")"
    ver="${ver#zacp-}"
  else
    ver="$("$entry" --version 2>/dev/null | awk '{print $2}')" || true
    ver="${ver#v}"
  fi
  echo "$ver"
}

# --- Migrate a legacy install from the historical default bin dir
#     (~/.acp/bin) to the correct one (~/.zacp/bin) ---
# 背景：旧版 install.sh 默认把二进制装到 ~/.acp/bin，正确位置是 $ZACP_DATA/bin
# （默认 ~/.zacp/bin）。本函数在更新的**末尾**执行——此时新版本已安装、已验证、
# 入口已指向它——所以这里的任何失败都不破坏本次更新（旧目录保留，可手动清理）。
# 安全规则：
#   - 只处理已知的坏默认目录 $HOME/.acp/bin；自定义 ZACP_BIN_DIR 或其他目录一律不动。
#   - legacy 平铺安装的入口若在旧目录内，先搬到标准命令目录再删旧目录，
#     避免删除旧目录时把命令入口一起删掉（入口位置通过全局 MIGRATED_ENTRY 回报 main）。
#   - 旧目录仅当其中只剩 zacp 相关文件时才整目录删除；否则只删 zacp 文件、
#     保留目录并提示（防误删用户数据）。
#   - 任何失败只打印 warning 并返回 0，更新本身保持成功。
migrate_old_bin_dir() {
  local old="$1" entry="$2" new="$3"
  local f name cur_target leftovers newest_prev new_entry
  [ -n "$HOME" ] || return 0
  [ "$old" = "${HOME}/.acp/bin" ] || return 0
  case "$old" in /*) ;; *) return 0 ;; esac   # 只接受绝对路径（防 HOME 被设成相对路径）
  [ -d "$old" ] || return 0
  [ -n "${ZACP_BIN_DIR:-}" ] && return 0      # 用户显式指定目录：不自动迁移

  echo "==> Migrating legacy binary dir: ${old} -> ${new}"

  # 当前版本文件名（入口此刻已是指向新目录中刚安装版本的 symlink）
  cur_target="$(basename "$(resolve_link "$entry")")"

  # Legacy 平铺安装：入口本身在旧目录内（此刻已被主流程 ln -sfn 换成 symlink）。
  # 先把它搬到标准命令目录 ~/.local/bin（install.sh 的默认入口位置），
  # 避免删除旧目录时把命令入口一起删掉。
  if [ "$entry" = "${old}/zacp" ]; then
    new_entry="${HOME}/.local/bin/zacp"
    if mkdir -p "${HOME}/.local/bin" 2>/dev/null && [ -w "${HOME}/.local/bin" ]; then
      if ln -sfn "${new}/${cur_target}" "$new_entry" && rm -f "$entry"; then
        echo "==> Moved entry: ${entry} -> ${new_entry}"
        MIGRATED_ENTRY="$new_entry"
        entry="$new_entry"
      else
        echo "warning: failed to move ${entry} to ${new_entry}; keeping ${old}" >&2
        return 0
      fi
    else
      echo "warning: cannot write ${HOME}/.local/bin; keeping ${entry} (old dir kept too)" >&2
      return 0
    fi
  fi

  # 把旧目录中的版本化二进制搬进新目录；同名跳过（刚装的新版本已在）。
  # 跨文件系统时 mv 会失败（~/.acp 与 ~/.zacp 同在 home 下，理论上不会），
  # 失败则保留旧目录，由用户手动清理。
  for f in "$old"/zacp-*; do
    [ -e "$f" ] || continue
    name="$(basename "$f")"
    if [ ! -e "$new/$name" ]; then
      if mv -f "$f" "$new/$name" 2>/dev/null; then
        echo "==> Moved: ${f} -> ${new}/${name}"
      else
        echo "warning: cannot move ${f}; keeping ${old} for manual cleanup" >&2
        return 0
      fi
    else
      rm -f "$f"
      echo "==> Skipped (already present): ${f}"
    fi
  done

  # 迁移后在新目录做一次 prune（与主流程第 7 步相同策略）：
  # 保留当前版本 + 最新一个旧版本，其余删除。
  newest_prev=""
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    [ "$(basename "$f")" = "$cur_target" ] && continue
    if [ -z "$newest_prev" ]; then
      newest_prev="$f"
      echo "==> Keeping previous version: ${f}"
      continue
    fi
    rm -f "$f"
    echo "==> Removed old version: ${f}"
  done < <(find "$new" -maxdepth 1 -type f -name 'zacp-*' | sort_ver -r)

  # 删除旧目录：仅当其中只剩 zacp 相关文件时才整目录删（防误删用户数据）；
  # 否则只删 zacp 文件并保留目录提示。删除失败不阻塞更新。
  leftovers="$(find "$old" -mindepth 1 -maxdepth 1 ! -name 'zacp' ! -name 'zacp-*' -print 2>/dev/null | head -1)"
  if [ -z "$leftovers" ]; then
    if rm -rf "$old" 2>/dev/null; then
      echo "==> Removed old binary dir: ${old}"
    else
      echo "warning: could not remove ${old}; please remove it manually" >&2
    fi
  else
    rm -f "$old"/zacp "$old"/zacp-* 2>/dev/null || true
    echo "warning: ${old} contains non-zacp files; removed zacp files only, please clean the rest manually" >&2
  fi
  return 0
}

main() {
  local os="" arch="" entry="" entry_dir="" bin_dir="" old_bin_dir="" real=""
  local local_ver="" remote_tag="" remote_ver="" url="" tag="" file=""
  local pkgfile="" bin="" ver="" target="" newest_prev="" latest_out="" f=""

  # --- Argument parsing ---
  while [ $# -gt 0 ]; do
    case "$1" in
      -f|--force) FORCE=1; shift ;;
      -v|--version) VERSION="${2:?--version requires a value}"; shift 2 ;;
      -d|--dir) INSTALL_DIR="${2:?--dir requires a value}"; shift 2 ;;
      --os) FORCE_OS="${2:?--os requires a value}"; shift 2 ;;
      --arch) FORCE_ARCH="${2:?--arch requires a value}"; shift 2 ;;
      --repo) REPO="$2"; shift 2 ;;
      --base-url) BASE_URL="$2"; shift 2 ;;
      --api-base) API_BASE="$2"; shift 2 ;;
      -h|--help) usage; exit 0 ;;
      *) echo "error: unknown argument $1 (use --help for usage)" >&2; exit 2 ;;
    esac
  done

  # --- Platform detection ---
  os="${FORCE_OS:-$(detect_os)}"
  arch="${FORCE_ARCH:-$(detect_arch)}"

  # --- 1. Require an existing installation ---
  entry="$(find_entry)" || {
    echo "error: zacp is not installed" >&2
    echo "Install it first:" >&2
    echo "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash" >&2
    exit 1
  }
  echo "==> Found zacp: ${entry}"

  # --- 2. Entry type: symlink (versioned layout) or plain binary (legacy install) ---
  # 同时记录旧 bin_dir：若命中历史默认目录 ~/.acp/bin，主流程末尾会把它迁移
  # 到 ~/.zacp/bin 并删除（详见 migrate_old_bin_dir 的注释）。
  entry_dir="$(dirname "$entry")"
  if [ -L "$entry" ]; then
    real="$(resolve_link "$entry")"
    old_bin_dir="$(dirname "$real")"
    if [ -n "${ZACP_BIN_DIR:-}" ]; then
      # 用户显式指定了二进制目录：尊重，绝不迁移
      bin_dir="$ZACP_BIN_DIR"
    elif [ "$old_bin_dir" = "${HOME}/.acp/bin" ]; then
      # 历史默认目录：新版本直接装进新默认目录，旧目录随后迁移
      bin_dir="${HOME}/.zacp/bin"
    else
      bin_dir="$old_bin_dir"
    fi
    echo "==> Entry: symlink -> ${real}"
  else
    old_bin_dir="${HOME}/.acp/bin"
    bin_dir="${ZACP_BIN_DIR:-${HOME}/.zacp/bin}"
    echo "==> Entry: plain binary (legacy install); will migrate to the versioned layout (~/.zacp/bin/zacp-<version> + symlink)"
  fi

  # --- 3. Version check (skipped when a specific version is given or --force) ---
  remote_tag=""
  url=""
  if [ -z "$VERSION" ] && [ "$FORCE" != 1 ]; then
    local_ver="$(local_version "$entry")"
    if [ -n "$local_ver" ]; then
      echo "==> Local version: ${local_ver}"
      # Abort on failure (decided): a version check that cannot reach the API
      # means the download would likely fail too — fail fast and let the user retry.
      latest_out="$(resolve_latest "$os" "$arch")" || exit 1
      remote_tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
      url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
      remote_ver="${remote_tag#v}"
      echo "==> Remote version: ${remote_ver}"
      if [ "$local_ver" = "$remote_ver" ]; then
        echo "==> Already up to date (v${local_ver}); nothing to do (use --force to reinstall)"
        exit 0
      fi
      # Guard: never downgrade unless explicitly asked (-v / --force)
      if [ "$(printf '%s\n%s\n' "$local_ver" "$remote_ver" | sort_ver | tail -1)" != "$remote_ver" ]; then
        echo "==> Local version (${local_ver}) is newer than the remote (${remote_ver}); nothing to do (use -v / --force to downgrade)"
        exit 0
      fi
      echo "==> Updating ${local_ver} -> ${remote_ver}..."
    else
      echo "==> Could not determine the local version; updating anyway"
      latest_out="$(resolve_latest "$os" "$arch")" || exit 1
      remote_tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
      url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
    fi
  fi

  # --- Determine tag and download URL (latest, or a pinned version) ---
  if [ -z "$remote_tag" ]; then
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
      # --force without a version: resolve the latest tag + URL in one request
      latest_out="$(resolve_latest "$os" "$arch")"
      remote_tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
      url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
    else
      remote_tag="$(norm_tag "$VERSION")"
      file="zacp-${remote_tag}-${os}-${arch}.$(pkg_ext "$os")"
      url="${BASE_URL}/${REPO}/releases/download/${remote_tag}/${file}"
      echo "==> Version: ${remote_tag}"
    fi
  fi

  # --- 4. Download to a temp dir ---
  STAGE_DIR="$(mktemp -d)"
  pkgfile="${STAGE_DIR}/pkg"
  echo "==> Downloading: ${url}"
  fetch "$url" "$pkgfile" || {
    echo "error: download failed (${url})" >&2
    echo "Please check: 1) the version/repo name is correct; 2) a ${os}/${arch} release package exists; 3) GitHub is reachable from your network" >&2
    exit 1
  }
  [ -s "$pkgfile" ] || {
    echo "error: download failed or the file is empty (${url})" >&2
    exit 1
  }
  echo "==> Downloaded: $(wc -c < "$pkgfile") bytes"

  # --- Extract and locate the binary ---
  extract_pkg "$pkgfile" "${STAGE_DIR}/unpacked" "$os"
  # The package has a top-level dir zacp-v<version>-<os>-<arch>/ containing a binary fixed named zacp
  bin="$(find "${STAGE_DIR}/unpacked" -maxdepth 2 -type f -name zacp | head -1)"
  [ -n "$bin" ] || {
    echo "error: no zacp executable found in the release package" >&2
    exit 1
  }

  # --- 5. Write-permission checks (entry dir + binary dir) ---
  if ! mkdir -p "$entry_dir" 2>/dev/null && [ ! -w "$entry_dir" ]; then
    echo "error: cannot write to the install directory ${entry_dir}" >&2
    echo "Try: sudo bash $0 ${VERSION:+-v "$VERSION"} ${INSTALL_DIR:+--dir "$INSTALL_DIR"}" >&2
    exit 1
  fi
  if ! mkdir -p "$bin_dir" 2>/dev/null && [ ! -w "$bin_dir" ]; then
    echo "error: cannot write to the binary directory ${bin_dir}" >&2
    exit 1
  fi

  # --- 6. Place the versioned binary, then switch the entry symlink ---
  # Safety: the new binary is fully installed BEFORE the entry is touched, so a
  # failure above leaves the current installation untouched. `ln -sfn` also
  # replaces a plain-file entry (legacy install migration) atomically.
  ver="${remote_tag#v}"
  target="${bin_dir}/zacp-${ver}"
  install -m 0755 "$bin" "$target"
  echo "==> Installed: ${target}"
  ln -sfn "$target" "$entry"
  echo "==> Linked: ${entry} -> ${target}"

  # --- 7. Prune old versions: keep the current plus the newest previous one ---
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    [ "$f" = "$target" ] && continue
    if [ -z "$newest_prev" ]; then
      newest_prev="$f"
      echo "==> Keeping previous version: ${f}"
      continue
    fi
    rm -f "$f"
    echo "==> Removed old version: ${f}"
  done < <(find "$bin_dir" -maxdepth 1 -type f -name 'zacp-*' | sort_ver -r)

  # --- 8. Verify (through the entry) ---
  "$entry" --version

  # --- 9. Migrate a legacy ~/.acp/bin install (if any) to ~/.zacp/bin ---
  # 旧 install.sh 的默认二进制目录是 ~/.acp/bin；把它迁移到 ~/.zacp/bin 并删除
  # 旧目录，让后续更新落在正确位置。任何失败都不阻断本次更新（旧目录保留）。
  # legacy 场景入口被搬走时，同步更新 entry/entry_dir 供下方的 PATH 提示使用。
  MIGRATED_ENTRY=""
  migrate_old_bin_dir "$old_bin_dir" "$entry" "$bin_dir"
  if [ -n "$MIGRATED_ENTRY" ]; then
    entry="$MIGRATED_ENTRY"
    entry_dir="$(dirname "$entry")"
  fi

  # --- 10. PATH hint (common for user-local installs) ---
  case ":$PATH:" in
    *":${entry_dir}:"*) ;;
    *)
      echo ""
      echo "hint: ${entry_dir} is not on your PATH. To make it available, run one of the following:"
      echo "  export PATH=\"${entry_dir}:\$PATH\"    # for the current session"
      if [ "$(uname -s)" = "Darwin" ]; then
        echo '  echo '"'"'export PATH="'${entry_dir}':$PATH"'"'"' >> ~/.zshrc && source ~/.zshrc'
      else
        echo "  echo 'export PATH=\"${entry_dir}:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
      fi
      ;;
  esac

  echo ""
  echo "Update complete. Restart any running zacp service for the new version to take effect."
}

main "$@"
