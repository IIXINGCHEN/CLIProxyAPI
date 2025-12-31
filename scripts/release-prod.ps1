param(
  [Parameter(Mandatory = $true)]
  [string]$Version,
  [string]$Remote = "origin",
  [string]$Branch = "main"
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

function Ensure-Command([string]$Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "$Name not found in PATH"
  }
}

function Ensure-CleanWorkingTree {
  $status = (& git status --porcelain)
  if ($status -and $status.Count -gt 0) {
    throw "git working tree is dirty. Commit/stash changes before releasing."
  }
}

function Ensure-ValidVersion([string]$V) {
  if (-not ($V -match "^v\\d+\\.\\d+\\.\\d+$")) {
    throw "invalid version tag: $V. Required format: v6.6.74"
  }
}

function Get-HeadCommit {
  return (& git rev-parse HEAD).Trim()
}

function Get-CurrentBranch {
  return (& git rev-parse --abbrev-ref HEAD).Trim()
}

function Get-TagCommit([string]$Tag) {
  return (& git rev-list -n 1 $Tag).Trim()
}

$repoRoot = Resolve-RepoRoot
Set-Location $repoRoot

Ensure-Command "git"
Ensure-ValidVersion $Version
Ensure-CleanWorkingTree

$currentBranch = Get-CurrentBranch
if ($currentBranch -ne $Branch) {
  throw "release requires branch '$Branch'. Current branch: '$currentBranch'."
}

$head = Get-HeadCommit

# If tag exists locally, only allow if it already points to HEAD.
$existingLocal = (& git tag -l $Version).Trim()
if ($existingLocal) {
  $tagCommit = Get-TagCommit $Version
  if ($tagCommit -ne $head) {
    throw "tag already exists locally and does not point to HEAD: $Version ($tagCommit != $head). Choose a new version."
  }
} else {
  & git tag -a $Version -m "release $Version" | Out-Null
}

Write-Host "Pushing release..."
Write-Host "  Remote:  $Remote"
Write-Host "  Branch:  $Branch"
Write-Host "  Tag:     $Version"
Write-Host "  Commit:  $head"

& git push $Remote $Branch
& git push $Remote $Version

Write-Host "Done. GitHub Actions should publish the release for tag $Version."
