//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/getlantern/systray"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	findWindowW    = user32.NewProc("FindWindowW")
	showWindow     = user32.NewProc("ShowWindow")
	registerHotKey = user32.NewProc("RegisterHotKey")
	unregisterHK   = user32.NewProc("UnregisterHotKey")
)

const (
	SW_RESTORE     = 9
	SW_SHOW        = 5
	SW_HIDE        = 0
	SW_MINIMIZE    = 6
	MOD_ALT        = 0x0001
	MOD_CONTROL    = 0x0002
	MOD_NOREPEAT   = 0x4000
	HOTKEY_ID_SHOW = 1 // Ctrl+Alt+O → show OK window
	VK_O           = 0x4F
)

// startTray launches the system tray in a goroutine with a locked OS thread.
// It blocks until the tray is ready or fails.
func startTray(ctx context.Context, app *App) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "tray goroutine panic: %v\n", r)
			}
		}()
		runtime.LockOSThread()
		systray.Run(
			func() { onTrayReady(ctx, app) },
			func() { onTrayExit() },
		)
	}()
}

func onTrayReady(ctx context.Context, app *App) {
	// Use the app icon. Look relative to the executable directory first
	// (handles installed scenarios where CWD may differ from install dir),
	// then fall back to relative path for development builds.
	iconBytes := readIconFile()
	if iconBytes != nil {
		systray.SetIcon(iconBytes)
	}
	systray.SetTitle("OK Agent")
	systray.SetTooltip("OK — Universal Agent")

	// —— Menu items ——
	mShow := systray.AddMenuItem("Show OK", "Bring OK window to front")
	systray.AddSeparator()
	mStartup := systray.AddMenuItem("Run at startup", "Launch OK automatically when Windows starts")
	if isStartupEnabled() {
		mStartup.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit OK completely")

	// Register global hotkey: Ctrl+Alt+O
	registerShowHotkey()

	// —— Event loop (single goroutine, single select) ——
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "tray event loop panic: %v\n", r)
			}
		}()
		for {
			select {
			case <-mShow.ClickedCh:
				bringOKToFront()

			case <-mQuit.ClickedCh:
				unregisterShowHotkey()
				if app.ctrl != nil {
					if err := app.ctrl.Snapshot(); err != nil {
						fmt.Fprintf(os.Stderr, "tray: shutdown snapshot: %v\n", err)
					}
					app.ctrl.Close()
				}
				systray.Quit()
				os.Exit(0)

			case <-mStartup.ClickedCh:
				if mStartup.Checked() {
					mStartup.Uncheck()
					disableStartup()
				} else {
					mStartup.Check()
					enableStartup()
				}

			case e := <-app.windowEvents:
				switch e {
				case "minimized", "closed":
					if hwnd := findOKWindow(); hwnd != 0 {
						showWindow.Call(hwnd, SW_HIDE)
					}
				}
			}
		}
	}()
}

// startupDir returns the Windows Startup folder path.
func startupDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

// enableStartup creates a shortcut in the Windows Startup folder.
func enableStartup() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := startupDir()
	if dir == "" {
		return
	}
	// Create a .bat launcher (simpler than a .lnk — no COM needed).
	// Escape special characters that would break batch parsing.
	batPath := filepath.Join(dir, "OK_Agent.bat")
	escapedExe := escapeBatString(exe)
	content := "@echo off\r\nstart \"\" \"" + escapedExe + "\"\r\n"
	_ = os.WriteFile(batPath, []byte(content), 0o644)
}

// disableStartup removes the startup shortcut.
func disableStartup() {
	dir := startupDir()
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, "OK_Agent.bat"))
	_ = os.Remove(filepath.Join(dir, "OK_Agent.lnk"))
}

// isStartupEnabled checks if the startup shortcut exists.
func isStartupEnabled() bool {
	dir := startupDir()
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "OK_Agent.bat")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "OK_Agent.lnk")); err == nil {
		return true
	}
	return false
}

func registerShowHotkey() bool {
	hwnd := findOKWindow()
	if hwnd == 0 {
		return false
	}
	ret, _, _ := registerHotKey.Call(hwnd, HOTKEY_ID_SHOW, MOD_CONTROL|MOD_ALT|MOD_NOREPEAT, VK_O)
	return ret != 0
}

func unregisterShowHotkey() {
	hwnd := findOKWindow()
	if hwnd != 0 {
		unregisterHK.Call(hwnd, HOTKEY_ID_SHOW)
	}
}

func findOKWindow() uintptr {
	// Use our custom window class name set in main.go.
	className := syscall.StringToUTF16Ptr("OK_Agent_Window")
	hwnd, _, _ := findWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd != 0 {
		return hwnd
	}
	// Fallback: try by title
	title := syscall.StringToUTF16Ptr("OK")
	hwnd, _, _ = findWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	return hwnd
}

func bringOKToFront() {
	hwnd := findOKWindow()
	if hwnd == 0 {
		return
	}
	showWindow.Call(hwnd, SW_RESTORE)
	showWindow.Call(hwnd, SW_SHOW)
	// Bring to foreground
	user32.NewProc("SetForegroundWindow").Call(hwnd)
}

func onTrayExit() {
	// Cleanup
}

// readIconFile looks for the app icon next to the executable, then in build/.
func readIconFile() []byte {
	dir := ""
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	candidates := []string{
		filepath.Join(dir, "appicon.png"),
		filepath.Join(dir, "icon.ico"),
		"build/appicon.png",
		"build/windows/icon.ico",
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}

// escapeBatString escapes characters special to Windows batch files.
func escapeBatString(s string) string {
	s = strings.ReplaceAll(s, "&", "^&")
	s = strings.ReplaceAll(s, "|", "^|")
	s = strings.ReplaceAll(s, "<", "^<")
	s = strings.ReplaceAll(s, ">", "^>")
	s = strings.ReplaceAll(s, "%", "%%")
	return s
}
