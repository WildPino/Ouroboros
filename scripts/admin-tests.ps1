#Requires -RunAsAdministrator
<#
.SYNOPSIS
Runs the tests that need an elevated console.

.DESCRIPTION
Runs every test whose name contains "Admin" with OUROBOROS_ADMIN_TESTS=1.
They work only in temporary directories, but they use system facilities
such as Volume Shadow Copies. The output is saved to the log file.
See docs/TESTING.md.
#>
param(
    [string] $LogFile = (Join-Path (Split-Path $PSScriptRoot) 'test-results\admin-tests.log')
)

$ErrorActionPreference = 'Continue'
$root = Split-Path $PSScriptRoot
New-Item -ItemType Directory -Force -Path (Split-Path $LogFile) | Out-Null
Start-Transcript -Path $LogFile -Force | Out-Null
$code = 1
try {
    Push-Location $root
    "Ouroboros admin tests, $(Get-Date -Format s)"
    & go version
    & restic version
    $env:OUROBOROS_ADMIN_TESTS = '1'
    & go test -count=1 -v -run 'Admin' ./... 2>&1 | ForEach-Object { "$_" }
    $code = $LASTEXITCODE
    if ($code -eq 0) { 'RESULT: PASS' } else { "RESULT: FAIL (go test exit code $code)" }
}
finally {
    Remove-Item Env:OUROBOROS_ADMIN_TESTS -ErrorAction SilentlyContinue
    Pop-Location
    Stop-Transcript | Out-Null
}
"Log saved to $LogFile"
exit $code
