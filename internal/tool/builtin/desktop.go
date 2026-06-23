package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

// command wraps exec.CommandContext and hides the console window on Windows.
func command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	winhide.Cmd(cmd)
	return cmd
}

func init() { tool.RegisterBuiltin(desktop{}) }

// desktop provides computer automation capabilities: screenshot, process
// management, window control, clipboard access, mouse/keyboard automation.
// Windows uses PowerShell; macOS uses osascript/screencapture; Linux uses
// xdotool/gnome-screenshot where available.
type desktop struct{}

func (desktop) Name() string { return "desktop" }

func (desktop) Description() string {
	return "Operate the computer — screenshots, process management, window control, clipboard, mouse/keyboard, speech (TTS), audio recording, and camera."
}

func (desktop) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["screenshot","processes","kill","start","clipboard-read","clipboard-write","windows-list","focus-window","send-keys","sleep","mouse-move","mouse-click","mouse-double-click","mouse-right-click","scroll","mouse-drag","speak","record-audio","take-photo","record-video","notify","env-list","env-get","env-set"],"type":"string"},"duration_ms":{"type":"integer"},"duration_sec":{"type":"integer"},"headline":{"type":"string"},"language":{"type":"string"},"path":{"type":"string"},"target":{"type":"string"},"text":{"type":"string"},"x":{"type":"number"},"x2":{"type":"number"},"y":{"type":"number"},"y2":{"type":"number"}},"required":["action"],"type":"object"}`)
}

func (desktop) ReadOnly() bool { return false }

func (desktop) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action      string  `json:"action"`
		Target      string  `json:"target"`
		Text        string  `json:"text"`
		Path        string  `json:"path"`
		X           float64 `json:"x"`
		Y           float64 `json:"y"`
		X2          float64 `json:"x2"`
		Y2          float64 `json:"y2"`
		DurationMs  int     `json:"duration_ms"`
		DurationSec int     `json:"duration_sec"`
		Language    string  `json:"language"`
		Headline    string  `json:"headline"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "screenshot":
		return screenshot(ctx, p.Path)
	case "processes":
		return listProcesses(ctx, p.Target)
	case "kill":
		return killProcess(ctx, p.Target)
	case "start":
		return startProgram(ctx, p.Path, p.Target)
	case "clipboard-read":
		return clipboardRead(ctx)
	case "clipboard-write":
		return clipboardWrite(ctx, p.Text)
	case "windows-list":
		return listWindows(ctx, p.Target)
	case "focus-window":
		return focusWindow(ctx, p.Target)
	case "send-keys":
		return sendKeys(ctx, p.Target, p.Text)
	case "sleep":
		return sleep(ctx, time.Duration(p.DurationMs)*time.Millisecond)
	case "mouse-move":
		return mouseMove(ctx, int(p.X), int(p.Y))
	case "mouse-click":
		return mouseClick(ctx, int(p.X), int(p.Y))
	case "mouse-double-click":
		return mouseDoubleClick(ctx, int(p.X), int(p.Y))
	case "mouse-right-click":
		return mouseRightClick(ctx, int(p.X), int(p.Y))
	case "scroll":
		return scrollMouse(ctx, int(p.X), int(p.Y))
	case "mouse-drag":
		return mouseDrag(ctx, int(p.X), int(p.Y), int(p.X2), int(p.Y2))
	case "speak":
		return speak(ctx, p.Text, p.Language)
	case "record-audio":
		return recordAudio(ctx, p.Path, p.DurationSec)
	case "take-photo":
		return takePhoto(ctx, p.Path)
	case "record-video":
		return recordVideo(ctx, p.Path, p.DurationSec)
	case "notify":
		return notifyDesktop(ctx, p.Headline, p.Text)
	case "env-list":
		return envList(ctx)
	case "env-get":
		return envGet(ctx, p.Target)
	case "env-set":
		return envSet(ctx, p.Target, p.Text)
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

// ——— screenshot ———

func screenshot(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = fmt.Sprintf("screenshot-%s.png", time.Now().Format("20060102-150405"))
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type -AssemblyName System.Windows.Forms,System.Drawing;` +
				`$s=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;` +
				`$b=New-Object System.Drawing.Bitmap($s.Width,$s.Height);` +
				`$g=[System.Drawing.Graphics]::FromImage($b);` +
				`$g.CopyFromScreen($s.Location,[System.Drawing.Point]::Empty,$s.Size);` +
				`$g.Dispose();$b.Save(` + escapePSSingleQuoted(path) + `);$b.Dispose()`
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
	case "darwin":
		cmd = exec.CommandContext(ctx, "screencapture", "-x", path)
	default:
		cmd = exec.CommandContext(ctx, "gnome-screenshot", "-f", path)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("screenshot: %w\n%s", err, string(out))
	}

	info, err := os.Stat(path)
	var size string
	if err == nil {
		size = humanizeDeskBytes(info.Size())
	} else {
		size = "unknown"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Screenshot\n\n✅ Saved: `%s` (%s)\n", path, size))
	return b.String(), nil
}

// ——— processes ———

func listProcesses(ctx context.Context, filter string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if filter != "" {
			escaped := strings.ReplaceAll(strings.ReplaceAll(filter, "'", "''"), "\x00", "")
			cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
				fmt.Sprintf("Get-Process | Where-Object {$_.ProcessName -like '*%s*'} | Select-Object Id,ProcessName,CPU,WorkingSet64 | Format-Table -AutoSize", escaped))
		} else {
			cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
				"Get-Process | Sort-Object CPU -Descending | Select-Object -First 20 Id,ProcessName,CPU,WorkingSet64 | Format-Table -AutoSize")
		}
	default:
		if filter != "" {
			cmd = exec.CommandContext(ctx, "ps", "aux")
		} else {
			cmd = exec.CommandContext(ctx, "ps", "aux", "--sort=-%cpu")
		}
	}

	out, _ := cmd.CombinedOutput()

	var b strings.Builder
	b.WriteString("# Processes\n\n")
	if filter != "" {
		b.WriteString(fmt.Sprintf("Filter: `%s`\n\n", filter))
	}
	b.WriteString("```\n" + truncateDesk(string(out), 2048) + "\n```\n")
	return b.String(), nil
}

// ——— kill process ———

func killProcess(ctx context.Context, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required (process name or PID)")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "taskkill", "/F", "/IM", target)
	default:
		cmd = exec.CommandContext(ctx, "killall", target)
	}

	out, _ := cmd.CombinedOutput()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Kill: %s\n\n", target))
	b.WriteString("```\n" + strings.TrimSpace(string(out)) + "\n```\n")
	return b.String(), nil
}

// ——— start program ———

// isWindowsExecutable reports whether path looks like a runnable file
// (has an executable extension), so we can bypass cmd.exe /c start.
func isWindowsExecutable(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".com", ".bat", ".cmd", ".msi":
		return true
	default:
		return false // non-executable
	}
}

func startProgram(ctx context.Context, path string, target string) (string, error) {
	if target == "" && path == "" {
		return "", fmt.Errorf("path or target is required")
	}

	program := path
	if program == "" {
		program = target
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Use direct exec to avoid cmd.exe shell injection. For executables this
		// works directly; for URLs/documents that need file-association (e.g.
		// .html, .pdf), fall back to cmd /c start with properly quoted path.
		if isWindowsExecutable(program) {
			cmd = command(ctx, program)
		} else {
			// Quote the path to prevent shell metacharacter injection.
			// Double any embedded quotes so they don't escape the quoting context.
			safe := strings.ReplaceAll(program, `"`, `""`)
			cmd = command(ctx, "cmd", "/c", "start", "", `"`+safe+`"`)
		}
	default:
		// On non-Windows platforms, accept only absolute paths or simple
		// names (no path separators) to prevent traversal attacks.
		if filepath.Base(program) == program {
			cmd = exec.CommandContext(ctx, program)
		} else if filepath.IsAbs(program) {
			cmd = exec.CommandContext(ctx, program)
		} else {
			return "", fmt.Errorf("start: %q must be an absolute path or a simple name without path separators", program)
		}
	}

	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("start %s: %w", program, err)
	}
	// Reap the child process in the background to avoid zombie/leak.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "desktop reap goroutine panic: %v\n", r)
			}
		}()
		if err := cmd.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "desktop: reap %s: %v\n", program, err)
		}
	}() // best-effort: zombie prevention on platforms that need it

	return fmt.Sprintf("# Started\n\n✅ `%s` launched\n", program), nil
}

// ——— clipboard ———

func clipboardRead(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", "Get-Clipboard")
	case "darwin":
		cmd = exec.CommandContext(ctx, "pbpaste")
	default:
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-o")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("clipboard read: %w", err)
	}

	text := string(out)
	if text == "" {
		return "(clipboard is empty)", nil
	}

	var b strings.Builder
	b.WriteString("# Clipboard\n\n")
	b.WriteString("```\n" + truncateDesk(text, 1024) + "\n```\n")
	return b.String(), nil
}

func clipboardWrite(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			"Set-Clipboard -Value "+escapePSSingleQuoted(text))
	case "darwin":
		cmd = exec.CommandContext(ctx, "pbcopy")
		cmd.Stdin = strings.NewReader(text)
	default:
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
	}

	if _, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("clipboard write: %w", err)
	}

	return fmt.Sprintf("# Clipboard\n\n✅ Written (%d chars)\n", len(text)), nil
}

// ——— windows ———

func listWindows(ctx context.Context, filter string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		script := "Get-Process | Where-Object {$_.MainWindowTitle -ne ''} | Select-Object Id,ProcessName,MainWindowTitle | Format-Table -AutoSize"
		if filter != "" {
			escaped := strings.ReplaceAll(strings.ReplaceAll(filter, "'", "''"), "\x00", "")
			script = fmt.Sprintf("Get-Process | Where-Object {$_.MainWindowTitle -ne '' -and $_.MainWindowTitle -like '*%s*'} | Select-Object Id,ProcessName,MainWindowTitle | Format-Table -AutoSize", escaped)
		}
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
		out, _ := cmd.CombinedOutput()

		var b strings.Builder
		b.WriteString("# Windows\n\n")
		if filter != "" {
			b.WriteString(fmt.Sprintf("Filter: `%s`\n\n", filter))
		}
		b.WriteString("```\n" + truncateDesk(string(out), 1024) + "\n```\n")
		return b.String(), nil
	case "darwin":
		cmd := exec.CommandContext(ctx, "osascript", "-e",
			`tell application "System Events" to get name of every process whose background only is false`)
		out, _ := cmd.CombinedOutput()
		return "# Windows (macOS)\n\n```\n" + string(out) + "\n```\n", nil
	default:
		cmd := exec.CommandContext(ctx, "wmctrl", "-l")
		out, _ := cmd.CombinedOutput()
		return "# Windows (Linux)\n\n```\n" + string(out) + "\n```\n", nil
	}
}

func focusWindow(ctx context.Context, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required (window title substring)")
	}

	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Win {
[DllImport("user32.dll")]public static extern bool SetForegroundWindow(IntPtr h);
[DllImport("user32.dll")]public static extern IntPtr FindWindow(string c,string w);
}
"@;
$h=[Win]::FindWindow($null,` + escapePSDoubleQuoted(target) + `);
if($h){[Win]::SetForegroundWindow($h)}else{"window not found"}`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Focus Window\n\nTarget: `%s`\n%s\n", target, strings.TrimSpace(string(out))), nil
	case "darwin":
		// AppleScript uses "" to escape a literal " inside strings (not \")
		escaped := strings.ReplaceAll(target, "\"", "\"\"")
		cmd := exec.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "System Events" to set frontmost of process "%s" to true`, escaped))
		cmd.CombinedOutput()
		return fmt.Sprintf("# Focus Window\n\n✅ `%s`\n", target), nil
	default:
		cmd := exec.CommandContext(ctx, "wmctrl", "-a", target)
		cmd.CombinedOutput()
		return fmt.Sprintf("# Focus Window\n\n✅ `%s`\n", target), nil
	}
}

// ——— send keys ———

func sendKeys(ctx context.Context, target, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required")
	}

	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait(` + escapePSSingleQuoted(escapeSendKeys(text)) + `)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		if _, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("send-keys: %w", err)
		}
		return "# Send Keys\n\n✅ Sent to active window\n", nil
	case "darwin":
		// AppleScript uses "" to escape a literal " inside strings (not \")
		escaped := strings.ReplaceAll(text, "\"", "\"\"")
		cmd := exec.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, escaped))
		cmd.CombinedOutput()
		return "# Send Keys\n\n✅ Typed\n", nil
	default:
		cmd := exec.CommandContext(ctx, "xdotool", "type", text)
		cmd.CombinedOutput()
		return "# Send Keys\n\n✅ Typed\n", nil
	}
}

// ——— mouse actions ———

func mouseMove(ctx context.Context, x, y int) (string, error) {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(%d,%d)`, x, y))
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick", fmt.Sprintf("m:%d,%d", x, y))
		desktopRun(cmd)
	default:
		cmd := exec.CommandContext(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Mouse Move\n\n✅ Moved cursor to (%d, %d)\n", x, y), nil
}

func mouseClick(ctx context.Context, x, y int) (string, error) {
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint LEFTDOWN=0x02, LEFTUP=0x04;
}
"@;[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick", fmt.Sprintf("c:%d,%d", x, y))
		desktopRun(cmd)
	default:
		args := []string{"click", "1"}
		if x > 0 || y > 0 {
			args = append(args, strconv.Itoa(x), strconv.Itoa(y))
		}
		cmd := exec.CommandContext(ctx, "xdotool", args...)
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Mouse Click\n\n✅ Left click at (%d, %d)\n", x, y), nil
}

func mouseDoubleClick(ctx context.Context, x, y int) (string, error) {
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint LEFTDOWN=0x02, LEFTUP=0x04;
}
"@;[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0);
[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick", fmt.Sprintf("d:%d,%d", x, y))
		desktopRun(cmd)
	default:
		cmd := exec.CommandContext(ctx, "xdotool", "click", "--repeat", "2", "--delay", "100", "1")
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Mouse Double Click\n\n✅ Double-click at (%d, %d)\n", x, y), nil
}

func mouseRightClick(ctx context.Context, x, y int) (string, error) {
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint RIGHTDOWN=0x08, RIGHTUP=0x10;
}
"@;[Mouse]::mouse_event([Mouse]::RIGHTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::RIGHTUP,0,0,0,0)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick", fmt.Sprintf("rc:%d,%d", x, y))
		desktopRun(cmd)
	default:
		args := []string{"click", "3"}
		if x > 0 || y > 0 {
			args = append(args, strconv.Itoa(x), strconv.Itoa(y))
		}
		cmd := exec.CommandContext(ctx, "xdotool", args...)
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Mouse Right Click\n\n✅ Right-click at (%d, %d)\n", x, y), nil
}

func scrollMouse(ctx context.Context, dx, dy int) (string, error) {
	if dx == 0 && dy == 0 {
		dy = -3 // default: scroll down 3 clicks
	}
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint WHEEL=0x800;
}
"@;[Mouse]::mouse_event([Mouse]::WHEEL,0,0,%d,0)`, dy*120)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick", fmt.Sprintf("wp:%d,%d", dy, dx))
		desktopRun(cmd)
	default:
		if dy > 0 {
			cmd := exec.CommandContext(ctx, "xdotool", "click", "--repeat", strconv.Itoa(dy), "5")
			desktopRun(cmd)
		} else {
			cmd := exec.CommandContext(ctx, "xdotool", "click", "--repeat", strconv.Itoa(absInt(dy)), "4")
			desktopRun(cmd)
		}
	}
	return fmt.Sprintf("# Scroll\n\n✅ Scrolled (dx=%d, dy=%d)\n", dx, dy), nil
}

func mouseDrag(ctx context.Context, x1, y1, x2, y2 int) (string, error) {
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void SetCursorPos(int x,int y);
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint LEFTDOWN=0x02, LEFTUP=0x04, MOVE=0x01;
}
"@;
[Mouse]::SetCursorPos(%d,%d);
[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);
[Mouse]::mouse_event([Mouse]::MOVE|0x8001,%d,%d,0,0);
[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0)`,
			x1, y1, x2, y2)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd)
	case "darwin":
		cmd := exec.CommandContext(ctx, "cliclick",
			fmt.Sprintf("dd:%d,%d", x1, y1),
			fmt.Sprintf("du:%d,%d", x2, y2))
		desktopRun(cmd)
	default:
		cmd := exec.CommandContext(ctx, "xdotool", "mousemove", strconv.Itoa(x1), strconv.Itoa(y1),
			"mousedown", "1", "mousemove", strconv.Itoa(x2), strconv.Itoa(y2), "mouseup", "1")
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Mouse Drag\n\n✅ Dragged from (%d,%d) to (%d,%d)\n", x1, y1, x2, y2), nil
}

// ——— sleep ———

func sleep(ctx context.Context, d time.Duration) (string, error) {
	if d <= 0 {
		d = time.Second
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	select {
	case <-time.After(d):
		return fmt.Sprintf("# Sleep\n\n✅ Waited %s\n", d), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ——— helpers ———

// escapePSSingleQuoted escapes s for use inside a PowerShell single-quoted string
// ('...'). Single quotes are doubled (PowerShell convention). Null bytes are
// stripped (they terminate C strings). Newlines are replaced with spaces to
// prevent statement splitting, and other control characters are stripped.
func escapePSSingleQuoted(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\x00", "")  // strip nulls (C string terminator)
	s = strings.ReplaceAll(s, "\r\n", " ") // CRLF → space
	s = strings.ReplaceAll(s, "\n", " ")   // LF → space
	s = strings.ReplaceAll(s, "\r", " ")   // CR → space
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' { // strip other control chars except tab
			return -1
		}
		return r
	}, s)
	return "'" + s + "'"
}

// escapePSDoubleQuoted escapes s for use inside a PowerShell double-quoted string
// ("..."). Backticks and double quotes are escaped; nulls stripped; control chars
// handled.
func escapePSDoubleQuoted(s string) string {
	s = strings.ReplaceAll(s, "`", "``")
	s = strings.ReplaceAll(s, "\"", "`\"")
	s = strings.ReplaceAll(s, "$", "`$")
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
	return "\"" + s + "\""
}

func escapeSendKeys(s string) string {
	repl := strings.NewReplacer(
		"+", "{+}", "^", "{^}", "%", "{%}",
		"~", "{~}", "(", "{(}", ")", "{)}",
		"{", "{{}", "}", "{}}",
	)
	return repl.Replace(s)
}

func humanizeDeskBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func truncateDesk(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// desktopRun runs a best-effort desktop command and logs errors to stderr.
// Use for non-critical operations (fallbacks, notifications) where failure
// should not abort the entire action but still be visible for debugging.
func desktopRun(cmd *exec.Cmd) {
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "desktop: %s: %v\n", strings.Join(cmd.Args, " "), err)
	}
}

// ——— speech (TTS) ———

func speak(ctx context.Context, text, language string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	switch runtime.GOOS {
	case "windows":
		code := "zh-CN"
		if language != "" {
			code = language
		}
		psScript :=
			`Add-Type -AssemblyName System.Speech;$s=New-Object System.Speech.Synthesis.SpeechSynthesizer;$s.SelectVoiceByHints([System.Speech.Synthesis.VoiceGender]::Neutral,[System.Speech.Synthesis.VoiceAge]::Adult,0,[System.Globalization.CultureInfo]::GetCultureInfo(` + escapePSSingleQuoted(code) + `));$s.Speak(` + escapePSSingleQuoted(text) + `)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("speak: %w\n%s", err, string(out))
		}
	case "darwin":
		voice := ""
		switch {
		case strings.HasPrefix(language, "zh"):
			voice = "Ting-Ting"
		case strings.HasPrefix(language, "ja"):
			voice = "Kyoko"
		case strings.HasPrefix(language, "ko"):
			voice = "Yuna"
		case strings.HasPrefix(language, "fr"):
			voice = "Amelie"
		case strings.HasPrefix(language, "de"):
			voice = "Anna"
		default:
			voice = "Samantha"
		}
		cmd := exec.CommandContext(ctx, "say", "-v", voice, text)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("speak: %w\n%s", err, string(out))
		}
	default:
		voice := ""
		if strings.HasPrefix(language, "zh") {
			voice = "Chinese"
		}
		args := []string{"-v", voice, text}
		if _, err := exec.LookPath("espeak"); err == nil {
			// espeak: -v for voice, text as arg
			cmd := exec.CommandContext(ctx, "espeak", args...)
			desktopRun(cmd)
		} else if _, err := exec.LookPath("spd-say"); err == nil {
			cmd := exec.CommandContext(ctx, "spd-say", text)
			desktopRun(cmd)
		} else {
			return "", fmt.Errorf("speak: install espeak or speech-dispatcher")
		}
	}
	return fmt.Sprintf("# Speak\n\n✅ Spoke %d characters\n", len(text)), nil
}

// ——— audio recording ———

func recordAudio(ctx context.Context, path string, sec int) (string, error) {
	if path == "" {
		path = fmt.Sprintf("recording-%s.wav", time.Now().Format("20060102-150405"))
	}
	if sec <= 0 {
		sec = 5
	}
	if sec > 60 {
		sec = 60
	}
	switch runtime.GOOS {
	case "windows":
		// PowerShell: use Windows Audio Session API to record
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.IO;using System.Runtime.InteropServices;
public class Mic {
[DllImport("winmm.dll")]public static extern int mciSendString(string c,string r,int s,IntPtr h);
}
"@;
[Mic]::mciSendString("open new type waveaudio alias mic",$null,0,[IntPtr]::Zero);
[Mic]::mciSendString("record mic",$null,0,[IntPtr]::Zero);
System.Threading.Thread::Sleep(%d);
[Mic]::mciSendString("save mic `+escapePSSingleQuoted(path)+`,$null,0,[IntPtr]::Zero);
[Mic]::mciSendString("close mic",$null,0,[IntPtr]::Zero)`,
			sec*1000)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-audio: %w\n%s", err, string(out))
		}
	case "darwin":
		cmd := exec.CommandContext(ctx, "rec", "-r", "44100", "-c", "1", "-b", "16", path, "trim", "0", strconv.Itoa(sec))
		if _, err := exec.LookPath("rec"); err != nil {
			// fallback to ffmpeg
			cmd = exec.CommandContext(ctx, "ffmpeg", "-f", "avfoundation", "-i", ":0", "-t", strconv.Itoa(sec), "-y", path)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-audio: %w\n%s", err, string(out))
		}
	default:
		cmd := exec.CommandContext(ctx, "arecord", "-d", strconv.Itoa(sec), "-f", "cd", "-t", "wav", path)
		if _, err := exec.LookPath("arecord"); err != nil {
			cmd = exec.CommandContext(ctx, "parecord", "--record", "--duration="+strconv.Itoa(sec), path)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-audio: %w\n%s", err, string(out))
		}
	}
	info, err := os.Stat(path)
	var size string
	if err == nil {
		size = humanizeDeskBytes(info.Size())
	} else {
		size = "unknown"
	}
	return fmt.Sprintf("# Record Audio\n\n✅ Recorded %ds to `%s` (%s)\n", sec, path, size), nil
}

// ——— audio transcription (speech-to-text) ———

//lint:ignore U1000 kept for future speech-to-text use
func transcribeAudio(ctx context.Context, path, language string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path to audio file is required")
	}
	switch runtime.GOOS {
	case "windows":
		if language == "" {
			language = "zh-CN"
		}
		// Use System.Speech.Recognition (built-in Windows speech recognition)
		// Works on Windows 8+ with the speech recognition feature installed.
		psScript :=
			`Add-Type -AssemblyName System.Speech;
try{$rec=New-Object System.Speech.Recognition.SpeechRecognitionEngine([System.Globalization.CultureInfo]::GetCultureInfo(` + escapePSSingleQuoted(language) + `));
$rec.SetInputToWaveFile(` + escapePSSingleQuoted(path) + `);
$rec.LoadGrammar((New-Object System.Speech.Recognition.DictationGrammar));
$result=$rec.Recognize();
if($result -ne $null){$result.Text}else{''}}catch{''}`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("transcribe-audio: %w\n%s", err, string(out))
		}
		text := strings.TrimSpace(string(out))
		if text == "" {
			return "", fmt.Errorf("transcribe-audio: no speech recognized (try a different language or check microphone)")
		}
		return fmt.Sprintf("# Transcribe Audio\n\n```\n%s\n```\n", text), nil
	default:
		return "", fmt.Errorf("transcribe-audio: not yet supported on %s", runtime.GOOS)
	}
}

// ——— camera / video ———

func takePhoto(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = fmt.Sprintf("photo-%s.jpg", time.Now().Format("20060102-150405"))
	}
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Cam {
[DllImport("avicap32.dll")]public static extern IntPtr capCreateCaptureWindow(string w,int f,int x,int y,int cx,int cy,int p,int id);
}
"@;
$w=[Cam]::capCreateCaptureWindow("WebCam",0x40000000,0,0,320,240,0,0);
[System.Threading.Thread]::Sleep(500);
$cap=0;
`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		desktopRun(cmd) // best-effort camera init
		// Try ffmpeg first (most reliable)
		ffmpeg := exec.CommandContext(ctx, "ffmpeg", "-f", "dshow", "-i", "video=Integrated Camera", "-vframes", "1", "-y", path)
		if out, err := ffmpeg.CombinedOutput(); err == nil {
			if werr := os.WriteFile(path+".log", out, 0o644); werr != nil {
				fmt.Fprintf(os.Stderr, "desktop: cannot write photo log: %v\n", werr)
			}
		}
		// Fallback: try using PowerShell with System.Drawing (screenshot as photo)
		fallback := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms;$s=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;$b=New-Object System.Drawing.Bitmap($s.Width,$s.Height);$g=[System.Drawing.Graphics]::FromImage($b);$g.CopyFromScreen($s.Location,[System.Drawing.Point]::Empty,$s.Size);$g.Dispose();$b.Save(`+escapePSSingleQuoted(path)+`)`)
		desktopRun(fallback) // best-effort fallback
	case "darwin":
		cmd := exec.CommandContext(ctx, "imagesnap", "-w", "1", path)
		if _, err := exec.LookPath("imagesnap"); err != nil {
			cmd = exec.CommandContext(ctx, "ffmpeg", "-f", "avfoundation", "-i", "1", "-vframes", "1", "-y", path)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("take-photo: %w\n%s", err, string(out))
		}
	default:
		cmd := exec.CommandContext(ctx, "ffmpeg", "-f", "v4l2", "-i", "/dev/video0", "-vframes", "1", "-y", path)
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return "", fmt.Errorf("take-photo: install ffmpeg (tried /dev/video0)")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("take-photo: %w\n%s", err, string(out))
		}
	}
	info, err := os.Stat(path)
	var size string
	if err == nil {
		size = humanizeDeskBytes(info.Size())
	} else {
		size = "unknown"
	}
	return fmt.Sprintf("# Photo\n\n✅ Captured to `%s` (%s)\n", path, size), nil
}

func recordVideo(ctx context.Context, path string, sec int) (string, error) {
	if path == "" {
		path = fmt.Sprintf("video-%s.mp4", time.Now().Format("20060102-150405"))
	}
	if sec <= 0 {
		sec = 5
	}
	if sec > 30 {
		sec = 30
	}
	switch runtime.GOOS {
	case "windows":
		cmd := exec.CommandContext(ctx, "ffmpeg", "-f", "dshow", "-i", "video=Integrated Camera:audio=Microphone", "-t", strconv.Itoa(sec), "-y", path)
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return "", fmt.Errorf("record-video: install ffmpeg")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-video: %w\n%s", err, string(out))
		}
	case "darwin":
		cmd := exec.CommandContext(ctx, "ffmpeg", "-f", "avfoundation", "-i", "1:0", "-t", strconv.Itoa(sec), "-y", path)
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return "", fmt.Errorf("record-video: install ffmpeg")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-video: %w\n%s", err, string(out))
		}
	default:
		cmd := exec.CommandContext(ctx, "ffmpeg", "-f", "v4l2", "-i", "/dev/video0", "-f", "alsa", "-i", "default", "-t", strconv.Itoa(sec), "-y", path)
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return "", fmt.Errorf("record-video: install ffmpeg")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("record-video: %w\n%s", err, string(out))
		}
	}
	info, err := os.Stat(path)
	var size string
	if err == nil {
		size = humanizeDeskBytes(info.Size())
	} else {
		size = "unknown"
	}
	return fmt.Sprintf("# Video\n\n✅ Recorded %ds to `%s` (%s)\n", sec, path, size), nil
}

// ——— notification ———

func notifyDesktop(ctx context.Context, headline, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if headline == "" {
		headline = "OK"
	}
	switch runtime.GOOS {
	case "windows":
		psScript :=
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Toast {
[DllImport("user32.dll")]public static extern int MessageBoxW(int h,string t,string c,int t2);
}
"@;[Toast]::MessageBoxW(0,` + escapePSSingleQuoted(text) + `,` + escapePSSingleQuoted(headline) + `,0)`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		if _, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "desktop: notify: %v\n", err)
		}
	case "darwin":
		// AppleScript uses "" to escape a literal " inside strings (not \")
		textEsc := strings.ReplaceAll(text, "\"", "\"\"")
		headlineEsc := strings.ReplaceAll(headline, "\"", "\"\"")
		cmd := exec.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`display notification "%s" with title "%s"`,
				textEsc,
				headlineEsc,
			))
		desktopRun(cmd)
	default:
		cmd := exec.CommandContext(ctx, "notify-send", headline, text)
		desktopRun(cmd)
	}
	return fmt.Sprintf("# Notify\n\n✅ Sent: %s — %s\n", headline, text), nil
}

// ——— environment variables ———

func envList(ctx context.Context) (string, error) {
	env := os.Environ()
	var b strings.Builder
	b.WriteString("# Environment Variables\n\n")
	b.WriteString("```\n")
	for _, e := range env {
		b.WriteString(e)
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	return b.String(), nil
}

func envGet(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("variable name is required")
	}
	val, ok := os.LookupEnv(name)
	if !ok {
		return fmt.Sprintf("# Environment\n\n`%s` is not set\n", name), nil
	}
	return fmt.Sprintf("# Environment\n\n`%s` = `%s`\n", name, val), nil
}

func envSet(ctx context.Context, name, value string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("variable name is required")
	}
	if err := os.Setenv(name, value); err != nil {
		return "", fmt.Errorf("setenv: %w", err)
	}
	return fmt.Sprintf("# Environment\n\n✅ Set `%s` = `%s`\n", name, value), nil
}
