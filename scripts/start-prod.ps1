param(
  [string]$ConfigPath = "config.yaml",
  [string]$Out = "bin\\cliproxyapi.exe",
  [switch]$AllowDirty,
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$Args
)

$ErrorActionPreference = "Stop"

function Resolve-RepoRoot {
  $scriptDir = $PSScriptRoot
  if (-not $scriptDir) {
    $scriptDir = Split-Path -Parent $PSCommandPath
  }
  if (-not $scriptDir) {
    throw "failed to resolve script directory"
  }
  return (Resolve-Path (Join-Path $scriptDir "..")).Path
}

function Get-GitValue([string]$Command, [string]$Fallback) {
  try {
    $v = (& git $Command.Split(" ") 2>$null).Trim()
    if ($v) { return $v }
  } catch {}
  return $Fallback
}

$repoRoot = Resolve-RepoRoot
Set-Location $repoRoot

if (-not (Test-Path $ConfigPath)) {
  throw "config file not found: $ConfigPath"
}

$dirty = $false
try {
  $status = (& git status --porcelain 2>$null)
  if ($status -and $status.Count -gt 0) { $dirty = $true }
} catch {}

if ($dirty -and -not $AllowDirty) {
  throw "git working tree is dirty. Commit/stash changes, or re-run with -AllowDirty."
}

$version = Get-GitValue "describe --tags --abbrev=0" "dev"
$commit = Get-GitValue "rev-parse --short HEAD" "none"
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

if ($version -eq "dev") {
  throw "production build requires a git tag (e.g. v6.6.74). Create a tag, or set Version explicitly."
}

Write-Host "Building production binary..."
Write-Host "  Version: $version"
Write-Host "  Commit:  $commit"
Write-Host "  Date:    $buildDate"

if ($dirty -and $AllowDirty) {
  Write-Warning "git working tree is dirty; continuing because -AllowDirty is set. Version excludes -dirty suffix."
}

$env:CGO_ENABLED = "0"
$env:GIN_MODE = "release"

$outDir = Split-Path -Parent $Out
if ($outDir -and -not (Test-Path $outDir)) {
  New-Item -ItemType Directory -Force $outDir | Out-Null
}

go build -trimpath -ldflags "-s -w -X 'main.Version=$version' -X 'main.Commit=$commit' -X 'main.BuildDate=$buildDate'" `
  -o $Out .\\cmd\\server\\

Write-Host "Starting server..."
& (Resolve-Path $Out).Path -config $ConfigPath @Args
