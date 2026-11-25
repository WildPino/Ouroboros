#Requires -RunAsAdministrator
<#
.SYNOPSIS
End-to-end disk-full test on a small virtual disk.

.DESCRIPTION
Creates a VHDX of -SizeMB in -WorkDir, mounts it in a folder (no drive
letter), creates a repository on it and runs "ouroboros run --force" for
-Iterations rounds, adding -ChunkMB of incompressible data each round.
Halfway, another file leaves only the free space the prediction asks for
and the round adds much more than predicted, so the backup runs out of
space and the recovery of DESIGN.md section 3.9 runs. Near the end another
file fills the disk, so the reclaim may not be able to make enough room.

After each round it checks that the free space never stays below min_free
unless Ouroboros reported that it could not reclaim enough (exit code 4),
and at the end it verifies all the data with "ouroboros check --read-data 100".

Nothing outside -WorkDir is touched. The virtual disk is detached and
-WorkDir deleted at the end, unless -Keep is given. See docs/TESTING.md.
#>
param(
    [Parameter(Mandatory = $true)] [string] $WorkDir,
    [int] $SizeMB = 8192,
    [int] $Iterations = 14,
    [int] $ChunkMB = 300,
    [switch] $Keep,
    [string] $LogFile
)

$ErrorActionPreference = 'Continue'
# Computed here: Windows PowerShell 5.1 may leave $PSScriptRoot empty while
# it evaluates parameter defaults.
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Split-Path -Parent $root
if (-not $LogFile) { $LogFile = Join-Path $root 'test-results\diskfull-test.log' }
$minFreeMB = 300

Add-Type -Namespace Ouroboros -Name Disk -MemberDefinition @'
[DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
public static extern bool GetDiskFreeSpaceEx(string dir, out ulong avail, out ulong total, out ulong free);
'@

function Get-FreeMB([string] $dir) {
    $avail = [uint64]0; $total = [uint64]0; $free = [uint64]0
    if (-not [Ouroboros.Disk]::GetDiskFreeSpaceEx($dir, [ref]$avail, [ref]$total, [ref]$free)) {
        throw "GetDiskFreeSpaceEx failed for $dir"
    }
    [math]::Floor($avail / 1MB)
}

function Write-RandomFile([string] $path, [int] $sizeMB) {
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $buf = New-Object byte[] (1MB)
    $f = [System.IO.File]::Create($path)
    try {
        for ($i = 0; $i -lt $sizeMB; $i++) {
            $rng.GetBytes($buf)
            $f.Write($buf, 0, $buf.Length)
        }
    }
    finally { $f.Dispose(); $rng.Dispose() }
}

function Invoke-Diskpart([string[]] $lines) {
    $script = Join-Path $WorkDir 'diskpart.txt'
    Set-Content -Path $script -Value $lines -Encoding Ascii
    # Write-Host keeps the output out of the return value; the transcript
    # still records it.
    & diskpart /s $script | ForEach-Object { Write-Host "  $_" }
    $code = $LASTEXITCODE
    Remove-Item $script
    return $code
}

$WorkDir = [System.IO.Path]::GetFullPath($WorkDir)
if ((Split-Path $WorkDir -Parent) -eq $null -or $WorkDir.Length -le 3) {
    throw 'WorkDir must be a folder, not the root of a drive'
}
if ((Test-Path $WorkDir) -and (Get-ChildItem -Force $WorkDir | Select-Object -First 1)) {
    throw "WorkDir $WorkDir exists and is not empty"
}

New-Item -ItemType Directory -Force -Path (Split-Path $LogFile) | Out-Null
Start-Transcript -Path $LogFile -Force | Out-Null

$bin = Join-Path $WorkDir 'bin'
$mnt = Join-Path $WorkDir 'mnt'
$cfgDir = Join-Path $WorkDir 'config'
$src = Join-Path $WorkDir 'src'
$vhdx = Join-Path $WorkDir 'disk.vhdx'
$cfg = Join-Path $cfgDir 'ouroboros.toml'
$exe = Join-Path $bin 'ouroboros.exe'
$attached = $false
$violations = New-Object System.Collections.Generic.List[string]
$codes = @{}

try {
    "Ouroboros disk-full test, $(Get-Date -Format s)"
    "WorkDir $WorkDir, disk $SizeMB MB, $Iterations rounds of $ChunkMB MB"
    foreach ($d in $bin, $mnt, $cfgDir, $src) { New-Item -ItemType Directory -Force -Path $d | Out-Null }

    Push-Location $root
    & go build -o $exe ./cmd/ouroboros
    $built = $LASTEXITCODE
    Pop-Location
    if ($built -ne 0) { throw 'go build failed' }
    & $exe version
    & restic version

    'Creating and mounting the virtual disk'
    $code = Invoke-Diskpart @(
        "create vdisk file=""$vhdx"" maximum=$SizeMB type=expandable",
        "select vdisk file=""$vhdx""",
        'attach vdisk',
        'create partition primary',
        'format fs=ntfs quick label=OUROTEST',
        "assign mount=""$mnt"""
    )
    $attached = $true
    if ($code -ne 0) { throw "diskpart failed with exit code $code" }
    "Free on the virtual disk: $(Get-FreeMB $mnt) MB"

    $repo = Join-Path $mnt 'repo'
    $toml = @"
[repository]
path = '$repo'

[space]
min_free = "${minFreeMB}MB"
prune_headroom = "1GB"
reserve_file_size = "200MB"
keep_min_snapshots = 1
protect_newer_than = "1m"

[schedule]
retry_after = "1m"

[logging]
level = "info"
event_log = false

[[job]]
name = "data"
paths = ['$src']
interval = "1m"
vss = false
"@
    Set-Content -Path $cfg -Value $toml -Encoding UTF8

    'Creating the repository: type a test password twice (at least 8 characters).'
    & $exe init --config $cfg
    if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 3) { throw "init failed with exit code $LASTEXITCODE" }

    # Expected prediction once history exists: the largest round times
    # growth_factor. The space check before a backup makes room for need;
    # the recovery after a full disk makes room for retry (DESIGN.md 3.4, 3.9).
    $growth = 1.5 * $ChunkMB
    $need = [int]($growth + $minFreeMB + 1024)
    $retry = [int](2 * $growth + $minFreeMB + 1024)
    $spike = [math]::Max([math]::Floor($Iterations / 2), 4)
    $spikeMB = $retry - 100
    $intruder = $Iterations - 2
    $other = Join-Path $mnt 'other-data.bin'
    for ($i = 1; $i -le $Iterations; $i++) {
        $size = $ChunkMB
        if ($i -eq $spike) { $size = $spikeMB }
        Write-RandomFile (Join-Path $src ("round{0:D2}.bin" -f $i)) $size
        # Keep only the last three rounds in the source, so old data becomes
        # unique to old snapshots.
        Get-ChildItem $src -Filter 'round*.bin' | Sort-Object Name | Select-Object -SkipLast 3 | Remove-Item

        if (Test-Path $other) {
            "Round ${i}: the other program frees its space"
            Remove-Item $other
        }
        $fill = 0
        if ($i -eq $spike) {
            # Leave just the free space the prediction asks for, so the larger
            # than predicted backup runs out of space.
            $fill = (Get-FreeMB $mnt) - ($need + 50)
        }
        elseif ($i -eq $intruder) {
            $fill = (Get-FreeMB $mnt) - 100
        }
        if ($fill -gt 0) {
            "Round ${i}: another program fills $fill MB of the disk"
            & fsutil file createnew $other ([int64]$fill * 1MB) | Out-Null
        }

        "---- Round $i/${Iterations}: +$size MB, free before $(Get-FreeMB $mnt) MB"
        $watch = [System.Diagnostics.Stopwatch]::StartNew()
        $out = @(& $exe run --force --config $cfg 2>&1 | ForEach-Object { "$_" })
        $code = $LASTEXITCODE
        $minutes = [math]::Round($watch.Elapsed.TotalMinutes, 1)
        $out | ForEach-Object { "  $_" }
        $codes[$code] = 1 + [int]$codes[$code]
        $free = Get-FreeMB $mnt
        $snaps = (& $exe snapshots --config $cfg 2>&1 | Select-Object -Last 1)
        "Round ${i}: exit code $code, free after $free MB, $snaps, $minutes min"
        # A backup stuck on a full disk must be stopped within about a
        # minute, not left to restic's 15 minutes of retries.
        if ($minutes -gt 5) {
            $violations.Add("round ${i}: took $minutes minutes")
        }
        $fullDiskFailure = $code -eq 1 -and ($out -match 'full disk').Count -gt 0
        if ($code -ne 0 -and $code -ne 3 -and $code -ne 4 -and -not ($i -eq $spike -and $fullDiskFailure)) {
            $violations.Add("round ${i}: unexpected exit code $code")
        }
        if ($free -lt $minFreeMB -and $code -ne 4 -and -not $fullDiskFailure) {
            $violations.Add("round ${i}: free space $free MB below min_free $minFreeMB MB without exit code 4")
        }
    }
    if (Test-Path $other) { Remove-Item $other }

    '---- Verifying all data'
    & $exe check --read-data 100 --config $cfg 2>&1 | ForEach-Object { "  $_" }
    if ($LASTEXITCODE -ne 0) { $violations.Add("final check failed with exit code $LASTEXITCODE") }
    & $exe status --config $cfg 2>&1 | ForEach-Object { "  $_" }

    $logs = Get-ChildItem (Join-Path $cfgDir 'logs') -Filter '*.log' | Get-Content
    $fullDisk = @($logs | Select-String 'backup failed on a full disk').Count
    $reclaims = @($logs | Select-String 'reclaiming space').Count
    $cannot = @($logs | Select-String 'cannot reclaim').Count
    ''
    "Exit codes: $(($codes.GetEnumerator() | Sort-Object Name | ForEach-Object { "$($_.Name) x$($_.Value)" }) -join ', ')"
    "Reclaims started: $reclaims, full-disk recoveries: $fullDisk, cannot-reclaim reports: $cannot"
    if ($reclaims -eq 0) { $violations.Add('no reclaim happened: increase -Iterations or -ChunkMB') }
    if ($fullDisk -eq 0) { 'Note: no backup ran out of space during this run (the prediction covered the spike).' }
}
catch {
    $violations.Add("script error: $_")
}
finally {
    if ($attached) {
        'Detaching the virtual disk'
        $null = Invoke-Diskpart @("select vdisk file=""$vhdx""", 'detach vdisk')
    }
    if ($violations.Count -eq 0) { 'RESULT: PASS' } else { 'RESULT: FAIL'; $violations | ForEach-Object { "  $_" } }
    if (-not $Keep) {
        Remove-Item -Recurse -Force $WorkDir -ErrorAction SilentlyContinue
        "Removed $WorkDir"
    }
    Stop-Transcript | Out-Null
    "Log saved to $LogFile"
}
if ($violations.Count -eq 0) { exit 0 } else { exit 1 }
