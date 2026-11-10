Unicode true
!define APPNAME "XFlow"
!define APPVERSION "1.2.6"
!define PUBLISHER "qingqiuya"
!define APPEXE "XFlow.exe"

Name "${APPNAME}"
OutFile "XFlow-Setup.exe"
InstallDir "$LOCALAPPDATA\${APPNAME}"
RequestExecutionLevel user
SetCompressor /SOLID lzma
Icon "app.ico"
UninstallIcon "app.ico"

VIProductVersion "1.2.6.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileDescription" "${APPNAME} 下载器"
VIAddVersionKey "FileVersion" "${APPVERSION}"
VIAddVersionKey "ProductVersion" "${APPVERSION}"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "LegalCopyright" "© ${PUBLISHER}"

!include "MUI2.nsh"
!define MUI_ICON "app.ico"
!define MUI_UNICON "app.ico"
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\${APPEXE}"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 XFlow"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Section "Install"
  ; 升级安装时先把正在跑的 XFlow 关掉：exe 被占用会导致写入失败，
  ; 用户会看到「无法打开要写入的文件」。没在跑的话 taskkill 报错也无害。
  DetailPrint "关闭正在运行的 XFlow…"
  nsExec::Exec 'taskkill /IM XFlow.exe /F /T'
  Pop $0
  Sleep 800

  SetOutPath "$INSTDIR"
  File "XFlow.exe"
  File "app.ico"
  ; 浏览器接管插件（不预装，用户到 chrome://extensions 手动加载 unpacked）
  File /r "ext"
  File "浏览器插件-安装说明.txt"

  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortCut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\${APPEXE}" "" "$INSTDIR\app.ico" 0
  CreateShortCut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${APPEXE}" "" "$INSTDIR\app.ico" 0

  CreateShortCut "$SMPROGRAMS\${APPNAME}\浏览器插件安装说明.lnk" "$INSTDIR\浏览器插件-安装说明.txt"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${APPNAME}\卸载 ${APPNAME}.lnk" "$INSTDIR\Uninstall.exe"

  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayName" "${APPNAME} 下载器"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayVersion" "${APPVERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayIcon" "$INSTDIR\app.ico"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "UninstallString" "$\"$INSTDIR\Uninstall.exe$\""
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  DetailPrint "关闭正在运行的 XFlow…"
  nsExec::Exec 'taskkill /IM XFlow.exe /F /T'
  Pop $0
  Sleep 600
  Delete "$INSTDIR\XFlow.exe"
  Delete "$INSTDIR\app.ico"
  Delete "$INSTDIR\浏览器插件-安装说明.txt"
  RMDir /r "$INSTDIR\ext"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APPNAME}\浏览器插件安装说明.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk"
  Delete "$SMPROGRAMS\${APPNAME}\卸载 ${APPNAME}.lnk"
  RMDir "$SMPROGRAMS\${APPNAME}"
  Delete "$DESKTOP\${APPNAME}.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"
SectionEnd
