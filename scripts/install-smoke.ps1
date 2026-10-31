# Only run on a disposable Windows test machine/CI user, never an existing installation.
$ErrorActionPreference = 'Stop'
$bridgeKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDeviceCable'
$bridgeDestination = Join-Path $env:LOCALAPPDATA 'Programs\WebDeviceCable'
if ((Test-Path $bridgeKey) -or (Test-Path $bridgeDestination)) { throw 'Installer smoke test requires an empty disposable Windows profile.' }
$bridgeVersion = (Get-Content (Join-Path $PSScriptRoot '..\package.json') | ConvertFrom-Json).version
$bridgeStage = Join-Path $PSScriptRoot "..\dist\staging\WebDeviceCable-$bridgeVersion-windows-amd64"
try {
    & (Join-Path $bridgeStage 'Install.ps1') -NoOpen
    $bridgeInstalled = Get-ItemProperty $bridgeKey
    if ($bridgeInstalled.DisplayName -ne 'WebDeviceCable' -or $bridgeInstalled.DisplayVersion -ne $bridgeVersion) { throw 'Installed Apps metadata is incorrect.' }
    if (-not (Test-Path (Join-Path $bridgeDestination 'WebDeviceCable.exe'))) { throw 'Executable not installed.' }
    $bridgeRegistered = (Get-Item 'HKCU:\Software\Classes\webdevicecable\shell\open\command').GetValue('')
    if (-not $bridgeRegistered.Contains('WebDeviceCable Launcher.exe')) { throw 'URL association is missing.' }
    & (Join-Path $bridgeDestination 'Uninstall.ps1')
    if ((Test-Path $bridgeKey) -or (Test-Path $bridgeDestination)) { throw 'Uninstallation left application registration/files.' }
    Write-Host 'WebDeviceCable native installer smoke test passed.'
} finally {
    if (Test-Path (Join-Path $bridgeDestination 'Uninstall.ps1')) { & (Join-Path $bridgeDestination 'Uninstall.ps1') }
}
