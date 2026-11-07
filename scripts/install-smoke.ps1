# Only run on a disposable Windows test machine/CI user, never an existing installation.
$ErrorActionPreference = 'Stop'
$bridgeKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDeviceCable'
$bridgeDestination = Join-Path $env:LOCALAPPDATA 'Programs\WebDeviceCable'
if ((Test-Path $bridgeKey) -or (Test-Path $bridgeDestination)) { throw 'Installer smoke test requires an empty disposable Windows profile.' }
$bridgeVersion = (Get-Content (Join-Path $PSScriptRoot '..\package.json') | ConvertFrom-Json).version
$bridgeStage = Join-Path $PSScriptRoot "..\dist\staging\WebDeviceCable-$bridgeVersion-windows-amd64"
$bridgeInstaller = Join-Path $PSScriptRoot "..\dist\WebDeviceCable-$bridgeVersion-Windows-x64-Setup.exe"
$bridgeData = Join-Path $env:APPDATA 'WebDeviceCable'
if (Test-Path $bridgeData) { throw 'Installer smoke test requires an empty disposable WDC data directory.' }
New-Item -ItemType Directory -Path $bridgeData | Out-Null
$bridgeSentinel = Join-Path $bridgeData 'installer-smoke.keep'
Set-Content $bridgeSentinel 'preserve settings and connections'
function Install-Exe {
    $bridgeProcess = Start-Process -FilePath $bridgeInstaller -ArgumentList '/S' -Wait -PassThru
    if ($bridgeProcess.ExitCode -ne 0) { throw ('EXE installer failed: ' + $bridgeProcess.ExitCode) }
}
function Assert-Installed {
    $bridgeInstalled = Get-ItemProperty $bridgeKey
    if ($bridgeInstalled.DisplayName -ne 'WebDeviceCable' -or $bridgeInstalled.DisplayVersion -ne $bridgeVersion) { throw 'Installed Apps metadata is incorrect.' }
    foreach ($bridgeFile in @('WebDeviceCable.exe', 'WebDeviceCable Launcher.exe', 'Uninstall.cmd', 'Uninstall.ps1', 'THIRD_PARTY_NOTICES.md', 'build-manifest.json')) {
        if (-not (Test-Path (Join-Path $bridgeDestination $bridgeFile))) { throw ('Missing installed file: ' + $bridgeFile) }
    }
    $bridgeRegistered = (Get-Item 'HKCU:\Software\Classes\webdevicecable\shell\open\command').GetValue('')
    if (-not $bridgeRegistered.Contains('WebDeviceCable Launcher.exe')) { throw 'URL association is missing.' }
    if (-not (Test-Path (Join-Path ([Environment]::GetFolderPath('Programs')) 'WebDeviceCable.lnk'))) { throw 'Start-menu shortcut is missing.' }
    if ((Get-Content $bridgeSentinel) -ne 'preserve settings and connections') { throw 'Installer modified private data.' }
}
function Uninstall-App {
    & (Join-Path $bridgeDestination 'Uninstall.ps1')
    if ((Test-Path $bridgeKey) -or (Test-Path $bridgeDestination)) { throw 'Uninstallation left application registration/files.' }
    if (-not (Test-Path $bridgeSentinel)) { throw 'Uninstallation removed private data.' }
}
try {
    Install-Exe
    Assert-Installed
    Install-Exe
    Assert-Installed
    Uninstall-App
    # Existing ZIP/script installations must upgrade through the same EXE.
    & (Join-Path $bridgeStage 'Install.ps1') -NoOpen
    Install-Exe
    Assert-Installed
    Uninstall-App
    $bridgeArmInstaller = Join-Path $PSScriptRoot "..\dist\WebDeviceCable-$bridgeVersion-Windows-ARM64-Setup.exe"
    $bridgeWrongArch = Start-Process -FilePath $bridgeArmInstaller -ArgumentList '/S' -Wait -PassThru
    if ($bridgeWrongArch.ExitCode -eq 0 -or (Test-Path $bridgeDestination)) { throw 'ARM64 installer did not reject an x64 PC.' }
    Write-Host 'WebDeviceCable EXE install, upgrade, architecture and removal smoke tests passed.'
} finally {
    if (Test-Path (Join-Path $bridgeDestination 'Uninstall.ps1')) { & (Join-Path $bridgeDestination 'Uninstall.ps1') }
    Remove-Item $bridgeSentinel -Force
    Remove-Item $bridgeData -Recurse -Force
}
