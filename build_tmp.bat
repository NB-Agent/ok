@echo off
cd /d "%~dp0"
go build -trimpath -ldflags "-s -w -X main.version=v1.0.2" -o release\ok-windows-amd64.exe .\cmd\ok
if %errorlevel% equ 0 (
    echo OK: windows-amd64
) else (
    echo FAIL: windows-amd64
    exit /b 1
)
