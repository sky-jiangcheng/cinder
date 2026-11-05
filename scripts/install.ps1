# Cinder Desktop install script for Windows
# Installs both the desktop app (repotory.exe) and the MCP server (repotory-mcp.exe).
# Run in PowerShell: iwr -useb https://raw.githubusercontent.com/sky-jiangcheng/cinder/master/scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

# The install FOLDER keeps the old word on purpose: it holds the frozen
# binaries (repotory.exe / repotory-mcp.exe — ADR-0049 decision 3), and
# renaming it would orphan every existing Windows install for no gain.
$InstallDir = "$env:LOCALAPPDATA\Repotory"
$BinaryName = "repotory.exe"
$McpBinaryName = "repotory-mcp.exe"
$Repo = "sky-jiangcheng/cinder"
$Target = "windows-amd64"

# ReleaseBase is the base URL for release assets. Overridable so CI can point
# it at a local fixture server and exercise the real download/extract/install
# path without a published release (see .github/workflows/install-smoke.yml).
$ReleaseBase = if ($env:REPOTORY_RELEASE_BASE) { $env:REPOTORY_RELEASE_BASE } else { "https://github.com/$Repo/releases/latest/download" }
$DesktopUrl = "$ReleaseBase/repotory-$Target.zip"
$McpUrl = "$ReleaseBase/repotory-mcp-$Target.zip"

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
$SumsFile = "$env:TEMP\repotory-SHA256SUMS"
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
# build/bin/repotory.exe), not a bare .exe. Download the archive and expand it
# rather than pointing at a .exe URL that 404s.
$DesktopZip = "$env:TEMP\repotory-$Target.zip"
try {
    Invoke-WebRequest -Uri $DesktopUrl -OutFile $DesktopZip
    Test-AssetSha256 -Path $DesktopZip -AssetName "repotory-$Target.zip"
    Expand-Archive -Path $DesktopZip -DestinationPath $InstallDir -Force
    if (-not (Test-Path "$InstallDir\$BinaryName")) {
        throw "archive did not contain $BinaryName"
    }
} finally {
    if (Test-Path $DesktopZip) { Remove-Item $DesktopZip -Force }
}

# MCP server: separate release asset, so AI clients can run it without the desktop
# app. Non-fatal: older releases may not have the asset yet.
Write-Host "Downloading Cinder MCP server for Windows..."
$McpZip = "$env:TEMP\repotory-mcp-$Target.zip"
$McpDownloaded = $false
try {
    Invoke-WebRequest -Uri $McpUrl -OutFile $McpZip
    $McpDownloaded = $true
} catch {
    Write-Warning "Could not download repotory-mcp (no asset for $Target, or download failed)."
    Write-Warning "The desktop app is installed and working; install the MCP server manually from $McpUrl"
}

if ($McpDownloaded) {
    # Outside the try on purpose: a checksum mismatch throws and must abort the
    # install, not fall through to the non-fatal warning path above.
    Test-AssetSha256 -Path $McpZip -AssetName "repotory-mcp-$Target.zip"
    try {
        Expand-Archive -Path $McpZip -DestinationPath $InstallDir -Force
        if (Test-Path "$InstallDir\$McpBinaryName") {
            Write-Host "Cinder MCP server installed to $InstallDir\$McpBinaryName"
        } else {
            Write-Warning "archive did not contain $McpBinaryName"
        }
    } catch {
        Write-Warning "Could not install repotory-mcp. The desktop app is installed and working."
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
Write-Host "Run 'repotory' in a new terminal to start!"
Write-Host ""
Write-Host "You can also create a desktop shortcut to: $InstallDir\$BinaryName"
