$ErrorActionPreference = 'Stop'
$bridgeDestination = Join-Path $env:LOCALAPPDATA 'Programs\WebDeviceCable'
New-Item -ItemType Directory -Force -Path $bridgeDestination | Out-Null
if (Test-Path (Join-Path $bridgeDestination 'webdevicecable.exe')) {
    & (Join-Path $PSScriptRoot 'webdevicecable.exe') desktop-stop
    if ($LASTEXITCODE -ne 0) { throw 'Close WebDeviceCable before updating.' }
    & (Join-Path $bridgeDestination 'webdevicecable.exe') stop
    Start-Sleep -Milliseconds 1000
}
Copy-Item (Join-Path $PSScriptRoot 'webdevicecable.exe') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'WebDeviceCable Launcher.exe') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'Uninstall.cmd') $bridgeDestination -Force
Copy-Item (Join-Path $PSScriptRoot 'Uninstall.ps1') $bridgeDestination -Force
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
Write-Host 'WebDeviceCable installed. Open it from the Start menu.'
Start-Process (Join-Path $bridgeDestination 'WebDeviceCable Launcher.exe')
