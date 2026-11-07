param([switch]$NoOpen)
$ErrorActionPreference = 'Stop'
$bridgeDestination = Join-Path $env:LOCALAPPDATA 'Programs\WebDeviceCable'
New-Item -ItemType Directory -Force -Path $bridgeDestination | Out-Null
if (Test-Path (Join-Path $bridgeDestination 'WebDeviceCable.exe')) {
    & (Join-Path $PSScriptRoot 'WebDeviceCable.exe') desktop-stop
    if ($LASTEXITCODE -ne 0) { throw 'Close WebDeviceCable before updating.' }
    & (Join-Path $bridgeDestination 'WebDeviceCable.exe') stop --if-running
    Start-Sleep -Milliseconds 1000
}
Copy-Item (Join-Path $PSScriptRoot 'WebDeviceCable.exe') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'WebDeviceCable Launcher.exe') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'Uninstall.cmd') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'Uninstall.ps1') $bridgeDestination -Force
foreach ($bridgeNotice in @('LICENSE', 'THIRD_PARTY_NOTICES.md', 'build-manifest.json')) {
    Copy-Item (Join-Path $PSScriptRoot $bridgeNotice) $bridgeDestination -Force
}
$bridgeShell = New-Object -ComObject WScript.Shell
$bridgeShortcut = $bridgeShell.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Programs')) 'WebDeviceCable.lnk'))
$bridgeShortcut.TargetPath = Join-Path $bridgeDestination 'WebDeviceCable Launcher.exe'
$bridgeShortcut.WorkingDirectory = $bridgeDestination
$bridgeShortcut.Description = 'Open your local WebDeviceCable control panel'
$bridgeShortcut.Save()
$bridgeProtocol = 'HKCU:\Software\Classes\webdevicecable'
New-Item -Force $bridgeProtocol | Out-Null
Set-Item $bridgeProtocol -Value 'URL:WebDeviceCable'
New-ItemProperty -Path $bridgeProtocol -Name 'URL Protocol' -Value '' -Force | Out-Null
New-Item -Force ($bridgeProtocol + '\shell\open\command') | Out-Null
Set-Item ($bridgeProtocol + '\shell\open\command') -Value ('"' + (Join-Path $bridgeDestination 'WebDeviceCable Launcher.exe') + '" launch "%1"')
$bridgeUninstall = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDeviceCable'
New-Item -Force $bridgeUninstall | Out-Null
$bridgeVersion = & (Join-Path $bridgeDestination 'WebDeviceCable.exe') version
if ($LASTEXITCODE -ne 0) { throw 'Could not read WebDeviceCable version.' }
$bridgeValues = @{
    DisplayName = 'WebDeviceCable'
    DisplayVersion = [string]$bridgeVersion
    Publisher = 'WebDeviceCable project'
    InstallLocation = $bridgeDestination
    DisplayIcon = (Join-Path $bridgeDestination 'WebDeviceCable Launcher.exe')
    UninstallString = ('"' + $env:ComSpec + '" /c ""' + (Join-Path $bridgeDestination 'Uninstall.cmd') + '""')
    URLInfoAbout = 'https://github.com/thomasmikava/WebDeviceCable'
}
foreach ($bridgeName in $bridgeValues.Keys) { New-ItemProperty -Path $bridgeUninstall -Name $bridgeName -Value $bridgeValues[$bridgeName] -PropertyType String -Force | Out-Null }
New-ItemProperty -Path $bridgeUninstall -Name 'NoModify' -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $bridgeUninstall -Name 'NoRepair' -Value 1 -PropertyType DWord -Force | Out-Null
Write-Host 'WebDeviceCable installed. Open it from the Start menu.'
if (-not $NoOpen) { Start-Process (Join-Path $bridgeDestination 'WebDeviceCable Launcher.exe') }
