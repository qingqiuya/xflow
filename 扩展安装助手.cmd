@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion
title 汐流 · 装浏览器接管扩展

set "EXTDIR=%~dp0ext"
if not exist "%EXTDIR%\manifest.json" (
  echo [错误] 没找到扩展目录：%EXTDIR%
  echo 请确认这个脚本和 ext 文件夹在同一个安装目录里。
  pause
  exit /b 1
)

echo ==========================================================
echo   汐流 XFlow · 浏览器接管扩展
echo ==========================================================
echo.
echo 扩展目录：%EXTDIR%
echo.
echo 接下来会做三件事：
echo   1. 在资源管理器里打开扩展目录（供下一步选择）
echo   2. 打开浏览器的扩展管理页
echo   3. 你在那边照着下面三步点一下就好
echo.
echo   ① 打开「开发者模式」开关
echo   ② 点「加载已解压的扩展程序」
echo   ③ 选中上面那个 ext 文件夹（直接粘贴路径进去也行）
echo.
echo 说明：浏览器从 2025 年起禁止第三方软件静默装扩展，
echo       也不接受商店外下载的 crx，所以最后这一步必须你点。
echo.
pause

start "" "%EXTDIR%"

set "OPENED="
if exist "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" (
  start "" "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" --new-window "edge://extensions"
  set "OPENED=1"
)
if not defined OPENED if exist "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" (
  start "" "%ProgramFiles%\Microsoft\Edge\Application\msedge.exe" --new-window "edge://extensions"
  set "OPENED=1"
)
if not defined OPENED if exist "%ProgramFiles%\Google\Chrome\Application\chrome.exe" (
  start "" "%ProgramFiles%\Google\Chrome\Application\chrome.exe" --new-window "chrome://extensions"
  set "OPENED=1"
)
if not defined OPENED if exist "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" (
  start "" "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" --new-window "chrome://extensions"
  set "OPENED=1"
)
if not defined OPENED (
  echo.
  echo 没找到 Edge / Chrome，请自己打开浏览器，地址栏输入 edge://extensions 或 chrome://extensions
)

echo.
echo 装好之后：地址栏右边会出现汐流图标，点开显示「已连接」就对了。
echo.
pause
