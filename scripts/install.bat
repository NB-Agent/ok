@echo off
setlocal enabledelayedexpansion
:: ================================================================
:: OK Universal Agent — Windows One-Line Installer
:: Usage: powershell -c "irm https://ok.sh/install.ps1 | iex"
::     or just double-click this .bat file
:: ================================================================
title OK Agent Installer

set "OK_VERSION=latest"
set "OK_REPO=esengine/ok"
set "OK_BIN=%USERPROFILE%\.ok\bin"
set "OK_HOME=%USERPROFILE%\.config\ok"

echo.
echo   ◆ OK Universal Agent Installer
echo   =============================
echo.

:: Check if already installed
where ok.exe >nul 2>&1
if %ERRORLEVEL%==0 (
    echo   ✓ OK is already installed
    ok --version
    echo   Run 'ok' to start.
    pause
    exit /b 0
)

:: Create directories
if not exist "%OK_BIN%" mkdir "%OK_BIN%"
if not exist "%OK_HOME%" mkdir "%OK_HOME%"

:: Detect architecture
set "ARCH=amd64"
if "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"

echo   → Downloading OK for Windows/%ARCH%...

:: Download binary
set "URL=https://github.com/%OK_REPO%/releases/latest/download/ok-windows-%ARCH%.exe"
set "DEST=%OK_BIN%\ok.exe"

powershell -NoProfile -Command ^
    "[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; " ^
    "Invoke-WebRequest -Uri '%URL%' -OutFile '%DEST%' -UseBasicParsing"

if %ERRORLEVEL% NEQ 0 (
    echo   ✗ Download failed. Trying alternative URL...
    set "URL=https://github.com/%OK_REPO%/releases/download/v3.0.0/ok-windows-%ARCH%.exe"
    powershell -NoProfile -Command ^
        "Invoke-WebRequest -Uri '%URL%' -OutFile '%DEST%' -UseBasicParsing"
)

:: Add to PATH
setx PATH "%PATH%;%OK_BIN%" >nul 2>&1
set "PATH=%PATH%;%OK_BIN%"

echo   ✓ Installed to %OK_BIN%\ok.exe
echo.
echo   ◆ Run 'ok' to start the universal agent.
echo   ◆ Run 'ok doctor' to verify installation.
echo   ◆ Run 'ok setup' to configure your API keys.
echo.
echo   Join the community: https://discord.gg/XF78rEME2D
echo.
pause
