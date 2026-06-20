package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(wakeWord{}) }

type wakeWord struct{}

func (wakeWord) Name() string { return "wake-word" }

func (wakeWord) Description() string {
	return "Listen for a wake word (default 'hey ok') in the background and trigger voice interaction. Requires a keyword-spotting engine (porcupine or vosk)."
}

func (wakeWord) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["start","stop","status"],"type":"string"},"keyword":{"type":"string"},"timeout_sec":{"type":"integer"}},"required":["action"],"type":"object"}`)
}

func (wakeWord) ReadOnly() bool { return false }

var (
	wakeWordCmd     *exec.Cmd
	wakeWordMu      sync.Mutex
	wakeWordKeyword string
	wakeWordEngine  string
)

func (wakeWord) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action     string `json:"action"`
		Keyword    string `json:"keyword"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	switch p.Action {
	case "start":
		return wakeWordStart(ctx, p)
	case "stop":
		return wakeWordStop()
	case "status":
		return wakeWordStatus()
	default:
		return "", fmt.Errorf("unknown action %q (use start/stop/status)", p.Action)
	}
}

func wakeWordStart(ctx context.Context, p struct {
	Action     string `json:"action"`
	Keyword    string `json:"keyword"`
	TimeoutSec int    `json:"timeout_sec"`
}) (string, error) {
	wakeWordMu.Lock()
	defer wakeWordMu.Unlock()

	if wakeWordCmd != nil {
		return "# Wake Word\n\nAlready listening for wake word. Use `action: stop` to stop.\n", nil
	}

	keyword := p.Keyword
	if keyword == "" {
		keyword = "hey ok"
	}

	// Detect available engine and build command.
	var cmd *exec.Cmd
	engine := ""
	if _, err := exec.LookPath("porcupine_demo"); err == nil {
		engine = "porcupine"
		args := []string{"--keywords", keyword}
		if p.TimeoutSec > 0 {
			args = append(args, "--timeout_sec", fmt.Sprintf("%d", p.TimeoutSec))
		}
		cmd = winhide.CommandContext(ctx, "porcupine_demo", args...)
	} else if _, err := exec.LookPath("vosk-transcriber"); err == nil {
		engine = "vosk"
		args := []string{"--keyword", keyword}
		if p.TimeoutSec > 0 {
			args = append(args, "--timeout-sec", fmt.Sprintf("%d", p.TimeoutSec))
		}
		cmd = winhide.CommandContext(ctx, "vosk-transcriber", args...)
	} else {
		return "", fmt.Errorf("no wake-word engine found — install Porcupine (https://github.com/Picovoice/porcupine) or Vosk (https://alphacephei.com/vosk)")
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start %s: %w", engine, err)
	}

	wakeWordCmd = cmd
	wakeWordKeyword = keyword
	wakeWordEngine = engine

	// Background goroutine: wait for process and clean up.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				wakeWordMu.Lock()
				wakeWordCmd = nil
				wakeWordMu.Unlock()
			}
		}()
		cmd.Wait()
		wakeWordMu.Lock()
		if wakeWordCmd == cmd {
			wakeWordCmd = nil
		}
		wakeWordMu.Unlock()
	}()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Wake Word\n\n- **Keyword**: `%s`\n", keyword))
	b.WriteString(fmt.Sprintf("- **Engine**: %s\n", engine))
	b.WriteString("- **Status**: listening in background\n")
	if p.TimeoutSec > 0 {
		b.WriteString(fmt.Sprintf("- **Timeout**: %ds\n", p.TimeoutSec))
	}
	b.WriteString("\nSay the wake word to trigger voice interaction. Use `wake-word action:stop` to stop listening.\n")
	return b.String(), nil
}

func wakeWordStop() (string, error) {
	wakeWordMu.Lock()
	defer wakeWordMu.Unlock()

	if wakeWordCmd == nil {
		return "# Wake Word\n\nNot currently listening.\n", nil
	}

	// Kill the process group to ensure all child processes terminate.
	if wakeWordCmd.Process != nil {
		// On Windows, Kill() terminates the process but not its children.
		// Use taskkill /T on Windows to kill the process tree.
		if err := wakeWordCmd.Process.Signal(syscall.SIGTERM); err != nil {
			wakeWordCmd.Process.Kill()
		}
	}
	wakeWordCmd = nil
	wakeWordKeyword = ""
	wakeWordEngine = ""
	return "# Wake Word\n\nStopped listening.\n", nil
}

func wakeWordStatus() (string, error) {
	wakeWordMu.Lock()
	cmd := wakeWordCmd
	keyword := wakeWordKeyword
	engine := wakeWordEngine
	wakeWordMu.Unlock()

	if cmd != nil {
		return fmt.Sprintf("# Wake Word\n\n**Status**: listening\n- **Keyword**: `%s`\n- **Engine**: %s\n\nUse `action: stop` to stop.\n", keyword, engine), nil
	}
	return "# Wake Word\n\n**Status**: stopped\n\nUse `action: start` to begin listening.\n", nil
}
