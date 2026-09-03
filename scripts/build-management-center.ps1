[CmdletBinding()]
param(
    [string]$OutputPath = 'dist\management.html',
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'

$managementCenterRepository = 'https://github.com/router-for-me/Cli-Proxy-API-Management-Center.git'
$managementCenterCommit = 'e0ee7123dfb5aa89a14ff73ac5a5c3bf4db658e0'
$bunVersion = '1.3.14'
$projectRoot = Split-Path -Parent $PSScriptRoot
$patchPath = Join-Path $projectRoot 'management-center\patches\e0ee712-mirasim-quota.patch'
$resolvedOutputPath = if ([System.IO.Path]::IsPathRooted($OutputPath)) {
    [System.IO.Path]::GetFullPath($OutputPath)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $projectRoot $OutputPath))
}

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw 'Git is required to build the Management Center.'
}
if (-not (Get-Command bun -ErrorAction SilentlyContinue) -and -not (Get-Command npx -ErrorAction SilentlyContinue)) {
    throw 'Bun or npx is required to build the Management Center.'
}
if (-not (Test-Path -LiteralPath $patchPath -PathType Leaf)) {
    throw "Management Center patch is missing: $patchPath"
}

function Invoke-GitChecked {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    & git @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "git failed: git $($Arguments -join ' ')"
    }
}

function Invoke-BunChecked {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    if (Get-Command bun -ErrorAction SilentlyContinue) {
        & bun @Arguments
    } else {
        & npx --yes "bun@$bunVersion" @Arguments
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Bun failed: $($Arguments -join ' ')"
    }
}

$tempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd(
    [System.IO.Path]::DirectorySeparatorChar,
    [System.IO.Path]::AltDirectorySeparatorChar
)
$workTree = [System.IO.Path]::GetFullPath(
    (Join-Path $tempBase ('cpa-mirasim-management-' + [guid]::NewGuid().ToString('N')))
)
$tempPrefix = $tempBase + [System.IO.Path]::DirectorySeparatorChar
if (-not $workTree.StartsWith($tempPrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
    -not (Split-Path -Leaf $workTree).StartsWith('cpa-mirasim-management-', [System.StringComparison]::Ordinal)) {
    throw "Refusing to use unexpected temporary path: $workTree"
}

$previousVersion = $env:VERSION
try {
    New-Item -ItemType Directory -Path $workTree | Out-Null
    Invoke-GitChecked '-C' $workTree 'init'
    Invoke-GitChecked '-C' $workTree 'remote' 'add' 'origin' $managementCenterRepository
    $fetched = $false
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        & git -C $workTree fetch --depth 1 origin $managementCenterCommit
        if ($LASTEXITCODE -eq 0) {
            $fetched = $true
            break
        }
        if ($attempt -lt 3) {
            Start-Sleep -Seconds (2 * $attempt)
        }
    }
    if (-not $fetched) {
        throw "git fetch failed after 3 attempts: $managementCenterCommit"
    }
    Invoke-GitChecked '-C' $workTree 'checkout' '--detach' 'FETCH_HEAD'
    Invoke-GitChecked '-C' $workTree 'apply' '--check' $patchPath
    Invoke-GitChecked '-C' $workTree 'apply' $patchPath

    Push-Location $workTree
    try {
        Invoke-BunChecked 'install' '--frozen-lockfile'
        Invoke-BunChecked 'run' 'type-check'
        if (-not $SkipTests) {
            Invoke-BunChecked 'test'
        }
        $env:VERSION = 'mirasim-quota-0.5.0'
        Invoke-BunChecked 'run' 'build'
    } finally {
        Pop-Location
    }

    $builtHTML = Join-Path $workTree 'dist\index.html'
    if (-not (Test-Path -LiteralPath $builtHTML -PathType Leaf)) {
        throw 'Management Center build did not produce dist\index.html.'
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $resolvedOutputPath) | Out-Null
    Copy-Item -LiteralPath $builtHTML -Destination $resolvedOutputPath -Force
    $hash = (Get-FileHash -LiteralPath $resolvedOutputPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "Management Center: $resolvedOutputPath"
    Write-Output "SHA256: $hash"
} finally {
    $env:VERSION = $previousVersion
    $resolvedWorkTree = [System.IO.Path]::GetFullPath($workTree)
    if ($resolvedWorkTree.StartsWith($tempPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedWorkTree).StartsWith('cpa-mirasim-management-', [System.StringComparison]::Ordinal) -and
        (Test-Path -LiteralPath $resolvedWorkTree)) {
        Remove-Item -LiteralPath $resolvedWorkTree -Recurse -Force
    }
}
