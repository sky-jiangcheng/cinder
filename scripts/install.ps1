# Cinder Desktop install script for Windows
# Installs both the desktop app (cinder.exe) and the MCP server (cinder-mcp.exe).
# Run in PowerShell: iwr -useb https://raw.githubusercontent.com/sky-jiangcheng/cinder/master/scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

# The install FOLDER keeps the old word on purpose: it is the Windows twin of
# the frozen data directory (`~/Library/Application Support/repotory` on macOS),
# and moving it would leave every existing install with two copies of the app
# and its database (ADR-0049 补注).
$InstallDir = "$env:LOCALAPPDATA\Repotory"
$BinaryName = "cinder.exe"
$McpBinaryName = "cinder-mcp.exe"
# Pre-rename names stay available as copies: an MCP client already configured
# on this machine points at repotory-mcp.exe by absolute path.
$CompatBinaryName = "repotory.exe"
$CompatMcpName = "repotory-mcp.exe"
$Repo = "sky-jiangcheng/cinder"
$Target = "windows-amd64"

# --- Which release? -----------------------------------------------------------
#
# Asset names embed the version, so `releases/latest/download/<file>` cannot be
# used any more — the tag has to be known before the URL can be built.
# CINDER_RELEASE_BASE (a flat directory of assets) and VERSION are overridable
# so CI can exercise this script against a local fixture server without a
# published release (see .github/workflows/install-smoke.yml).
$Version = $env:VERSION
$ReleaseBase = $env:CINDER_RELEASE_BASE
if (-not $ReleaseBase) {
    if (-not $Version) {
        $Latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" `
            -Headers @{ "User-Agent" = "cinder-install" }
        $Version = $Latest.tag_name.TrimStart("v")
        if (-not $Version) { throw "could not resolve the latest release of $Repo" }
    }
    $ReleaseBase = "https://github.com/$Repo/releases/download/v$Version"
}
if (-not $Version -and $ReleaseBase -match '/v?([0-9]+\.[0-9]+\.[0-9][^/]*)$') {
    $Version = $Matches[1]
}
$DesktopAsset = "Cinder-$Version-$Target.zip"
$McpAsset = "cinder-mcp-$Version-$Target.zip"
$DesktopUrl = "$ReleaseBase/$DesktopAsset"
$McpUrl = "$ReleaseBase/$McpAsset"

Write-Host "Installing Cinder $Version ($Target)"

# --- Checksum verification -----------------------------------------------------
#
# Every release publishes a SHA256SUMS asset listing the digest of each shipped
# file (see .github/workflows/release.yml); scripts/install.sh implements the
# same check for macOS/Linux. Verifying turns a corrupt mirror or a tampered
# download into a loud failure instead of a silently broken binary.
#
# The manifest is optional on purpose: releases published before it existed
# do not have the asset, and a missing manifest must not fail an otherwise
# healthy install. A digest MISMATCH is always fatal.
$SumsFile = "$env:TEMP\cinder-SHA256SUMS"
$SumsLoaded = $false
try {
    Invoke-WebRequest -Uri "$ReleaseBase/SHA256SUMS" -OutFile $SumsFile -ErrorAction Stop
    if ((Get-Item $SumsFile).Length -gt 0) { $SumsLoaded = $true }
} catch {
    Write-Warning "This release has no SHA256SUMS asset, skipping checksum verification."
}

function Test-AssetSha256 {
    param(
        [Parameter(Mandatory=$true)][string]$Path,
        [Parameter(Mandatory=$true)][string]$AssetName
    )
    if (-not $SumsLoaded) { return }
    $line = Get-Content $SumsFile | Where-Object { $_ -match ("\s" + [Regex]::Escape($AssetName) + "$") } | Select-Object -First 1
    if (-not $line) {
        Write-Warning "$AssetName is not listed in SHA256SUMS, skipping its verification."
        return
    }
    $expected = ($line.Trim() -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "SHA256 mismatch for $AssetName — refusing to install.`n  expected: $expected`n  actual:   $actual"
    }
    Write-Host "Verified $AssetName (sha256)."
}

Write-Host "Downloading Cinder Desktop for Windows..."

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# The release workflow publishes Windows as a .zip (Compress-Archive of
# build/bin/cinder.exe), not a bare .exe. Download the archive and expand it
# rather than pointing at an .exe URL that 404s.
$DesktopZip = "$env:TEMP\$DesktopAsset"
try {
    Invoke-WebRequest -Uri $DesktopUrl -OutFile $DesktopZip
    Test-AssetSha256 -Path $DesktopZip -AssetName $DesktopAsset
    Expand-Archive -Path $DesktopZip -DestinationPath $InstallDir -Force
    if (-not (Test-Path "$InstallDir\$BinaryName")) {
        throw "archive did not contain $BinaryName"
    }
    # Old name → new name. A copy rather than a symlink: Windows symlinks need
    # developer mode or elevation, and this has to work for everyone.
    Copy-Item "$InstallDir\$BinaryName" "$InstallDir\$CompatBinaryName" -Force
} finally {
    if (Test-Path $DesktopZip) { Remove-Item $DesktopZip -Force }
}

# MCP server: separate release asset, so AI clients can run it without the desktop
# app. Non-fatal: older releases may not have the asset yet.
Write-Host "Downloading Cinder MCP server for Windows..."
$McpZip = "$env:TEMP\$McpAsset"
$McpDownloaded = $false
try {
    Invoke-WebRequest -Uri $McpUrl -OutFile $McpZip
    $McpDownloaded = $true
} catch {
    Write-Warning "Could not download $McpBinaryName (no asset for $Target, or download failed)."
    Write-Warning "The desktop app is installed and working; install the MCP server manually from $McpUrl"
}

if ($McpDownloaded) {
    # Outside the try on purpose: a checksum mismatch throws and must abort the
    # install, not fall through to the non-fatal warning path above.
    Test-AssetSha256 -Path $McpZip -AssetName $McpAsset
    try {
        Expand-Archive -Path $McpZip -DestinationPath $InstallDir -Force
        if (Test-Path "$InstallDir\$McpBinaryName") {
            Copy-Item "$InstallDir\$McpBinaryName" "$InstallDir\$CompatMcpName" -Force
            Write-Host "Cinder MCP server installed to $InstallDir\$McpBinaryName"
        } else {
            Write-Warning "archive did not contain $McpBinaryName"
        }
    } catch {
        Write-Warning "Could not install $McpBinaryName. The desktop app is installed and working."
    } finally {
        if (Test-Path $McpZip) { Remove-Item $McpZip -Force }
    }
}

# Add to PATH
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to PATH"
}

Write-Host ""
Write-Host "Cinder Desktop installed to $InstallDir"
Write-Host "Run 'cinder' in a new terminal to start!"
Write-Host ""
Write-Host "You can also create a desktop shortcut to: $InstallDir\$BinaryName"
