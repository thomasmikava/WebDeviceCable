$ErrorActionPreference = 'Stop'
$bridgeDestination = Join-Path $env:LOCALAPPDATA 'Programs\WebDeviceCable'
if (Test-Path (Join-Path $bridgeDestination 'webdevicecable.exe')) {
    & (Join-Path $bridgeDestination 'webdevicecable.exe') desktop-stop
    if ($LASTEXITCODE -ne 0) { throw 'Close WebDeviceCable before uninstalling.' }
    & (Join-Path $bridgeDestination 'webdevicecable.exe') stop
    Start-Sleep -Milliseconds 1000
}
$bridgeShortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'WebDeviceCable.lnk'
Remove-Item $bridgeShortcut -Force -ErrorAction SilentlyContinue
$bridgeProtocol = 'HKCU:\Software\Classes\webdevicecable'
$bridgeProtocolKey = Get-Item ($bridgeProtocol + '\shell\open\command') -ErrorAction SilentlyContinue
$bridgeRegistered = if ($bridgeProtocolKey) { $bridgeProtocolKey.GetValue('') } else { '' }
if ($bridgeRegistered -and $bridgeRegistered.Contains($bridgeDestination)) { Remove-Item $bridgeProtocol -Recurse -Force }
# Fixed per-user installation only. Never delete the user's configuration.
Remove-Item $bridgeDestination -Recurse -Force -ErrorAction SilentlyContinue
Write-Host 'WebDeviceCable removed. Your private settings and connection history are retained.'
