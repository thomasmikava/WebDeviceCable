; A single Windows download, built on Windows, macOS or Linux with NSIS 3.
; Keep install/upgrade/removal behavior in the existing signed PowerShell scripts.
Unicode true
RequestExecutionLevel user
ManifestDPIAware true
SetCompressor /SOLID zlib
!include "MUI2.nsh"
!include "x64.nsh"

Name "WebDeviceCable ${VERSION}"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\WebDeviceCable"
BrandingText "WebDeviceCable"
VIProductVersion "${NUMERIC_VERSION}.0"
VIAddVersionKey /LANG=1033 "ProductName" "WebDeviceCable"
VIAddVersionKey /LANG=1033 "FileDescription" "WebDeviceCable installer (${ARCH_LABEL})"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "WebDeviceCable contributors"

!define MUI_WELCOMEPAGE_TEXT "Install WebDeviceCable for your Windows account.$\r$\n$\r$\nYour settings and connections are kept when upgrading."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\WebDeviceCable Launcher.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open WebDeviceCable"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_LANGUAGE "English"

Function .onInit
!if "${ARCH}" == "amd64"
  ${IfNot} ${IsNativeAMD64}
    MessageBox MB_OK|MB_ICONSTOP "This installer is for Windows on Intel or AMD x64 PCs. For Windows on Arm, download Windows-ARM64-Setup.exe." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
!else if "${ARCH}" == "arm64"
  ${IfNot} ${IsNativeARM64}
    MessageBox MB_OK|MB_ICONSTOP "This installer is for Windows on Arm. For Intel or AMD PCs, download Windows-x64-Setup.exe." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
!else
  !error "Unsupported Windows architecture"
!endif
FunctionEnd

; Native Windows makensis does not normalize mixed host path separators.
; Change directory once so File instructions work on every build host.
!cd "${STAGE}"
Section "WebDeviceCable"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "build-manifest.json"
  File "WebDeviceCable.exe"
  File "WebDeviceCable Launcher.exe"
  File "Install.ps1"
  File "Uninstall.cmd"
  File "Uninstall.ps1"
  File "LICENSE"
  File "THIRD_PARTY_NOTICES.md"
  ; Use native PowerShell so Installed Apps registration matches the app's
  ; native registry view, even though the NSIS bootstrap itself is 32-bit.
  nsExec::ExecToStack '"$WINDIR\SysNative\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\Install.ps1" -NoOpen'
  Pop $0
  Pop $1
  ${If} $0 != 0
    DetailPrint "$1"
    MessageBox MB_OK|MB_ICONSTOP "WebDeviceCable could not be installed. Close WebDeviceCable and try again.$\r$\n$\r$\n$1" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
SectionEnd
