@echo off
chcp 65001 >nul
set PATH=D:\Program Files\Go\bin;C:\Users\Administrator\AppData\Roaming\npm;%PATH%

cd /d "%~dp0"

echo === step 0: get version ===
for /f %%a in ('git describe --tags --always 2^>nul') do set VERSION=%%a
if not defined VERSION set VERSION=dev
echo Version: %VERSION%

set BIN=desktop\build\bin
if not exist %BIN% mkdir %BIN%

echo === step 1: generate icons ===
go run scripts/genicon.go
if %ERRORLEVEL% neq 0 (
    echo ICON GENERATION FAILED
    pause
    exit /b 1
)

echo === step 2: build CLI ===
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %BIN%\ok-cli.exe .\cmd\ok
if %ERRORLEVEL% neq 0 (
    echo CLI BUILD FAILED
    pause
    exit /b 1
)
echo [OK] ok-cli.exe

echo === step 3: build desktop frontend ===
cd /d "%~dp0desktop\frontend"
call pnpm install >nul
if %ERRORLEVEL% neq 0 (
    echo PNPM INSTALL FAILED
    pause
    exit /b 1
)
call pnpm build
if %ERRORLEVEL% neq 0 (
    echo PNPM BUILD FAILED
    pause
    exit /b 1
)

echo === step 4: build desktop app ===
cd /d "%~dp0desktop"
call wails build
if %ERRORLEVEL% neq 0 (
    echo WAILS BUILD FAILED
    pause
    exit /b 1
)
echo [OK] ok.exe

cd /d "%~dp0"

echo.
echo ========= BUILD COMPLETE =========
echo.
echo Output: %BIN%\
echo.
dir /b %BIN%\*.exe 2>nul
echo.
echo  CLI:        ok-cli.exe
echo  Desktop:    ok.exe
echo.
echo === optional: build with tree-sitter (requires gcc) ===
echo   set CGO_ENABLED=1 ^&^& go build -tags=treesitter -trimpath -ldflags="-s -w" -o %BIN%\ok-ts.exe .\cmd\ok
pause
