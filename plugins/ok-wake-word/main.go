// @ok/wake-word — MCP plugin: Wake word detection.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/plugin"
)

type server struct{ running *sync.Map }

func (server) Info() (string, string) { return "ok-wake-word", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{
			Name:        "wake-word",
			Description: "Listen for wake word",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":      plugin.StrEnum("start", "stop", "status"),
					"keyword":     plugin.StrProp(),
					"timeout_sec": map[string]any{"type": "integer"},
				},
				"required": []string{"action"},
			},
		},
	}
}

func (s server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "wake-word" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action      string `json:"action"`
		Keyword     string `json:"keyword"`
		TimeoutSec  int    `json:"timeout_sec"`
	}
	json.Unmarshal(args, &p)

	switch p.Action {
	case "start":
		if p.Keyword == "" {
			p.Keyword = "hey ok"
		}
		s.running.Store("listener", true)
		if p.TimeoutSec > 0 {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "wake-word listener panic: %v\n", r)
					}
				}()
				time.Sleep(time.Duration(p.TimeoutSec) * time.Second)
				s.running.Store("listener", false)
			}()
		}
		exec.Command("porcupine_demo", "--keywords", p.Keyword).Start()
		return p.Keyword, nil
	case "stop":
		s.running.Store("listener", false)
		exec.Command("taskkill", "/f", "/im", "porcupine_demo.exe").Run()
		return "stopped", nil
	case "status":
		status := "STOPPED"
		if v, ok := s.running.Load("listener"); ok {
			if b, okb := v.(bool); okb && b {
				status = "RUNNING"
			}
		}
		return status, nil
	}
	return "", fmt.Errorf("unknown action: %s", p.Action)
}

func main() { plugin.RunStdio(server{running: &sync.Map{}}) }
