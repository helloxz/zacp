# zacp Windows one-click installer: auto-detects the CPU architecture,
# downloads the matching release package (zip) from GitHub Releases and
# installs it into a user directory.
#
# Design notes (mirrors install.sh for macOS / Linux, Windows-specific bits):
#   - Architecture detection: PROCESSOR_ARCHITECTURE, with
#     PROCESSOR_ARCHITEW6432 for a 32-bit PowerShell running on 64-bit
#     Windows (pack.sh ships windows/amd64 and windows/arm64).
#   - Version policy: the latest release is installed by default (resolved
#     via the GitHub API); -Version pins a specific version (tags use a v
#     prefix; pass 0.1.0 or v0.1.0, both are normalized).
#   - Package format: windows always downloads the .zip asset
#     (zacp-v<version>-windows-<arch>.zip; top-level dir + zacp.exe inside).
#   - Upgrade-friendly layout (mirrors install.sh):
#       $HOME\.acp\bin\zacp.exe            -> command entry (copy of the newest version)
#       $HOME\.acp\bin\zacp-<version>.exe  -> versioned binary
#     The entry is a plain copy, NOT a symlink: Windows symlinks require
#     admin rights or developer mode, while a copy is idempotent and always
#     works. The previous version is kept for rollback; older ones are pruned.
#     ~/.acp holds binaries only; runtime state lives under $ZACP_DATA
#     (default ~/.zacp) and the two never overlap.
#   - PATH: the bin dir is added to the *user* PATH (HKCU\Environment) via
#     the registry API with ExpandString (preserves %VAR% entries), not setx
#     (setx truncates at 1024 chars and can corrupt the PATH).
#   - TLS 1.2 is forced up front: PowerShell 5.1 on older Windows defaults
#     to TLS 1.0, which GitHub rejects.
#   - Output is English: PS 5.1 consoles default to GBK and Chinese output
#     is garbled in some environments; Chinese notes live in the README.
#
# Usage (PowerShell; runs in memory, no ExecutionPolicy change needed):
#   irm https://raw.githubusercontent.com/helloxz/zacp/main/install.ps1 | iex
# Or download and run with arguments (needs -ExecutionPolicy Bypass):
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Version 0.1.0] [-NoPath]
#
# Environment variables (equivalent to the parameters; the only channel when
# using irm | iex): ZACP_VERSION, ZACP_REPO, ZACP_BASE_URL, ZACP_API_BASE,
# ZACP_BIN_DIR, ZACP_NO_PATH
param(
  [string]$Version = $env:ZACP_VERSION,
  [string]$Repo = $env:ZACP_REPO,
  [string]$BaseUrl = $env:ZACP_BASE_URL,
  [string]$ApiBase = $env:ZACP_API_BASE,
  [string]$BinDir = $env:ZACP_BIN_DIR,
  [switch]$NoPath,
  [switch]$Help
)

$ErrorActionPreference = 'Stop'
# IWR progress bars are extremely slow under PS 5.1; keep downloads silent
# (matches install.sh's curl -fsSL behavior)
$ProgressPreference = 'SilentlyContinue'

# --- Force TLS 1.2 up front: PS 5.1 on older Windows may negotiate TLS 1.0
#      by default, which GitHub API / downloads reject (Windows-only gotcha) ---
try {
  [Net.ServicePointManager]::SecurityProtocol = `
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
  # Very old systems may lack the Tls12 enum value; ignore, connection failures are reported later
}

# --- Execution mode: irm | iex runs the script in memory where 'exit' would
#     close the user's whole PowerShell session, so every exit point is written
#     as 'if (in-memory) { return } else { exit }'; under powershell -File /
#     direct execution a real exit code is returned for CI/callers ---
$script:isInMemory = [string]::IsNullOrEmpty($PSScriptRoot)

# --- Basic env check: irm (Invoke-RestMethod) needs PS 3.0+ and Expand-Archive
#     needs 5.0+; Win10/11 ship PS 5.1. Fail early with a clear message otherwise ---
if ($PSVersionTable.PSVersion.Major -lt 5) {
  Write-Host "error: PowerShell 5.0 or newer is required (current: $($PSVersionTable.PSVersion))" -ForegroundColor Red
  if ($script:isInMemory) { return }
  exit 1
}

# --- Defaults (aligned with install.sh defaults / env var conventions) ---
if (-not $Repo) { $Repo = 'helloxz/zacp' }
if (-not $BaseUrl) { $BaseUrl = 'https://github.com' }
if (-not $ApiBase) { $ApiBase = 'https://api.github.com' }
$skipPath = $NoPath -or ($env:ZACP_NO_PATH -eq '1')

if ($Help) {
  Write-Host @'
zacp installer (Windows)

Usage:
  irm https://raw.githubusercontent.com/helloxz/zacp/main/install.ps1 | iex
  powershell -ExecutionPolicy Bypass -File install.ps1 [options]

Options:
  -Version <ver>      Install a specific version (v prefix optional); default: latest
  -Repo <owner/repo>  Override repository (default: helloxz/zacp)
  -BaseUrl <url>      Download URL prefix (e.g. a mirror)
  -ApiBase <url>      GitHub API URL (for resolving latest)
  -BinDir <dir>       Binary directory (default: $HOME\.acp\bin)
  -NoPath             Do not modify the user PATH

Environment variables: ZACP_VERSION, ZACP_REPO, ZACP_BASE_URL,
  ZACP_API_BASE, ZACP_BIN_DIR, ZACP_NO_PATH

Install layout (upgrade-friendly):
  $HOME\.acp\bin\zacp.exe            command entry (copy of the newest version)
  $HOME\.acp\bin\zacp-<version>.exe  versioned binary
  The previous version is kept for rollback; older ones are pruned.
'@
  if ($script:isInMemory) { return }
  exit 0
}

# --- Architecture detection ---
function Get-Arch {
  # On 32-bit PowerShell running on 64-bit Windows the real arch is in PROCESSOR_ARCHITEW6432
  $arch = $env:PROCESSOR_ARCHITEW6432
  if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
  switch ($arch) {
    'AMD64' { return 'amd64' }
    'ARM64' { return 'arm64' }
    default { throw "error: unsupported CPU architecture '$arch' (zacp release packages are only provided for amd64 / arm64)" }
  }
}

# --- Resolve the latest release, returns @{Tag; Url}; falls back to the
#     releases list when /releases/latest 404s (same semantics as install.sh:
#     stable-only -> including prereleases) ---
function Get-ReleaseInfo {
  param([string]$Arch)
  $headers = @{ 'User-Agent' = 'zacp-installer' }   # GitHub API requires it, missing UA gets 403
  $apiUrl = "$ApiBase/repos/$Repo/releases/latest"
  Write-Host "==> Resolving latest release: $apiUrl"
  $release = $null
  try {
    $release = Invoke-RestMethod -Uri $apiUrl -Headers $headers
  } catch {
    Write-Host '==> No stable release yet, falling back to the newest release (including prereleases)...'
    try {
      $list = Invoke-RestMethod -Uri "$ApiBase/repos/$Repo/releases?per_page=1" -Headers $headers
      $release = $list[0]
    } catch {
      throw 'error: failed to fetch the release list (network issue or API rate limit?)'
    }
  }
  if (-not $release) { throw 'error: empty response from the GitHub API' }
  $tag = [string]$release.tag_name
  if (-not $tag) { throw 'error: no tag_name found in the latest release response' }
  $asset = @($release.assets) | Where-Object { $_.name -like "zacp-v*-windows-$Arch.zip" } | Select-Object -First 1
  if (-not $asset) { throw "error: no windows/$Arch asset found in the latest release (missing package or unexpected API response)" }
  return @{ Tag = $tag; Url = [string]$asset.browser_download_url }
}

# --- Tag normalization: 0.1.0 -> v0.1.0 (project convention: tags must have a v prefix) ---
function Get-Tag {
  param([string]$V)
  if ($V -like 'v*') { return $V }
  return "v$V"
}

# --- Main flow ---
function Install-Zacp {
  param([string]$Arch)

  # --- Determine tag and download URL ---
  $tag = ''
  $url = ''
  if (-not $Version -or $Version -eq 'latest') {
    $info = Get-ReleaseInfo -Arch $Arch
    $tag = $info.Tag
    $url = $info.Url
  } else {
    $tag = Get-Tag $Version
    $file = "zacp-$tag-windows-$Arch.zip"
    $url = "$BaseUrl/$Repo/releases/download/$tag/$file"
    Write-Host "==> Version: $tag"
  }

  # --- Temp dir (try/finally guarantees cleanup) ---
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('zacp-install-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    # --- Download (-UseBasicParsing avoids PS 5.1's IE-engine dependency; non-empty check) ---
    $pkg = Join-Path $tmp 'pkg.zip'
    Write-Host "==> Downloading: $url"
    try {
      Invoke-WebRequest -Uri $url -OutFile $pkg -UseBasicParsing -Headers @{ 'User-Agent' = 'zacp-installer' }
    } catch {
      throw "error: download failed ($url). Please check: 1) the version/repo name is correct; 2) a windows/$Arch release package exists; 3) GitHub is reachable from your network"
    }
    $pkgLen = (Get-Item $pkg).Length
    if ($pkgLen -eq 0) { throw "error: download failed or the file is empty ($url)" }
    Write-Host "==> Downloaded: $pkgLen bytes"

    # --- Extract and locate the binary (zip top-level dir: zacp-v<ver>-windows-<arch>/) ---
    $unpacked = Join-Path $tmp 'unpacked'
    Expand-Archive -Path $pkg -DestinationPath $unpacked
    $bin = Get-ChildItem -Path $unpacked -Recurse -Filter 'zacp.exe' -File | Select-Object -First 1
    if (-not $bin) { throw 'error: no zacp.exe found in the release package' }

    # --- Binary dir (default ~/.acp/bin, mirrors install.sh; ~/.acp holds binaries
    #     only, runtime state lives in $ZACP_DATA = ~/.zacp, the two never overlap) ---
    if (-not $BinDir) { $BinDir = Join-Path $HOME '.acp\bin' }
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null

    # --- Versioned binary (strip the v prefix to match 'zacp --version') ---
    $ver = $tag -replace '^v', ''
    $target = Join-Path $BinDir "zacp-$ver.exe"
    Copy-Item -Path $bin.FullName -Destination $target -Force
    Write-Host "==> Installed: $target"

    # --- Command entry: copy the newest version as zacp.exe (no symlink:
    #     normal users lack permission and developer mode is required) ---
    $entry = Join-Path $BinDir 'zacp.exe'
    Copy-Item -Path $bin.FullName -Destination $entry -Force
    Write-Host "==> Installed: $entry"

    # --- Verify (through the command entry) BEFORE pruning: never delete the
    #     only rollback copy when the new binary turns out to be broken ---
    Write-Host '==> Verifying...'
    & $entry --version
    if ($LASTEXITCODE -ne 0) { throw "'zacp --version' failed with exit code $LASTEXITCODE" }

    # --- Prune old versions: keep the current plus the newest previous one
    #     (mirrors install.sh). The sort key strips any -rc/-beta suffix so
    #     prerelease filenames still sort as their base version. ---
    $oldVersions = Get-ChildItem -Path $BinDir -Filter 'zacp-*.exe' -File |
      Where-Object { $_.FullName -ne $target } |
      Sort-Object -Property {
        # Regex-precheck before [version] conversion: the dir may contain
        # non x.y.z-named files and a bare cast would throw, aborting an
        # already-successful install; unparseable names sort last
        $v = (($_.Name -replace '^zacp-', '') -replace '\.exe$', '') -replace '-.*$', ''
        if ($v -match '^\d+(\.\d+){1,3}$') { [version]$v } else { [version]'0.0.0' }
      } -Descending
    $keptPrev = $false
    foreach ($f in $oldVersions) {
      if (-not $keptPrev) {
        Write-Host "==> Keeping previous version: $($f.FullName)"
        $keptPrev = $true
        continue
      }
      Remove-Item -Path $f.FullName -Force
      Write-Host "==> Removed old version: $($f.FullName)"
    }

    # --- PATH: add the bin dir to the user PATH by default (registry
    #     read-modify-write, not setx; case-insensitive dedup) ---
    $pathAdded = $false
    if (-not $skipPath) {
      # Read/write the raw (unexpanded) user PATH via the registry: .NET's
      # GetEnvironmentVariable expands %VAR% entries and SetEnvironmentVariable
      # rewrites the value as REG_SZ, which would literalize existing
      # %JAVA_HOME%-style entries. DoNotExpandEnvironmentNames + ExpandString
      # keep the original semantics (case-insensitive dedup).
      $norm = $BinDir.TrimEnd('\')
      $reg = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
      if (-not $reg) { throw 'error: cannot open HKCU\Environment for writing' }
      try {
        $userPath = [string]$reg.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        try { $kind = $reg.GetValueKind('Path') } catch { $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $exists = @($userPath -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ieq $norm }
        if ($exists) {
          Write-Host "==> Already on user PATH: $BinDir"
        } else {
          $newPath = if ([string]::IsNullOrEmpty($userPath)) { $norm } else { $userPath.TrimEnd(';') + ';' + $norm }
          $reg.SetValue('Path', $newPath, $kind)
          Write-Host "==> Added to user PATH: $BinDir (new terminals will pick it up)"
          $pathAdded = $true
        }
      } finally {
        $reg.Dispose()
      }
    }

    # --- Completion hints ---
    Write-Host ''
    Write-Host 'Installation complete. On first run zacp creates ~/.zacp/config.toml;'
    Write-Host 'edit it as needed (e.g. the command under [[agents]]).'
    Write-Host 'To upgrade later, re-run this script (the previous version is kept for rollback).'
    if (-not $skipPath) {
      if ($pathAdded) {
        Write-Host 'Start it (in a new terminal) with: zacp'
      } else {
        Write-Host 'Start it with: zacp'
      }
    } else {
      Write-Host "Start it with: & `"$entry`""
    }
    Write-Host 'Then open http://127.0.0.1:8680/ in your browser.'
  } finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# --- Entry: collect errors into a clear message; exit semantics handled by
#     inline return (irm | iex) / exit (-File), see the note above ---
try {
  $arch = Get-Arch
  Write-Host "==> Target platform: windows/$arch"
  Install-Zacp -Arch $arch
} catch {
  Write-Host "error: $_" -ForegroundColor Red
  if ($script:isInMemory) { return }
  exit 1
}
