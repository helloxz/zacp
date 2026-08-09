#!/usr/bin/env bash
# zacp one-click installer: auto-detects macOS / Linux and CPU architecture,
# downloads the matching release package from GitHub Releases and installs it
# into a user directory.
#
# Design notes:
#   - Platform auto-detection: uname detects the OS (Darwin->darwin / Linux->linux)
#     and the architecture (amd64/arm64); --os/--arch can override the detection
#     (e.g. run the amd64 build on Apple Silicon via Rosetta).
#   - Version policy: the latest release is installed by default (the tag and
#     asset URL are resolved via the GitHub API); -v pins a specific version
#     (tags use a v prefix; pass 0.1.0 or v0.1.0, both are normalized).
#   - Package format by platform: Linux downloads .tar.gz (tar is a base tool
#     on every distro; unzip is often NOT installed), macOS/Windows stay .zip.
#   - Upgrade-friendly layout (supports a future `zacp update`):
#       <cmd-dir>/zacp                  -> command entry: symlink to the current version
#       ~/.zacp/bin/zacp-<version>       -> versioned binary (e.g. ~/.zacp/bin/zacp-0.1.0)
#     The symlink points to the newest version; the previous version is kept
#     for rollback and anything older is pruned. The binary root can be
#     overridden with ZACP_BIN_DIR (handy for CI / testing).
#     Note: binaries and runtime state both live under $ZACP_DATA (default
#     ~/.zacp): bin/ holds the binaries, config.toml and data/ the runtime state.
#   - No config generated: $ZACP_DATA/config.toml is created by zacp itself on
#     first start (backend/internal/config/init.go); the installer only places
#     the binary and leaves configuration alone.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/helloxz/zacp/main/install.sh | bash
#   bash install.sh                            # install latest
#   bash install.sh -v 0.1.0                   # install a specific version (v prefix optional)
#   bash install.sh --dir /usr/local/bin       # custom command dir (usually needs sudo)
#   bash install.sh --arch amd64               # override arch detection (e.g. Rosetta)
#   bash install.sh --base-url https://ghproxy.net/https://github.com  # use a mirror
#
# Environment variables (equivalent to the flags, handy for CI / pipelines):
#   ZACP_REPO, ZACP_BASE_URL, ZACP_API_BASE, ZACP_BIN_DIR
set -euo pipefail

# --- Configurable defaults ---
REPO="${ZACP_REPO:-helloxz/zacp}"                              # GitHub owner/name
BASE_URL="${ZACP_BASE_URL:-https://github.com}"                # asset download domain (override for mirrors)
API_BASE="${ZACP_API_BASE:-https://api.github.com}"            # GitHub API domain (for resolving latest)

VERSION=""        # empty = latest
INSTALL_DIR=""    # empty = auto-select
FORCE_OS=""
FORCE_ARCH=""
STAGE_DIR=""       # temp dir (global, referenced by the EXIT trap; must survive main's return)

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
zacp installer (macOS / Linux)

Usage:
  bash install.sh [options]

Options:
  -v, --version <ver>   Install a specific version (tags use a v prefix; pass 0.1.0 or v0.1.0); default: latest
  -d, --dir <dir>       Command (symlink) directory (default: ~/.local/bin, /usr/local/bin when root)
      --os <os>         Override OS detection: darwin | linux
      --arch <arch>     Override architecture detection: amd64 | arm64
      --repo <owner/repo>  Override repository (default: helloxz/zacp)
      --base-url <prefix>  Download URL prefix (e.g. mirror https://ghproxy.net/https://github.com)
      --api-base <url>  GitHub API URL (for resolving latest; usually set together with --base-url)
  -h, --help            Show this help

Install layout (upgrade-friendly):
  <dir>/zacp                  symlink to the current version (the command on PATH)
  ~/.zacp/bin/zacp-<version>   versioned binary (e.g. ~/.zacp/bin/zacp-0.1.0; override with ZACP_BIN_DIR)
  The previous version is kept for rollback; older ones are pruned.

Examples:
  bash install.sh
  bash install.sh -v 0.1.0 --dir /usr/local/bin
  bash install.sh --os darwin --arch amd64          # amd64 build on Apple Silicon (Rosetta)
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

main() {
  local os arch tag url file dir bin

  # --- Argument parsing ---
  while [ $# -gt 0 ]; do
    case "$1" in
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
  echo "==> Target platform: ${os}/${arch}"

  # --- Determine tag and download URL ---
  if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
    # One request yields both tag and URL (resolve_latest prints two lines: tag / url)
    local latest_out
    latest_out="$(resolve_latest "$os" "$arch")"
    tag="$(printf '%s\n' "$latest_out" | sed -n '1p')"
    url="$(printf '%s\n' "$latest_out" | sed -n '2p')"
  else
    tag="$(norm_tag "$VERSION")"
    file="zacp-${tag}-${os}-${arch}.$(pkg_ext "$os")"
    url="${BASE_URL}/${REPO}/releases/download/${tag}/${file}"
    echo "==> Version: ${tag}"
  fi

  # --- Download to a temp dir ---
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

  # --- Command dir (where the symlink lives, i.e. the dir on PATH) ---
  local dir="$INSTALL_DIR"
  if [ -z "$dir" ]; then
    if [ "$(id -u)" = 0 ]; then
      dir="/usr/local/bin"
    else
      dir="${HOME}/.local/bin"
    fi
  fi
  if ! mkdir -p "$dir" 2>/dev/null && [ ! -w "$dir" ]; then
    echo "error: cannot write to install directory ${dir}" >&2
    echo "Try: sudo bash $0 --dir ${dir} (or pick a --dir you can write to)" >&2
    exit 1
  fi

  # --- Versioned binary root (default ~/.zacp/bin), overridable for CI/testing ---
  # 二进制与运行时状态同处 $ZACP_DATA（默认 ~/.zacp）：bin/ 放二进制，
  # config.toml 与 data/ 放运行时状态。
  local bin_dir="${ZACP_BIN_DIR:-${HOME}/.zacp/bin}"
  if ! mkdir -p "$bin_dir" 2>/dev/null && [ ! -w "$bin_dir" ]; then
    echo "error: cannot write to binary directory ${bin_dir}" >&2
    exit 1
  fi

  # --- Place the versioned binary; strip the v prefix to match `zacp --version` ---
  local ver target
  ver="${tag#v}"
  target="${bin_dir}/zacp-${ver}"
  install -m 0755 "$bin" "$target"
  echo "==> Installed: ${target}"

  # --- Command entry: symlink -> versioned binary ---
  # `ln -sfn` also replaces a plain-file install left by older installer versions.
  ln -sfn "$target" "${dir}/zacp"
  echo "==> Linked: ${dir}/zacp -> ${target}"

  # --- Prune old versions: keep the current plus the newest previous one ---
  # List versioned binaries newest-first; the first entry that is not the
  # current version is the rollback copy and stays, everything older is removed.
  local newest_prev="" f
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

  # --- Verify (through the symlink) ---
  "${dir}/zacp" --version

  # --- PATH hint (common for user-local installs) ---
  case ":$PATH:" in
    *":${dir}:"*) ;;
    *)
      echo ""
      echo "hint: ${dir} is not on your PATH. To make it available, run one of the following:"
      echo "  export PATH=\"${dir}:\$PATH\"    # for the current session"
      if [ "$(uname -s)" = "Darwin" ]; then
        echo '  echo '"'"'export PATH="'${dir}':$PATH"'"'"' >> ~/.zshrc && source ~/.zshrc'
      else
        echo "  echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
      fi
      ;;
  esac

  echo ""
  echo "Installation complete. On first run zacp creates ~/.zacp/config.toml; edit it as needed (e.g. the command under [[agents]])."
  echo "To upgrade later, re-run this script (the previous version is kept for rollback)."
}

main "$@"
