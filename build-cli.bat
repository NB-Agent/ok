@echo off
chcp 65001 >nul
set PATH=D:\Program Files\Go\bin;%PATH%
cd /d "%~dp0"
echo === building CLI binary ===
go build -trimpath -ldflags="-s -w" -o bin\ok.exe .\cmd\ok
if %ERRORLEVEL% neq 0 (
    echo BUILD FAILED
    pause
    exit /b 1
)
echo === DONE ===
echo.
echo Binary: bin\ok.exe  (%date% %time%)
echo.
echo Try: bin\ok.exe chat
pause
