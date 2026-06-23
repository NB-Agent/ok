@echo off
chcp 65001 >nul
set PATH=D:\Program Files\Go\bin;C:\Users\Administrator\AppData\Roaming\npm;%PATH%

cd /d "%~dp0"

echo === 🤖 一键构建全部：CLI + 桌面端 + 7 个机器人 ===
echo.

set BIN=build\bin
if not exist %BIN% mkdir %BIN%

echo === 🖥  1/9 CLI ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-cli.exe ..\cmd\ok
echo   ✓ ok-cli.exe

echo === 🌐  2/9 前端 ===
cd frontend
call pnpm install >nul
call pnpm build
cd ..
echo   ✓ frontend

echo === 📦  3/9 桌面端 ===
call wails build
echo   ✓ ok.exe

echo === 🤖  4/9 Slack ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-slack-bot.exe ..\cmd\ok-slack-bot
echo   ✓ ok-slack-bot.exe

echo === 🤖  5/9 Discord ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-discord-bot.exe ..\cmd\ok-discord-bot
echo   ✓ ok-discord-bot.exe

echo === 🤖  6/9 Telegram ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-telegram-bot.exe ..\cmd\ok-telegram-bot
echo   ✓ ok-telegram-bot.exe

echo === 🤖  7/9 企业微信 ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-wechat-bot.exe ..\cmd\ok-wechat-bot
echo   ✓ ok-wechat-bot.exe

echo === 🤖  8/9 飞书 ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-feishu-bot.exe ..\cmd\ok-feishu-bot
echo   ✓ ok-feishu-bot.exe

echo === 🤖  9/9 钉钉 + WhatsApp ===
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-dingtalk-bot.exe ..\cmd\ok-dingtalk-bot
echo   ✓ ok-dingtalk-bot.exe
go build -trimpath -ldflags="-s -w" -o %BIN%\ok-whatsapp-bot.exe ..\cmd\ok-whatsapp-bot
echo   ✓ ok-whatsapp-bot.exe

echo.
echo ══════════════════════════════════
echo  🎉  全部构建完成！
echo ══════════════════════════════════
echo.
dir /b %BIN%\*.exe
echo.
pause
