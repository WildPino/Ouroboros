<#
.SYNOPSIS
    Build, test and lint Ouroboros.

.DESCRIPTION
    Targets run in the order given. Default: lint, test, build.

      build             Build bin\ouroboros.exe and the development build of
                        bin\app\Ouroboros.exe, which embeds no restic.
      test              Run all unit tests.
      race              Run all unit tests with the race detector (needs cgo and gcc).
      lint              gofmt, go vet and staticcheck.
      release-snapshot  Build the command line zip and Ouroboros.exe, with the
                        pinned restic, and their SHA-256 checksums into dist\.

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
$ProgressPreference = 'SilentlyContinue'
Set-Location -LiteralPath $PSScriptRoot

$CliPkg = './cmd/ouroboros'
$AppPkg = './cmd/ouroboros-app'
$PayloadDir = 'internal/payload/files'
# Build tool only: run with "go run", never a dependency of the program.
$Winres = 'github.com/tc-hib/go-winres@v0.3.3'

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
    param([string] $Version, [switch] $Windowed)
    $flags = "-s -w -X main.version=$Version"
    if ($Windowed) { $flags += ' -H windowsgui' }
    return $flags
}

# Write the manifest, icon and version information of the command in $Dir
# as a .syso file that "go build" links in.
function Invoke-Winres {
    param([string] $Dir, [string] $Version)
    # The version resource needs a number first; untagged builds get 0.0.0.
    $rv = if ($Version -match '^v\d') { $Version } else { "0.0.0-$Version" }
    Invoke-Native go @('run', $Winres, 'make', '--in', "$Dir/winres/winres.json", '--out', "$Dir/rsrc",
        '--arch', 'amd64', '--product-version', $rv, '--file-version', $rv)
}

function Invoke-GoBuild {
    param([string] $Version, [string] $Out, [string] $Pkg, [switch] $Windowed, [string] $Tags = '')
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    try {
        Invoke-Winres $Pkg $Version
        $a = @('build', '-trimpath', '-ldflags', (Get-LdFlags $Version -Windowed:$Windowed), '-o', $Out)
        if ($Tags) { $a += @('-tags', $Tags) }
        Invoke-Native go ($a + $Pkg)
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
        # "go test" would link the resources too, and a test binary with the
        # administrator manifest cannot start from a console that is not
        # elevated.
        Remove-Item -Path "$Pkg/rsrc_windows_*.syso" -ErrorAction SilentlyContinue
    }
}

function Get-Sha256 {
    param([string] $Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

# Stage the files that Ouroboros.exe embeds, gzip-compressed, with the
# manifest read by internal/payload. Each entry has Path (inside the
# installation folder), Source and Version.
function Write-Payload {
    param([object[]] $Entries)
    if (Test-Path -LiteralPath $PayloadDir) { Remove-Item -LiteralPath $PayloadDir -Recurse -Force }
    $files = @()
    foreach ($e in $Entries) {
        $dst = Join-Path $PayloadDir "$($e.Path).gz"
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $dst) | Out-Null
        $in = [IO.File]::OpenRead((Resolve-Path -LiteralPath $e.Source).Path)
        try {
            $out = [IO.File]::Create((Join-Path $PSScriptRoot $dst))
            try {
                $gz = New-Object IO.Compression.GZipStream($out, [IO.Compression.CompressionLevel]::Optimal)
                try { $in.CopyTo($gz) } finally { $gz.Dispose() }
            } finally { $out.Dispose() }
        } finally { $in.Dispose() }
        $files += [ordered]@{
            path    = $e.Path
            version = $e.Version
            size    = (Get-Item -LiteralPath $e.Source).Length
            sha256  = Get-Sha256 $e.Source
        }
    }
    $json = ConvertTo-Json -Depth 4 @{ files = $files }
    # UTF-8 without a byte order mark, which the JSON decoder rejects.
    [IO.File]::WriteAllText((Join-Path $PSScriptRoot "$PayloadDir/manifest.json"), $json, (New-Object Text.UTF8Encoding($false)))
}

function Get-CliVersion {
    param([string] $Exe)
    $out = & $Exe version
    if ($LASTEXITCODE -ne 0 -or $out -notmatch '^ouroboros (\S+)$') { throw "$Exe version printed '$out'" }
    return $Matches[1]
}

# Download the restic release pinned in third_party/restic/restic.json,
# check its SHA-256 and return the path of restic.exe.
function Get-PinnedRestic {
    $pin = Get-Content -Raw -LiteralPath 'third_party/restic/restic.json' | ConvertFrom-Json
    $cache = 'bin/cache'
    New-Item -ItemType Directory -Force -Path $cache | Out-Null
    $zip = Join-Path $cache (Split-Path -Leaf $pin.url)
    if (-not (Test-Path -LiteralPath $zip) -or (Get-Sha256 $zip) -ne $pin.sha256) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -UseBasicParsing -Uri $pin.url -OutFile $zip
    }
    $got = Get-Sha256 $zip
    if ($got -ne $pin.sha256) {
        Remove-Item -LiteralPath $zip -Force
        throw "$($pin.url) has SHA-256 $got, expected $($pin.sha256)"
    }
    $dir = Join-Path $cache "restic_$($pin.version)"
    if (Test-Path -LiteralPath $dir) { Remove-Item -LiteralPath $dir -Recurse -Force }
    Expand-Archive -LiteralPath $zip -DestinationPath $dir
    $exe = Join-Path $dir 'restic.exe'
    Move-Item -LiteralPath (Join-Path $dir $pin.exe) -Destination $exe
    return [pscustomobject]@{ Exe = $exe; Version = $pin.version }
}

# Build Ouroboros.exe embedding the command line at $Cli, the licenses and,
# when given, restic; then check the embedded files with the hidden
# diagnostic argument.
function Invoke-AppBuild {
    param([string] $Version, [string] $Cli, [string] $Out, $Restic)
    $entries = @(
        @{ Path = 'bin/ouroboros.exe'; Source = $Cli; Version = (Get-CliVersion $Cli) },
        @{ Path = 'LICENSE'; Source = 'LICENSE'; Version = '' }
    )
    if ($Restic) {
        $entries += @{ Path = 'restic/restic.exe'; Source = $Restic.Exe; Version = $Restic.Version }
        $entries += @{ Path = 'restic/LICENSE'; Source = 'third_party/restic/LICENSE'; Version = '' }
    }
    Write-Payload $entries
    Invoke-GoBuild -Version $Version -Out $Out -Pkg $AppPkg -Windowed -Tags 'payload'
    # The check needs no privileges: RunAsInvoker lets it start from a
    # console that is not elevated despite the administrator manifest. A
    # windowed program returns at once unless its output is captured.
    $env:__COMPAT_LAYER = 'RunAsInvoker'
    try {
        $report = & $Out --embedded-versions | Out-String
        $code = $LASTEXITCODE
    } finally {
        Remove-Item Env:__COMPAT_LAYER -ErrorAction SilentlyContinue
    }
    if ($code -ne 0) { throw "embedded file check failed:`n$report" }
    Write-Host $report.TrimEnd()
}

function Invoke-Build {
    $v = Get-Version
    Invoke-GoBuild -Version $v -Out 'bin/ouroboros.exe' -Pkg $CliPkg
    Write-Host "built bin\ouroboros.exe ($v)"
    Invoke-AppBuild -Version $v -Cli 'bin/ouroboros.exe' -Out 'bin/app/Ouroboros.exe'
    Write-Host "built bin\app\Ouroboros.exe ($v, development build without restic)"
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

    $cli = Join-Path $stage 'ouroboros.exe'
    Invoke-GoBuild -Version $v -Out $cli -Pkg $CliPkg
    Copy-Item -LiteralPath 'ouroboros.example.toml', 'LICENSE', 'README.md', 'CHANGELOG.md' -Destination $stage
    # The restore guide must be at hand when the machine that made the
    # backups is not.
    New-Item -ItemType Directory -Force -Path (Join-Path $stage 'docs') | Out-Null
    Copy-Item -LiteralPath 'docs/RESTORE.md' -Destination (Join-Path $stage 'docs')

    $zip = Join-Path 'dist' "$name.zip"
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip

    $appExe = Join-Path 'dist' 'Ouroboros.exe'
    Invoke-AppBuild -Version $v -Cli $cli -Out $appExe -Restic (Get-PinnedRestic)

    $sums = Join-Path 'dist' 'checksums.txt'
    @(
        "$(Get-Sha256 $appExe)  Ouroboros.exe",
        "$(Get-Sha256 $zip)  $name.zip"
    ) | Out-File -LiteralPath $sums -Encoding ascii
    Write-Host "created $zip"
    Write-Host "created $appExe"
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
