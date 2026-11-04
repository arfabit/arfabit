# Builds ARFABIT for Windows: core\arfabit.exe, the shell ARFABIT.exe, and
# ARFABIT-setup.exe (docs/RELEASE.md §3, §5). Run on Windows with Go, the
# .NET SDK and Inno Setup 6 or later:
#
#   packaging\windows\build.ps1            a "dev" build
#   packaging\windows\build.ps1 v0.1.0     a release
#   packaging\windows\build.ps1 v0.1.0 -Upload
#                                          a release, with the installer put
#                                          on its draft on GitHub (docs/RELEASE.md §7)
#
# Everything goes in dist\windows.

param([string]$Version = "dev", [switch]$Upload)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path "$PSScriptRoot\..\..").Path
Set-Location $root

if ($Version -eq "dev") {
    $short = "0.0.0"
} elseif ($Version -match '^v(\d+\.\d+\.\d+)$') {
    $short = $Matches[1]
} else {
    throw "The version must look like v0.1.0, or be left out for a dev build."
}

# A release's installer is built from exactly the tagged commit, the one the
# Mac app on the same draft was built from.
if ($Upload) {
    if ($Version -eq "dev") { throw "Only a release can be uploaded. Give its version, such as v0.1.0." }
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
        throw "The GitHub CLI is not installed: winget install GitHub.cli, then gh auth login."
    }
    # for-each-ref says nothing for a missing tag, where cat-file would write
    # to stderr, which Windows PowerShell stops on.
    if ((git for-each-ref --format="%(objecttype)" "refs/tags/$Version") -ne "tag") {
        throw "There is no annotated tag $Version here. Run: git fetch --tags"
    }
    if (git status --porcelain) { throw "There are changes not committed. Commit or put them away first." }
    if ((git rev-parse HEAD) -ne (git rev-parse "$Version^{commit}")) {
        throw "This checkout is not at $Version. Run: git checkout $Version"
    }
}

$dist = Join-Path $root "dist\windows"

# A copy of ARFABIT started from dist\windows holds its files, and Windows
# will not remove a running program. Stop that copy, the app first so it does
# not start ARFABIT again. An installed ARFABIT runs from elsewhere and is left
# alone.
$running = Get-Process -Name arfabit -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and $_.Path.StartsWith($dist + "\", [StringComparison]::OrdinalIgnoreCase) } |
    Sort-Object { $_.Path.Length }
if ($running) {
    Write-Host "Stopping the ARFABIT running from $dist"
    $running | Stop-Process -Force
    $running | Wait-Process -Timeout 10 -ErrorAction SilentlyContinue
}

if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Force (Join-Path $dist "core") | Out-Null

Write-Host "ARFABIT $Version"

# The icons, from the picture exported from Icon Composer.
go run ./packaging/icon -out (Join-Path $dist "icons")
if ($LASTEXITCODE) { throw "The icons were not made." }

# ARFABIT itself, cgo off.
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w -X github.com/arfabit/arfabit/internal/version.Version=$Version" -o (Join-Path $dist "core\arfabit.exe") .\cmd\arfabit
if ($LASTEXITCODE) { throw "go build did not finish." }

# The shell.
dotnet build -c Release "shell\windows\ARFABIT.csproj" "-p:Version=$short" -o (Join-Path $dist "shell")
if ($LASTEXITCODE) { throw "dotnet build did not finish." }
Copy-Item (Join-Path $dist "shell\ARFABIT.exe") $dist

# The installer.
# Inno Setup 6 or later, from PATH or wherever its installer put it.
$iscc = Get-Command iscc -ErrorAction SilentlyContinue
if ($iscc) {
    $iscc = $iscc.Source
} else {
    $iscc = Get-ChildItem -Path $env:ProgramFiles, ${env:ProgramFiles(x86)}, (Join-Path $env:LOCALAPPDATA "Programs") -Filter "Inno Setup *" -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending |
        ForEach-Object { Join-Path $_.FullName "ISCC.exe" } |
        Where-Object { Test-Path $_ } |
        Select-Object -First 1
    if (-not $iscc) { throw "Inno Setup was not found. Install it with: winget install JRSoftware.InnoSetup" }
}
& $iscc "/DVersion=$short" "/DDist=$dist" "packaging\windows\arfabit.iss"
if ($LASTEXITCODE) { throw "Inno Setup did not finish." }

Write-Host "Made $(Join-Path $dist 'ARFABIT-setup.exe')"

if ($Upload) {
    gh release upload $Version (Join-Path $dist "ARFABIT-setup.exe") --clobber
    if ($LASTEXITCODE) { throw "The installer was not uploaded to the draft release $Version." }
    Write-Host ""
    Write-Host "Put the installer on the draft release $Version. Then, on the Mac:"
    Write-Host ""
    Write-Host "  packaging/release/release.sh finish $Version"
    Write-Host ""
    Write-Host "And here, to carry on with main: git checkout main"
}
