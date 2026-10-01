# Builds ARFABIT for Windows: core\arfabit.exe, the shell ARFABIT.exe, and
# ARFABIT-setup.exe (docs/RELEASE.md §3, §5). Run on Windows with Go, the
# .NET SDK and Inno Setup 6:
#
#   packaging\windows\build.ps1            a "dev" build
#   packaging\windows\build.ps1 v0.1.0     a release
#
# Everything goes in dist\windows.

param([string]$Version = "dev")

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

$dist = Join-Path $root "dist\windows"
if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Force (Join-Path $dist "core") | Out-Null

Write-Host "ARFABIT $Version"

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
$iscc = Get-Command iscc -ErrorAction SilentlyContinue
$iscc = if ($iscc) { $iscc.Source } else { Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe" }
& $iscc "/DVersion=$short" "/DDist=$dist" "packaging\windows\arfabit.iss"
if ($LASTEXITCODE) { throw "Inno Setup did not finish." }

Write-Host "Made $(Join-Path $dist 'ARFABIT-setup.exe')"
