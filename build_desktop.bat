@echo off
cd /d "%~dp0"
echo === Rebuilding desktop binary ===
cd desktop
wails build
if %errorlevel% equ 0 (
    echo OK: desktop build succeeded
    echo Copying to release...
    copy build\bin\ok.exe ..\release\ok-windows-desktop.exe
    echo Done
) else (
    echo FAIL: desktop build
    exit /b 1
)
