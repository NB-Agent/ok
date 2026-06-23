@echo off
chcp 65001 >nul
set PATH=D:\Program Files\Go\bin;C:\Users\Administrator\AppData\Roaming\npm;%PATH%
cd /d "%~dp0"

echo === step 0: get version ===
for /f %%a in ('git describe --tags --always 2^>nul') do set VERSION=%%a
if not defined VERSION set VERSION=dev
echo Version: %VERSION%

set OUT=release
if not exist %OUT% mkdir %OUT%

echo === step 1: generate icons ===
go run scripts/genicon.go
if %ERRORLEVEL% neq 0 ( echo ICON FAILED & pause & exit /b 1 )

echo.
echo === step 2: build CLI x 6 platforms ===
echo   [1/6] linux/amd64
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-linux-amd64 .\cmd\ok
if %ERRORLEVEL% neq 0 ( echo FAILED & pause & exit /b 1 )

echo   [2/6] linux/arm64
set GOARCH=arm64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-linux-arm64 .\cmd\ok

echo   [3/6] darwin/amd64
set GOOS=darwin
set GOARCH=amd64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-darwin-amd64 .\cmd\ok

echo   [4/6] darwin/arm64
set GOARCH=arm64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-darwin-arm64 .\cmd\ok

echo   [5/6] windows/amd64
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-windows-amd64.exe .\cmd\ok

echo   [6/6] windows/arm64
set GOARCH=arm64
go build -trimpath -ldflags="-s -w -X main.version=%VERSION%" -o %OUT%\ok-windows-arm64.exe .\cmd\ok

echo   CLI: 6/6 OK

echo.
echo === step 3: build desktop frontend ===
cd /d "%~dp0desktop\frontend"
call pnpm install >nul
call pnpm build
if %ERRORLEVEL% neq 0 ( echo FRONTEND FAILED & pause & exit /b 1 )

echo.
echo === step 4: build desktop (Windows) ===
cd /d "%~dp0desktop"
set CGO_ENABLED=1
set GOOS=windows
set GOARCH=amd64
call wails build -trimpath
if %ERRORLEVEL% neq 0 ( echo DESKTOP FAILED & pause & exit /b 1 )
copy /y build\bin\ok.exe "%~dp0%OUT%\ok-windows-desktop.exe" >nul

cd /d "%~dp0"

echo.
echo ===================== BUILD COMPLETE =====================
echo.
echo Output: %OUT%\
echo.
dir /b %OUT%\*
echo.
echo   6 CLI  + 1 Desktop (Windows)  = 7 binaries
echo.
echo   macOS/Linux desktop: build on native OS with wails build
echo.
pause
