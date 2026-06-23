// @ok/computer-use — MCP plugin: Visual desktop automation.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/NB-Agent/ok/internal/plugin"
)

type server struct{}

func (server) Info() (string, string) { return "ok-computer-use", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{
			Name:        "computer-use",
			Description: "Control the computer visually — screenshot, analyze, act",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal": map[string]any{"type": "string"},
				},
				"required": []string{"goal"},
			},
		},
	}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "computer-use" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct{ Goal string }
	json.Unmarshal(args, &p)
	if p.Goal == "" {
		return "", fmt.Errorf("goal required")
	}
	ss, err := screenshot()
	if err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}
	return fmt.Sprintf("Goal: %s\nScreenshot: %s\n", p.Goal, ss), nil
}

func main() { plugin.RunStdio(server{}) }

func screenshot() (string, error) {
	path := os.TempDir() + "\\ok-cu.png"
	err := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $b=New-Object Drawing.Bitmap([Windows.Forms.Screen]::PrimaryScreen.Bounds.Width,[Windows.Forms.Screen]::PrimaryScreen.Bounds.Height); $g=[Drawing.Graphics]::FromImage($b); $g.CopyFromScreen(0,0,0,0,$b.Size); $b.Save('%s')`, path)).Run()
	return path, err
}
