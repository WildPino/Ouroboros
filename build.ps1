<#
.SYNOPSIS
    Build, test and lint Ouroboros.

.DESCRIPTION
    Targets run in the order given. Default: lint, test, build.

      build             Build bin\ouroboros.exe for the host platform.
      test              Run all unit tests.
      race              Run all unit tests with the race detector (needs cgo and gcc).
      lint              gofmt, go vet and staticcheck.
      release-snapshot  Build a release zip and SHA-256 checksums into dist\.

.EXAMPLE
    .\build.ps1 lint test
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0, ValueFromRemainingArguments = $true)]
    [ValidateSet('build', 'test', 'race', 'lint', 'release-snapshot')]
    [string[]] $Target = @('lint', 'test', 'build')
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

$Main = './cmd/ouroboros'

function Invoke-Native {
    param([string] $Exe, [string[]] $Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Exe $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Get-Version {
    $v = & git describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $v) { return 'dev' }
    return $v.Trim()
}

function Get-LdFlags {
    param([string] $Version)
    return "-s -w -X main.version=$Version"
}

function Invoke-Build {
    $env:CGO_ENABLED = '0'
    $v = Get-Version
    Invoke-Native go @('build', '-trimpath', '-ldflags', (Get-LdFlags $v), '-o', 'bin/ouroboros.exe', $Main)
    Write-Host "built bin\ouroboros.exe ($v)"
}

function Invoke-Test {
    $env:CGO_ENABLED = '0'
    Invoke-Native go @('test', './...')
}

function Invoke-Race {
    $env:CGO_ENABLED = '1'
    Invoke-Native go @('test', '-race', './...')
}

function Invoke-Lint {
    $unformatted = & gofmt -l .
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed' }
    if ($unformatted) {
        throw "files not formatted with gofmt:`n$($unformatted -join "`n")"
    }
    Invoke-Native go @('vet', './...')
    if (-not (Get-Command staticcheck -ErrorAction SilentlyContinue)) {
        throw 'staticcheck not found; install it with: go install honnef.co/go/tools/cmd/staticcheck@latest'
    }
    Invoke-Native staticcheck @('./...')
}

function Invoke-ReleaseSnapshot {
    $v = Get-Version
    $name = "ouroboros_${v}_windows_amd64"
    $stage = Join-Path 'dist' $name
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null

    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    try {
        Invoke-Native go @('build', '-trimpath', '-ldflags', (Get-LdFlags $v), '-o', (Join-Path $stage 'ouroboros.exe'), $Main)
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    }
    Copy-Item -LiteralPath 'ouroboros.example.toml', 'LICENSE', 'README.md' -Destination $stage

    $zip = Join-Path 'dist' "$name.zip"
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip

    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    $sums = Join-Path 'dist' 'checksums.txt'
    "$hash  $name.zip" | Out-File -LiteralPath $sums -Encoding ascii
    Write-Host "created $zip"
    Write-Host "created $sums"
}

foreach ($t in $Target) {
    Write-Host "==> $t"
    switch ($t) {
        'build'            { Invoke-Build }
        'test'             { Invoke-Test }
        'race'             { Invoke-Race }
        'lint'             { Invoke-Lint }
        'release-snapshot' { Invoke-ReleaseSnapshot }
    }
}
