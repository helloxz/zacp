# zacp Windows self-updater: updates an existing zacp installation to the
# latest release, keeping the upgrade-friendly layout.
#
# Design notes (mirrors update.sh for macOS / Linux, Windows-specific bits):
#   - Requires an existing installation: entry detection order is
#     -Dir -> PATH (Get-Command, external programs only) -> $HOME\.zacp\bin\zacp.exe.
#     When nothing is found it prints an install hint and exits.
#   - Layout detection: install.ps1 uses a plain COPY for the command entry
#     (no symlink - Windows symlinks need admin rights or developer mode), so
#     "versioned layout" is detected by the presence of zacp-<version>.exe
#     next to the entry. A lone zacp.exe (manually copied) is updated in place.
#   - Local version: the newest zacp-<version>.exe filename is preferred (works
#     even if the binary is broken), else `zacp --version` (2nd column, v-stripped).
#   - Version guard: never downgrade unless -Version / -Force is given (mirrors
#     update.sh); "already up to date" skips the download.
#   - Windows-specific: a running zacp.exe cannot be replaced (file lock), so
#     the updater refuses to run while a zacp process is active.
#   - Safety: the versioned binary is fully placed and VERIFIED before the
#     entry is replaced, so a broken download never breaks the current
#     installation (stronger than update.sh, which switches the entry first).
#   - Runtime state (config / db under $ZACP_DATA, default ~/.zacp) is never
#     touched by an update; only binaries under the bin dir are replaced.
#   - Legacy migration: installs whose bin dir is the historical default
#     $HOME\.acp\bin are moved to $HOME\.zacp\bin (versioned files copied,
#     the user PATH entry swapped via the registry) and the old dir removed,
#     so future updates land in the right place. An explicit -Dir is respected
#     as-is and never migrated.
#   - Output is English: PS 5.1 consoles default to GBK and Chinese output
#     is garbled in some environments; Chinese notes live in the README.
#
# Usage (PowerShell; runs in memory, no ExecutionPolicy change needed):
#   irm https://raw.githubusercontent.com/helloxz/zacp/main/update.ps1 | iex
# Or download and run with arguments (needs -ExecutionPolicy Bypass):
#   powershell -ExecutionPolicy Bypass -File update.ps1 [-Version 0.1.0] [-Force]
#
# Environment variables (equivalent to the parameters; the only channel when
# using irm | iex): ZACP_VERSION, ZACP_REPO, ZACP_BASE_URL, ZACP_API_BASE,
# ZACP_DIR, ZACP_FORCE
param(
  [string]$Version = $env:ZACP_VERSION,
  [switch]$Force,
  [string]$Dir = $env:ZACP_DIR,
  [string]$Repo = $env:ZACP_REPO,
  [string]$BaseUrl = $env:ZACP_BASE_URL,
  [string]$ApiBase = $env:ZACP_API_BASE,
  [switch]$Help
)

$ErrorActionPreference = 'Stop'
# IWR progress bars are extremely slow under PS 5.1; keep downloads silent
# (matches update.sh's curl -fsSL behavior)
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

# --- Defaults (aligned with install.ps1 / update.sh defaults & env conventions) ---
if (-not $Repo) { $Repo = 'helloxz/zacp' }
if (-not $BaseUrl) { $BaseUrl = 'https://github.com' }
if (-not $ApiBase) { $ApiBase = 'https://api.github.com' }
$force = $Force -or ($env:ZACP_FORCE -eq '1')

if ($Help) {
  Write-Host @'
zacp updater (Windows)

Usage:
  irm https://raw.githubusercontent.com/helloxz/zacp/main/update.ps1 | iex
  powershell -ExecutionPolicy Bypass -File update.ps1 [options]

Options:
  -Version <ver>      Update to a specific version (v prefix optional); default: latest
  -Force              Update even when the local version is already the latest
  -Dir <dir>          Install directory (contains zacp.exe and zacp-<ver>.exe);
                      default: detected from the existing install
  -Repo <owner/repo>  Override repository (default: helloxz/zacp)
  -BaseUrl <url>      Download URL prefix (e.g. a mirror)
  -ApiBase <url>      GitHub API URL (for resolving latest)

Environment variables: ZACP_VERSION, ZACP_REPO, ZACP_BASE_URL,
  ZACP_API_BASE, ZACP_DIR, ZACP_FORCE

Install layout (managed by install.ps1; update.ps1 keeps it):
  <dir>\zacp.exe            command entry (copy of the newest version)
  <dir>\zacp-<version>.exe  versioned binary
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

# --- Version helpers ---
function ConvertTo-VersionNumber {
  # Strip any -rc/-beta suffix so prerelease versions compare as their base
  # version (same convention as the prune sort key in install.ps1)
  param([string]$V)
  $v = $V -replace '-.*$', ''
  if ($v -match '^\d+(\.\d+){1,3}$') { return [version]$v }
  return $null
}

function Test-NewerVersion {
  # Returns $true when A > B; falls back to an ordinal string comparison when
  # either side is not a plain numeric version
  param([string]$A, [string]$B)
  $a = ConvertTo-VersionNumber $A
  $b = ConvertTo-VersionNumber $B
  if ($a -and $b) { return $a -gt $b }
  return ([string]::Compare($A, $B, [StringComparison]::Ordinal) -gt 0)
}

# --- Locate the installed entry: -Dir first, then PATH, then the default ---
function Find-Entry {
  if ($Dir) {
    $p = Join-Path $Dir 'zacp.exe'
    if (Test-Path -LiteralPath $p) { return $p }
    throw "error: no zacp.exe found in -Dir '$Dir'"
  }
  # -CommandType Application: external programs only, never aliases/functions
  $cmd = Get-Command -Name zacp -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($cmd) { return $cmd.Source }
  $def = Join-Path $HOME '.zacp\bin\zacp.exe'
  if (Test-Path -LiteralPath $def) { return $def }
  return $null
}

# --- Newest versioned binary in the bin dir ($null when the layout has none).
#     The entry zacp.exe is always a copy of the newest versioned file, so this
#     is the current local version - and it works even if the binary is broken
#     (mirrors update.sh preferring the symlink target dir name) ---
function Find-NewestVersioned {
  param([string]$BinDir)
  $files = @(Get-ChildItem -Path $BinDir -Filter 'zacp-*.exe' -File -ErrorAction SilentlyContinue)
  # Only real zacp-<x.y.z>.exe names count; anything else (e.g. zacp-tool.exe) is ignored
  $versioned = @($files) | Where-Object {
    (($_.Name -replace '^zacp-', '') -replace '\.exe$', '') -match '^\d+(\.\d+){1,3}'
  }
  if (-not $versioned) { return $null }
  return $versioned | Sort-Object -Property {
    ConvertTo-VersionNumber (($_.Name -replace '^zacp-', '') -replace '\.exe$', '')
  } -Descending | Select-Object -First 1
}

# --- Local version: newest versioned filename first, else `zacp --version`
#     (2nd column like update.sh's awk '{print $2}', v prefix stripped) ---
function Get-LocalVersion {
  param([string]$Entry)
  $binDir = Split-Path -Parent $Entry
  $newest = Find-NewestVersioned -BinDir $binDir
  if ($newest) {
    return (($newest.Name -replace '^zacp-', '') -replace '\.exe$', '')
  }
  $out = @(& $Entry --version 2>$null)
  if (-not $out) { return $null }
  $line = [string]($out | Select-Object -First 1)
  $parts = $line -split '\s+'
  if ($parts.Count -lt 2) { return $null }
  return ([string]$parts[1]) -replace '^v', ''
}

# --- Main flow ---
function Update-Zacp {
  param([string]$Arch)

  # --- 1. Require an existing installation ---
  $entry = Find-Entry
  if (-not $entry) {
    throw "zacp is not installed. Install it first:`n  irm https://raw.githubusercontent.com/$Repo/main/install.ps1 | iex"
  }
  Write-Host "==> Found zacp: $entry"
  $binDir = Split-Path -Parent $entry

  # --- Legacy migration detection: the historical default bin dir was
  #     ~/.acp\bin (install.ps1 now uses ~/.zacp\bin). When the found install
  #     lives there AND no explicit -Dir was given, the new version is
  #     installed into ~/.zacp\bin and the old dir is migrated afterwards.
  #     An explicit -Dir is respected as-is and never migrated. ---
  $oldBinDir = $null
  $legacyDefault = Join-Path $HOME '.acp\bin'
  if (-not $Dir -and $binDir.TrimEnd('\') -ieq $legacyDefault.TrimEnd('\') -and (Test-Path -LiteralPath $legacyDefault)) {
    $oldBinDir = $binDir
    $binDir = Join-Path $HOME '.zacp\bin'
    Write-Host "==> Legacy binary dir detected: $oldBinDir; migrating to $binDir"
  }

  # --- Windows-specific: a running zacp.exe cannot be replaced (file lock).
  #     Refuse to run while the service is active; do NOT auto-kill it (that
  #     could lose runtime state and needs admin rights anyway) ---
  $proc = Get-Process -Name zacp -ErrorAction SilentlyContinue
  if ($proc) {
    $ids = @($proc.Id) -join ', '
    throw "error: zacp is currently running (PID $ids). Stop it first, then re-run the updater - Windows cannot replace an in-use executable"
  }

  # --- 2. Version check (skipped when a specific version is given or --force) ---
  $tag = ''
  $url = ''
  if (-not $Version -and -not $force) {
    $localVer = Get-LocalVersion -Entry $entry
    if ($localVer) {
      Write-Host "==> Local version: $localVer"
      $info = Get-ReleaseInfo -Arch $Arch
      $tag = $info.Tag
      $url = $info.Url
      $remoteVer = $tag -replace '^v', ''
      Write-Host "==> Remote version: $remoteVer"
      if ($localVer -eq $remoteVer) {
        Write-Host "==> Already up to date (v$localVer); nothing to do (use -Force to reinstall)"
        return
      }
      # Guard: never downgrade unless explicitly asked (-Version / -Force)
      if (Test-NewerVersion -A $localVer -B $remoteVer) {
        Write-Host "==> Local version ($localVer) is newer than the remote ($remoteVer); nothing to do (use -Version / -Force to downgrade)"
        return
      }
      Write-Host "==> Updating $localVer -> $remoteVer..."
    } else {
      Write-Host '==> Could not determine the local version; updating anyway'
      $info = Get-ReleaseInfo -Arch $Arch
      $tag = $info.Tag
      $url = $info.Url
    }
  }

  # --- Determine tag and download URL (latest, or a pinned version) ---
  if (-not $tag) {
    if (-not $Version -or $Version -eq 'latest') {
      # --force without a version: resolve the latest tag + URL in one request
      $info = Get-ReleaseInfo -Arch $Arch
      $tag = $info.Tag
      $url = $info.Url
    } else {
      $tag = Get-Tag $Version
      $file = "zacp-$tag-windows-$Arch.zip"
      $url = "$BaseUrl/$Repo/releases/download/$tag/$file"
      Write-Host "==> Version: $tag"
    }
  }

  # --- 3. Download to a temp dir (try/finally guarantees cleanup) ---
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('zacp-update-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
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

    # --- 4. Place the versioned binary, verify it, then replace the entry ---
    # Safety: the new binary is fully installed and verified BEFORE the entry
    # is touched, so any failure above leaves the current installation intact
    # (stronger than update.sh, which switches the entry symlink first).
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $ver = $tag -replace '^v', ''
    $target = Join-Path $binDir "zacp-$ver.exe"
    try {
      Copy-Item -Path $bin.FullName -Destination $target -Force
    } catch {
      throw "error: cannot write to $binDir (is it read-only? try running PowerShell as Administrator): $_"
    }
    Write-Host "==> Installed: $target"
    Write-Host '==> Verifying the new binary...'
    $null = & $target --version
    if ($LASTEXITCODE -ne 0) { throw "'zacp --version' on the new binary failed with exit code $LASTEXITCODE" }

    $entryCopy = Join-Path $binDir 'zacp.exe'
    try {
      Copy-Item -Path $bin.FullName -Destination $entryCopy -Force
    } catch {
      throw "error: cannot replace the command entry $entryCopy (is it read-only or still running? try running PowerShell as Administrator): $_"
    }
    Write-Host "==> Replaced: $entryCopy"

    # --- 5. Verify (through the command entry) BEFORE pruning: if this check
    #         fails, the previous versioned binary is still in place so the old
    #         installation remains intact and rollback is trivial ---
    Write-Host '==> Verifying...'
    & $entryCopy --version
    if ($LASTEXITCODE -ne 0) { throw "'zacp --version' failed with exit code $LASTEXITCODE" }

    # --- 6. Prune old versions: keep the current plus the newest previous one
    #         (mirrors install.ps1). The sort key strips any -rc/-beta suffix
    #         so prerelease filenames still sort as their base version. ---
    $oldVersions = Get-ChildItem -Path $binDir -Filter 'zacp-*.exe' -File |
      Where-Object { $_.FullName -ne $target } |
      Sort-Object -Property {
        # Regex-precheck before [version] conversion: the dir may contain
        # non x.y.z-named files and a bare cast would throw; unparseable names sort last
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

    # --- 7. Migrate a legacy ~/.acp\bin install (if any) to ~/.zacp\bin ---
    # 旧 install.ps1 的默认二进制目录是 ~/.acp\bin；迁移到 ~/.zacp\bin：
    # 版本化文件复制过来（同名跳过，刚装的新版本已在）、用户 PATH 条目替换、
    # 然后删除旧目录（仅当其中只剩 zacp 文件时整目录删，防误删用户数据）。
    # 任何失败只警告不阻断：新安装已完整就位且已验证。复制有失败时保留旧目录。
    if ($oldBinDir) {
      $migrateOk = $true
      $oldFiles = @(Get-ChildItem -LiteralPath $oldBinDir -Filter 'zacp-*.exe' -File -ErrorAction SilentlyContinue)
      foreach ($f in $oldFiles) {
        $dest = Join-Path $binDir $f.Name
        if (Test-Path -LiteralPath $dest) {
          Write-Host "==> Skipped (already present): $($f.FullName)"
        } else {
          try {
            Copy-Item -LiteralPath $f.FullName -Destination $dest -Force
            Write-Host "==> Moved: $($f.FullName) -> $dest"
          } catch {
            Write-Host "warning: cannot copy $($f.FullName) to $dest; keeping $oldBinDir for manual cleanup" -ForegroundColor Yellow
            $migrateOk = $false
          }
        }
      }
      # PATH 替换始终执行（新目录已有完整安装）；删除仅在复制全部成功时执行
      try { Update-UserPathEntry -OldEntry $oldBinDir -NewEntry $binDir } catch { Write-Host "warning: PATH update failed: $_" -ForegroundColor Yellow }
      if ($migrateOk) {
        $nonZacp = @(Get-ChildItem -LiteralPath $oldBinDir -Force -ErrorAction SilentlyContinue | Where-Object { $_.Name -notlike 'zacp*' })
        if (-not $nonZacp) {
          try {
            Remove-Item -LiteralPath $oldBinDir -Recurse -Force
            Write-Host "==> Removed old binary dir: $oldBinDir"
          } catch {
            Write-Host "warning: could not remove $oldBinDir; please remove it manually" -ForegroundColor Yellow
          }
        } else {
          Get-ChildItem -LiteralPath $oldBinDir -Filter 'zacp*' -Force -ErrorAction SilentlyContinue | Remove-Item -Force
          Write-Host "warning: $oldBinDir contains non-zacp files; removed zacp files only, please clean the rest manually" -ForegroundColor Yellow
        }
      }
    }

    # --- PATH hint (only when the bin dir is missing from the current PATH) ---
    $norm = $binDir.TrimEnd('\')
    $onPath = @($env:Path -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ieq $norm }
    if (-not $onPath) {
      Write-Host ''
      Write-Host "hint: $binDir is not on your PATH. To add it, run:"
      Write-Host "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$binDir', 'User')"
      Write-Host '  (or via: Settings > System > About > Advanced system settings > Environment Variables)'
    }

    Write-Host ''
    Write-Host 'Update complete. Start zacp when ready (the previous version is kept for rollback).'
  } finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# --- Swap one entry for another in the USER PATH (HKCU\Environment), reusing
#     install.ps1's proven registry pattern: raw values are read with
#     DoNotExpandEnvironmentNames (keeps %VAR% entries intact) and written back
#     with the original value kind (ExpandString); case-insensitive dedup.
#     Also updates the current process $env:Path and broadcasts WM_SETTINGCHANGE
#     (best-effort). Failures only warn: the update must not fail over PATH
#     cosmetics ---
function Update-UserPathEntry {
  param([string]$OldEntry, [string]$NewEntry)
  $oldNorm = $OldEntry.TrimEnd('\')
  $newNorm = $NewEntry.TrimEnd('\')
  $reg = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
  if (-not $reg) {
    Write-Host "warning: cannot open HKCU\Environment; PATH not updated (add $NewEntry manually)" -ForegroundColor Yellow
    return
  }
  try {
    $userPath = [string]$reg.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    try { $kind = $reg.GetValueKind('Path') } catch { $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString }
    # 去掉旧条目（大小写不敏感），新条目不存在则追加；同时清理空段
    $segments = @($userPath -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ine $oldNorm }
    $hasNew = @($segments) | Where-Object { $_.TrimEnd('\') -ieq $newNorm }
    if (-not $hasNew) { $segments += $newNorm }
    $newPath = $segments -join ';'
    if ($newPath -ne $userPath) {
      $reg.SetValue('Path', $newPath, $kind)
      Write-Host "==> Updated user PATH: $oldNorm -> $newNorm"
      # 当前进程立即生效（irm | iex 共享调用方进程）
      $currentHasNew = @($env:Path -split ';') | Where-Object { $_ -and $_.TrimEnd('\') -ieq $newNorm }
      if (-not $currentHasNew) { $env:Path = $newNorm + ';' + $env:Path }
      # WM_SETTINGCHANGE 广播（best-effort；Add-Type 需本地 C# 编译器，
      # 'Zacp.Native.EnvNotify' 守卫防同进程重复 Add-Type）
      try {
        if (-not ('Zacp.Native.EnvNotify' -as [type])) {
          $envNotifySource = @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
          Add-Type -Namespace Zacp.Native -Name EnvNotify -MemberDefinition $envNotifySource
        }
        # HWND_BROADCAST=0xffff, WM_SETTINGCHANGE=0x1a, SMTO_ABORTIFHUNG=0x0002
        $result = [UIntPtr]::Zero
        $null = [Zacp.Native.EnvNotify]::SendMessageTimeout(
          [IntPtr]0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 0x0002, 5000, [ref]$result)
      } catch {
        Write-Host '==> Note: environment-change notification skipped (harmless)'
      }
    }
  } finally {
    $reg.Dispose()
  }
}

# --- Entry: collect errors into a clear message; exit semantics handled by
#     inline return (irm | iex) / exit (-File), see the note above ---
try {
  $arch = Get-Arch
  Write-Host "==> Target platform: windows/$arch"
  Update-Zacp -Arch $arch
} catch {
  Write-Host "error: $_" -ForegroundColor Red
  if ($script:isInMemory) { return }
  exit 1
}
