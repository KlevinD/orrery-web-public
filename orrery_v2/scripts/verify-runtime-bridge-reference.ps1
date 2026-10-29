[CmdletBinding()]
param(
    [string]$ReferencePath
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($ReferencePath)) {
    $ReferencePath = Join-Path $PSScriptRoot "..\.cache\upstream\aioncore"
}

$expectedCommit = "76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d"
$expectedTag = "v0.1.52"
$expectedTreeEntries = 32
$expectedTreeSha256 = "249f18b4d12e0bc037fe3397f5e0542bee27508995f49342bc4a5c27d8957db4"

$resolvedReference = (Resolve-Path -LiteralPath $ReferencePath).Path

function Invoke-ReferenceGit {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)

    $output = @(& git -C $resolvedReference @Arguments)
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed for $resolvedReference"
    }
    return $output
}

$commit = (Invoke-ReferenceGit rev-parse HEAD | Select-Object -First 1).Trim()
if ($commit -ne $expectedCommit) {
    throw "AionCore commit mismatch: got $commit, want $expectedCommit"
}

$tag = (Invoke-ReferenceGit describe --tags --exact-match | Select-Object -First 1).Trim()
if ($tag -ne $expectedTag) {
    throw "AionCore tag mismatch: got $tag, want $expectedTag"
}

$worktreeChanges = @(Invoke-ReferenceGit status --porcelain -- crates/aionui-session)
if ($worktreeChanges.Count -ne 0) {
    throw "aionui-session reference checkout has local changes: $($worktreeChanges -join '; ')"
}

$tree = @(Invoke-ReferenceGit ls-tree -r HEAD crates/aionui-session)
if ($tree.Count -ne $expectedTreeEntries) {
    throw "aionui-session tree entry count mismatch: got $($tree.Count), want $expectedTreeEntries"
}

$normalizedTree = ($tree -join "`n") + "`n"
$treeBytes = [System.Text.UTF8Encoding]::new($false).GetBytes($normalizedTree)
$sha256 = [System.Security.Cryptography.SHA256]::Create()
try {
    $treeHash = ([BitConverter]::ToString($sha256.ComputeHash($treeBytes)) -replace "-", "").ToLowerInvariant()
} finally {
    $sha256.Dispose()
}
if ($treeHash -ne $expectedTreeSha256) {
    throw "aionui-session tree digest mismatch: got $treeHash, want $expectedTreeSha256"
}

[pscustomobject]@{
    Reference = $resolvedReference
    Tag = $tag
    Commit = $commit
    TreeEntries = $tree.Count
    TreeSha256 = $treeHash
}
