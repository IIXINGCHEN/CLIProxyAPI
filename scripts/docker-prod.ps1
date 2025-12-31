param(
  [string]$ComposeService = "cli-proxy-api",
  [string]$Image = "cli-proxy-api:local",
  [switch]$AllowDirty,
  [switch]$NoCache
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

$repoRoot = Resolve-RepoRoot
Set-Location $repoRoot

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
  throw "git not found in PATH"
}

$dirty = $false
$status = (& git status --porcelain)
if ($status -and $status.Count -gt 0) { $dirty = $true }
if ($dirty -and -not $AllowDirty) {
  throw "git working tree is dirty. Commit/stash changes, or re-run with -AllowDirty."
}

$env:VERSION = (git describe --tags --abbrev=0).Trim()
$env:COMMIT = (git rev-parse --short HEAD).Trim()
$env:BUILD_DATE = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$env:CLI_PROXY_IMAGE = $Image

if (-not $env:VERSION -or $env:VERSION -eq "dev") {
  throw "docker production build requires a git tag (e.g. v6.6.74). Create a tag first."
}

Write-Host "Docker production build..."
Write-Host "  Service: $ComposeService"
Write-Host "  Image:   $env:CLI_PROXY_IMAGE"
Write-Host "  Version: $env:VERSION"
Write-Host "  Commit:  $env:COMMIT"
Write-Host "  Date:    $env:BUILD_DATE"

$buildArgs = @("compose", "build")
if ($NoCache) { $buildArgs += "--no-cache" }
$buildArgs += @($ComposeService)
& docker @buildArgs

& docker compose up -d --remove-orphans --pull never $ComposeService
