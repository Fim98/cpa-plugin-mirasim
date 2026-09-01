[CmdletBinding()]
param(
    [string]$Version = '0.0.0-dev',
    [string]$OutputDirectory = 'dist'
)

$ErrorActionPreference = 'Stop'

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go 1.26 or later is required.'
}
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    throw 'A GCC-compatible C compiler is required for -buildmode=c-shared.'
}

$projectRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = if ([System.IO.Path]::IsPathRooted($OutputDirectory)) {
    $OutputDirectory
} else {
    Join-Path $projectRoot $OutputDirectory
}
$outputPath = Join-Path $outputRoot 'mirasim.dll'
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

Push-Location $projectRoot
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed.' }

    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed.' }

    go build -trimpath -buildmode=c-shared -ldflags "-s -w -X main.pluginVersion=$Version" -o $outputPath ./cmd/mirasim
    if ($LASTEXITCODE -ne 0) { throw 'plugin build failed.' }

    $headerPath = [System.IO.Path]::ChangeExtension($outputPath, '.h')
    if (Test-Path -LiteralPath $headerPath) {
        Remove-Item -LiteralPath $headerPath -Force
    }

    Write-Output (Resolve-Path -LiteralPath $outputPath)
} finally {
    Pop-Location
}
