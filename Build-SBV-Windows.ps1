#requires -Version 5.1
[CmdletBinding()]
param(
    [string]$WorkDir = "$env:USERPROFILE\sbv-windows-build",
    [string]$OutputDir,
    [switch]$SkipInstall
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# Stop any running sbv instance to unlock DLLs
Stop-Process -Name sbv -Force -ErrorAction SilentlyContinue

$scriptDir = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($scriptDir)) {
    $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
}
if ([string]::IsNullOrWhiteSpace($scriptDir)) {
    $scriptDir = (Get-Location).Path
}

if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $scriptDir 'SBV-Windows-Portable'
}
$OutputDir = [System.IO.Path]::GetFullPath($OutputDir)

function Write-Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }
function Require-Command($name, $hint) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        throw "Missing '$name'. $hint"
    }
}

Write-Host "SBV Windows Portable Builder (FTS5 + HEIC/libheif + Media Extractor)" -ForegroundColor Green
Write-Host "This builds lowcarbdev/sbv locally and does not upload your SMS backup anywhere."

if (-not $SkipInstall) {
    Write-Step "Checking build prerequisites with winget"

    $packages = @(
        @{ Id='Git.Git'; Name='Git' },
        @{ Id='GoLang.Go'; Name='Go' },
        @{ Id='OpenJS.NodeJS.LTS'; Name='Node.js LTS' },
        @{ Id='MSYS2.MSYS2'; Name='MSYS2' }
    )

    foreach ($pkg in $packages) {
        Write-Host "Checking $($pkg.Name)..."
        if ($pkg.Name -eq 'Git' -and (Get-Command git -ErrorAction SilentlyContinue)) { continue }
        if ($pkg.Name -eq 'Go' -and (Test-Path 'C:\go\bin\go.exe')) { continue }
        if ($pkg.Name -like '*Node*' -and (Get-Command node -ErrorAction SilentlyContinue)) { continue }
        if ($pkg.Name -eq 'MSYS2' -and (Test-Path 'C:\msys64\usr\bin\bash.exe')) { continue }

        & winget install --id $pkg.Id --exact --accept-package-agreements --accept-source-agreements --silent --disable-interactivity 2>$null
        if ($LASTEXITCODE -ne 0) {
            Write-Host "winget returned $LASTEXITCODE for $($pkg.Name); continuing in case it is already installed." -ForegroundColor Yellow
        }
    }
}

# Refresh common PATH locations for the current process.
$env:Path = @(
    'C:\Program Files\Git\cmd',
    'C:\Program Files\Go\bin',
    'C:\go\bin',
    'C:\Program Files\nodejs',
    'C:\msys64\ucrt64\bin',
    $env:Path
) -join ';'

Require-Command git "Install Git for Windows."
Require-Command go "Install Go 1.25.7 or newer."
Require-Command node "Install Node.js 22.22 or newer."
Require-Command npm "npm is included with Node.js."

$goVersionText = (& go version)
Write-Host $goVersionText
$nodeVersionText = (& node --version)
Write-Host "Node $nodeVersionText"

$msysBash = 'C:\msys64\usr\bin\bash.exe'
if (-not (Test-Path $msysBash)) { throw "MSYS2 was not found at C:\msys64. Install MSYS2 or adjust the script." }

Write-Step "Installing native UCRT64 dependencies (GCC, pkg-config, libheif)"
& $msysBash -lc "pacman -Syu --noconfirm"
# A core update can request shell restart. A second pass is intentional.
& $msysBash -lc "pacman -Syu --noconfirm"
& $msysBash -lc "pacman -S --needed --noconfirm mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-libheif"
if ($LASTEXITCODE -ne 0) { throw "Failed to install MSYS2 native dependencies." }

Write-Step "Cloning/checking SBV source"
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
$repo = Join-Path $WorkDir 'sbv'
if (-not (Test-Path (Join-Path $repo '.git'))) {
    if (Test-Path $repo) { Remove-Item -Recurse -Force $repo }
    & git clone --depth 1 https://github.com/lowcarbdev/sbv.git $repo
    if ($LASTEXITCODE -ne 0) { throw "Could not clone SBV." }
}

Write-Step "Building React frontend"
Push-Location (Join-Path $repo 'frontend')
try {
    & npm ci
    if ($LASTEXITCODE -ne 0) { throw "npm ci failed." }
    & npm run build
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed." }
} finally { Pop-Location }

Write-Step "Preparing Windows-local build"
# Keep SBV local-only instead of exposing port 8085 on every network interface.
$mainGo = Join-Path $repo 'main.go'
$mainText = Get-Content $mainGo -Raw
$mainText = $mainText.Replace('e.Start(":" + port)', 'e.Start("127.0.0.1:" + port)')
$mainText = $mainText.Replace('e.Start(":"+port)', 'e.Start("127.0.0.1:"+port)')
Set-Content -Path $mainGo -Value $mainText -Encoding UTF8

# CGO uses MSYS2 UCRT64 GCC; libheif-go locates libheif through pkg-config.
$env:CGO_ENABLED = '1'
$env:CC = 'C:\msys64\ucrt64\bin\gcc.exe'
$env:CXX = 'C:\msys64\ucrt64\bin\g++.exe'
$env:PKG_CONFIG = 'C:\msys64\ucrt64\bin\pkg-config.exe'
$env:PKG_CONFIG_PATH = 'C:\msys64\ucrt64\lib\pkgconfig;C:\msys64\ucrt64\share\pkgconfig'
$env:PATH = 'C:\msys64\ucrt64\bin;' + $env:PATH

Write-Step "Building sbv.exe with FTS5 + HEIC"
Push-Location $repo
try {
    & go mod download
    if ($LASTEXITCODE -ne 0) { throw "go mod download failed." }
    & go build -trimpath -tags 'fts5 heic' -ldflags '-s -w' -o sbv.exe .
    if ($LASTEXITCODE -ne 0) { throw "Go build failed." }
} finally { Pop-Location }

Write-Step "Assembling portable folder"
if (Test-Path $OutputDir) { Remove-Item -Recurse -Force $OutputDir }
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir 'frontend') | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir 'data') | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir 'media') | Out-Null

Copy-Item (Join-Path $repo 'sbv.exe') $OutputDir
Copy-Item -Recurse (Join-Path $repo 'frontend\dist') (Join-Path $OutputDir 'frontend\dist')
Copy-Item (Join-Path $repo 'LICENSE') (Join-Path $OutputDir 'SBV-LICENSE.txt')

# Copy the direct + transitive UCRT64 DLL dependencies reported by ldd.
# We perform recursive discovery because libheif pulls codec libraries such as libde265.
$binDir = 'C:\msys64\ucrt64\bin'
$queue = New-Object System.Collections.Generic.Queue[string]
$seen = New-Object 'System.Collections.Generic.HashSet[string]' ([System.StringComparer]::OrdinalIgnoreCase)
$queue.Enqueue((Join-Path $OutputDir 'sbv.exe'))

function Get-LddDlls([string]$file) {
    $abs = [System.IO.Path]::GetFullPath($file)
    $drive = $abs.Substring(0,1).ToLower()
    $unixPath = '/' + $drive + '/' + ($abs.Substring(3) -replace '\\','/')
    $out = & $msysBash -lc "PATH=/ucrt64/bin:`$PATH ldd '$unixPath' 2>/dev/null" | Out-String
    $paths = @()
    foreach ($line in ($out -split "`r?`n")) {
        if ($line -match '=>\s+(/ucrt64/bin/[^\s]+\.dll)') { $paths += $Matches[1] }
        elseif ($line -match '^\s*(/ucrt64/bin/[^\s]+\.dll)') { $paths += $Matches[1] }
    }
    return $paths | Sort-Object -Unique
}

while ($queue.Count -gt 0) {
    $current = $queue.Dequeue()
    foreach ($unixDll in (Get-LddDlls $current)) {
        $name = Split-Path $unixDll -Leaf
        if ($seen.Add($name)) {
            $src = Join-Path $binDir $name
            if (Test-Path $src) {
                Copy-Item $src $OutputDir -Force
                $queue.Enqueue((Join-Path $OutputDir $name))
            }
        }
    }
}

# libheif may use codec plugins loaded at runtime, which do not appear in ldd(sbv.exe).
# Copy matching plugin DLLs plus their transitive dependencies when present.
$pluginDirs = @(
    'C:\msys64\ucrt64\lib\libheif',
    'C:\msys64\ucrt64\lib\libheif\plugins',
    'C:\msys64\ucrt64\lib\libheif-plugins'
)
$portablePluginDir = Join-Path $OutputDir 'libheif-plugins'
New-Item -ItemType Directory -Force -Path $portablePluginDir | Out-Null
foreach ($d in $pluginDirs) {
    if (Test-Path $d) {
        Get-ChildItem $d -Filter '*heif*.dll' -File -ErrorAction SilentlyContinue | ForEach-Object {
            Copy-Item $_.FullName $portablePluginDir -Force
        }
    }
}

# Copy known HEIF/HEVC codec DLLs as a safety net. ldd already handles dependencies if linked directly.
@('libde265*.dll','libx265*.dll','libaom*.dll','libdav1d*.dll') | ForEach-Object {
    Get-ChildItem $binDir -Filter $_ -File -ErrorAction SilentlyContinue | ForEach-Object { Copy-Item $_.FullName $OutputDir -Force }
}

$launcher = @'
@echo off
setlocal
cd /d "%~dp0"
set "DB_PATH_PREFIX=%~dp0data"
set "PORT=8085"
set "LIBHEIF_PLUGIN_PATH=%~dp0libheif-plugins;%~dp0"
if not exist "%~dp0data" mkdir "%~dp0data"
if not exist "%~dp0media" mkdir "%~dp0media"
start "" http://127.0.0.1:8085
"%~dp0sbv.exe"
endlocal
'@
Set-Content -Path (Join-Path $OutputDir 'Start SBV.cmd') -Value $launcher -Encoding ASCII

$readme = @'
SBV Windows Portable (Passwordless + Media Extractor Edition)
=============================================================

Run: Start SBV.cmd
Then use http://127.0.0.1:8085 in your browser.

What is included
----------------
- sbv.exe: native Windows x64 SBV server (passwordless local-only mode)
- frontend/dist: SBV React user interface with "Extract Media" feature
- data/: local database/import storage
- media/: default folder for extracted image, video, and audio files
- libheif and required UCRT64 DLLs needed for HEIC/HEIF support
- libheif-plugins/: codec plugins when supplied by the installed MSYS2 package

Media Extraction Feature
------------------------
Click the "Extract Media" button in the top navigation bar to parse your SMS/MMS XML
backup and extract individual photos, videos, and audio notes into separate folders:
  - media/image/  (JPEG, PNG, HEIC, GIF, WebP)
  - media/video/  (MP4, 3GP, MOV)
  - media/audio/  (AMR, MP3, M4A, AAC)
  - media/other/  (vCards, attachments)

Files are automatically dated according to the message timestamp and can be opened
directly in Windows Explorer with the "Open Media Folder" button.

Privacy & Access
----------------
The launcher binds to 127.0.0.1, reachable only from this PC.
No username or password is required—opening the application takes you directly to your conversations.

Large 4 GB backup note
----------------------
Keep the XML on a local NTFS drive with substantial free space. Import or media extraction
streams the file with low memory usage. Do not delete your original SMS Backup & Restore XML.
'@
Set-Content -Path (Join-Path $OutputDir 'README-WINDOWS.txt') -Value $readme -Encoding UTF8

# Write provenance/version information.
$commit = (& git -C $repo rev-parse HEAD).Trim()
$versions = @"
SBV commit: $commit
Go: $(& go version)
Node: $(& node --version)
Built: $(Get-Date -Format o)
Build tags: fts5 heic
Passwordless mode: true
Media extractor: true
libheif package:
$(& $msysBash -lc "pacman -Q mingw-w64-ucrt-x86_64-libheif 2>/dev/null")
"@
Set-Content -Path (Join-Path $OutputDir 'BUILD-INFO.txt') -Value $versions -Encoding UTF8

Write-Step "Creating ZIP"
$zip = "$OutputDir.zip"
if (Test-Path $zip) { Remove-Item $zip -Force }
Start-Sleep -Seconds 1
if (Get-Command tar.exe -ErrorAction SilentlyContinue) {
    & tar.exe -a -cf $zip -C $OutputDir *
} else {
    Compress-Archive -Path "$OutputDir\*" -DestinationPath $zip -CompressionLevel Optimal
}

Write-Host "`nSUCCESS" -ForegroundColor Green
Write-Host "Portable folder: $OutputDir"
Write-Host "ZIP: $zip"
Write-Host "Run 'Start SBV.cmd' from the portable folder."
