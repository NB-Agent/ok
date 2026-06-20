// Package agent provides the ComputerUse orchestrator — a screenshot→analyze→act→verify
// loop that lets the agent control the desktop GUI. It calls the LLM directly (bypassing
// the provider.Message type, which doesn't support multimodal image inputs) to analyze
// screenshots and decide the next action.
package computeruse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/winhide"
)

// ComputerUse runs a visual desktop-automation loop: screenshot → LLM analysis →
// execute action → verify → repeat. It makes direct HTTP calls to the OpenAI-compatible
// provider so it can send base64-encoded screenshot images (the standard Message type
// only supports plain text).
type ComputerUse struct {
	apiKey   string
	baseURL  string
	model    string
	http     *http.Client
	maxSteps int
}

// ComputerAction is a structured action the LLM returns during the computer-use loop.
type ComputerAction struct {
	Action  string `json:"action"` // click | double-click | right-click | type | key | scroll | wait | done | fail
	X       int    `json:"x,omitempty"`
	Y       int    `json:"y,omitempty"`
	Text    string `json:"text,omitempty"`
	Key     string `json:"key,omitempty"`     // Enter, Tab, Escape, etc.
	Clicks  int    `json:"clicks,omitempty"`  // scroll: positive=down, negative=up
	MS      int    `json:"ms,omitempty"`      // wait: milliseconds to wait
	Summary string `json:"summary,omitempty"` // done/fail
	Reason  string `json:"reason,omitempty"`  // fail
}

// NewComputerUse creates a ComputerUse loop driver.
func NewComputerUse(apiKey, baseURL, model string) *ComputerUse {
	return &ComputerUse{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		maxSteps: 20,
	}
}

// actionSchemas are the tool definitions exposed to the LLM during the visual loop.
var actionSchemas = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "click",
			"description": "Left-click at screen coordinates (x, y)",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "double_click",
			"description": "Double-click at screen coordinates (x, y)",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "right_click",
			"description": "Right-click at screen coordinates (x, y)",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "integer", "description": "X coordinate"},
					"y": map[string]any{"type": "integer", "description": "Y coordinate"},
				},
				"required": []string{"x", "y"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "type",
			"description": "Type text at the current cursor position",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string", "description": "Text to type"},
				},
				"required": []string{"text"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "key",
			"description": "Press a special keyboard key",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key": map[string]any{
						"type": "string",
						"enum": []string{"Enter", "Tab", "Escape", "Backspace", "Delete",
							"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
							"Ctrl+A", "Ctrl+C", "Ctrl+V", "Ctrl+Z", "Ctrl+S", "Alt+Tab", "Ctrl+Enter"},
					},
				},
				"required": []string{"key"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "scroll",
			"description": "Scroll the mouse wheel. Negative clicks = scroll up, positive = scroll down.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"clicks": map[string]any{"type": "integer", "description": "Number of scroll clicks (negative=up, positive=down)"},
				},
				"required": []string{"clicks"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "wait",
			"description": "Wait for the UI to update. Use after opening an app, clicking a dialog button, or before verifying a change.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ms": map[string]any{"type": "integer", "description": "Milliseconds to wait (max 5000, default 500)"},
				},
				"required": []string{"ms"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "done",
			"description": "Call when the goal is fully accomplished",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{"type": "string", "description": "Summary of what was accomplished"},
				},
				"required": []string{"summary"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "fail",
			"description": "Call when the goal cannot be completed",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string", "description": "Why the goal cannot be completed"},
				},
				"required": []string{"reason"},
			},
		},
	},
}

const computerUseSystemPrompt = `You are a computer control AI. Your job is to complete a user's goal by looking at screenshots and deciding what actions to take on the computer.

RULES:
1. You will receive a screenshot image. Examine it carefully to understand what's on screen.
2. Choose ONE action at a time. After each action, you'll receive a new screenshot showing the result.
3. Use precise pixel coordinates based on what you see in the screenshot.
4. If the screen resolution is large, remember that coordinates start from the top-left corner (0,0).
5. To find a contact in an app like WeChat/WhatsApp/Telegram: first click the search bar, type the name, wait, then click the contact.
6. After typing text, always press Enter or click the send button to send the message.
7. After clicking something that should open a dialog/window/app, wait 500ms for the UI to update, then check the next screenshot to verify.
8. After each action, the system will show you a verification screenshot. Check if the action had the expected effect. If not, try a different approach.
9. Call "done" only when the goal is fully complete.
10. Call "fail" if you encounter an insurmountable obstacle (explain why).`

// RunGoal executes one computer-use goal: screenshot → analyze → act → verify loop.
// It returns a summary of what was accomplished.
func (cu *ComputerUse) RunGoal(ctx context.Context, goal string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "ok-computer-*")
	if err != nil {
		return "", fmt.Errorf("computer-use: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	history := make([]map[string]any, 0)

	for step := 0; step < cu.maxSteps; step++ {
		// 1. Take screenshot
		screenshotPath := filepath.Join(tmpDir, fmt.Sprintf("step-%d.png", step))
		if err := takeScreenshot(ctx, screenshotPath); err != nil {
			return "", fmt.Errorf("computer-use: screenshot: %w", err)
		}

		// 2. Read and base64 encode
		b64img, err := imageToBase64(screenshotPath)
		if err != nil {
			return "", fmt.Errorf("computer-use: encode image: %w", err)
		}

		// 3. Build the multimodal message
		userMsg := map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("GOAL: %s\n\nStep %d of %d. Look at the screenshot and decide the next action.", goal, step+1, cu.maxSteps)},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64img}},
			},
		}

		// 4. Build messages array
		messages := []map[string]any{
			{"role": "system", "content": computerUseSystemPrompt},
		}
		messages = append(messages, history...)
		messages = append(messages, userMsg)

		// 5. Call LLM
		action, callID, err := cu.callLLM(ctx, messages)
		if err != nil {
			return "", fmt.Errorf("computer-use: LLM call: %w", err)
		}

		// 6. Execute action
		switch action.Action {
		case "done":
			summary := action.Summary
			if summary == "" {
				summary = "Goal completed"
			}
			return summary, nil

		case "fail":
			reason := action.Reason
			if reason == "" {
				reason = "Unknown failure"
			}
			return "", fmt.Errorf("computer-use: %s", reason)

		case "wait":
			ms := action.MS
			if ms <= 0 {
				ms = 500
			}
			if ms > 5000 {
				ms = 5000
			}
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			history = append(history, assistantActionMsg(callID, "wait", map[string]any{"ms": ms}))
			history = append(history, map[string]any{
				"role": "tool", "tool_call_id": callID,
				"content": fmt.Sprintf("Waited %dms", ms),
			})

		case "click":
			if err := desktopClick(ctx, action.X, action.Y); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "click", map[string]any{"x": action.X, "y": action.Y}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Clicked at (%d, %d)", action.X, action.Y),
			})
			history = cu.appendVerification(ctx, history, tmpDir, step)

		case "double-click":
			if err := desktopDoubleClick(ctx, action.X, action.Y); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "double_click", map[string]any{"x": action.X, "y": action.Y}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Double-clicked at (%d, %d)", action.X, action.Y),
			})
			history = cu.appendVerification(ctx, history, tmpDir, step)

		case "right-click":
			if err := desktopRightClick(ctx, action.X, action.Y); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "right_click", map[string]any{"x": action.X, "y": action.Y}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Right-clicked at (%d, %d)", action.X, action.Y),
			})

		case "type":
			if err := desktopType(ctx, action.Text); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "type", map[string]any{"text": action.Text}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Typed: %q", action.Text),
			})

		case "key":
			if err := desktopKey(ctx, action.Key); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "key", map[string]any{"key": action.Key}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Pressed key: %s", action.Key),
			})

		case "scroll":
			if err := desktopScroll(ctx, action.Clicks); err != nil {
				history = append(history, map[string]any{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      fmt.Sprintf("Error: %v", err),
				})
				continue
			}
			history = append(history, assistantActionMsg(callID, "scroll", map[string]any{"clicks": action.Clicks}))
			history = append(history, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("Scrolled %d clicks", action.Clicks),
			})

		default:
			return "", fmt.Errorf("computer-use: unknown action %q", action.Action)
		}
	}

	return "", fmt.Errorf("computer-use: max steps (%d) reached — goal may be partially complete", cu.maxSteps)
}

// appendVerification takes a post-action screenshot so the LLM can verify
// the action had the expected effect. It pauses 300ms first for UI rendering.
func (cu *ComputerUse) appendVerification(ctx context.Context, history []map[string]any, tmpDir string, step int) []map[string]any {
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return history
	}
	verifyPath := filepath.Join(tmpDir, fmt.Sprintf("step-%d-verify.png", step))
	if err := takeScreenshot(ctx, verifyPath); err != nil {
		history = append(history, map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "(Verification screenshot unavailable — continue to next step)"},
			},
		})
		return history
	}
	b64v, err := imageToBase64(verifyPath)
	if err != nil {
		return history
	}
	history = append(history, map[string]any{
		"role": "user",
		"content": []map[string]any{
			{"type": "text", "text": "SCREEN AFTER ACTION: Verify the action had the expected effect. If it did not, try a different approach in the next step."},
			{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64v}},
		},
	})
	return history
}

// callLLM sends the messages to the API and returns the parsed action.
func (cu *ComputerUse) callLLM(ctx context.Context, messages []map[string]any) (ComputerAction, string, error) {
	body := map[string]any{
		"model":       cu.model,
		"messages":    messages,
		"tools":       actionSchemas,
		"tool_choice": "auto",
		"temperature": 0,
		"max_tokens":  1024,
	}

	rawBody, err := json.Marshal(body)
	if err != nil {
		return ComputerAction{}, "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cu.baseURL+"/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		return ComputerAction{}, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cu.apiKey)

	resp, err := cu.http.Do(req)
	if err != nil {
		return ComputerAction{}, "", fmt.Errorf("request: %w", err)
	}
	defer log.Close("computer response", resp.Body)

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return ComputerAction{}, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ComputerAction{}, "", fmt.Errorf("API status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(rawResp, &result); err != nil {
		return ComputerAction{}, "", fmt.Errorf("unmarshal: %w", err)
	}

	if len(result.Choices) == 0 {
		return ComputerAction{}, "", fmt.Errorf("no choices in response")
	}

	msg := result.Choices[0].Message

	// If the model responded with text instead of a tool call, treat it as a
	// "look again" instruction — just pass the text back as a tool result so the
	// next step sees it and issues a real action.
	if len(msg.ToolCalls) == 0 {
		return ComputerAction{
			Action:  "done",
			Summary: msg.Content,
		}, "", nil
	}

	tc := msg.ToolCalls[0]
	callID := tc.ID
	if callID == "" {
		callID = "cu-" + strconv.Itoa(len(messages))
	}

	action := ComputerAction{}
	rawArgs := []byte(tc.Function.Arguments)

	switch tc.Function.Name {
	case "click":
		action.Action = "click"
		var p struct{ X, Y int }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse click args: %w", err)
		}
		action.X, action.Y = p.X, p.Y

	case "double_click":
		action.Action = "double-click"
		var p struct{ X, Y int }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse double_click args: %w", err)
		}
		action.X, action.Y = p.X, p.Y

	case "right_click":
		action.Action = "right-click"
		var p struct{ X, Y int }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse right_click args: %w", err)
		}
		action.X, action.Y = p.X, p.Y

	case "type":
		action.Action = "type"
		var p struct{ Text string }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse type args: %w", err)
		}
		action.Text = p.Text

	case "key":
		action.Action = "key"
		var p struct{ Key string }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse key args: %w", err)
		}
		action.Key = p.Key

	case "scroll":
		action.Action = "scroll"
		var p struct{ Clicks int }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			return ComputerAction{}, "", fmt.Errorf("parse scroll args: %w", err)
		}
		action.Clicks = p.Clicks

	case "done":
		action.Action = "done"
		var p struct{ Summary string }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			action.Summary = tc.Function.Arguments // best effort
		} else {
			action.Summary = p.Summary
		}

	case "fail":
		action.Action = "fail"
		var p struct{ Reason string }
		if err := json.Unmarshal(rawArgs, &p); err != nil {
			action.Reason = tc.Function.Arguments
		} else {
			action.Reason = p.Reason
		}

	default:
		return ComputerAction{}, "", fmt.Errorf("unknown action: %s", tc.Function.Name)
	}

	return action, callID, nil
}

// assistantActionMsg builds the assistant message that issued a tool call.
func assistantActionMsg(callID, name string, args map[string]any) map[string]any {
	rawArgs, err := json.Marshal(args)
	if err != nil {
		rawArgs = []byte(`{"error":"failed to marshal args"}`)
	}
	return map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []map[string]any{
			{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(rawArgs),
				},
			},
		},
	}
}

// ——— desktop helpers (cross-platform) ———

func takeScreenshot(ctx context.Context, path string) error {
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms,System.Drawing;`+
				`$s=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;`+
				`$b=New-Object System.Drawing.Bitmap($s.Width,$s.Height);`+
				`$g=[System.Drawing.Graphics]::FromImage($b);`+
				`$g.CopyFromScreen($s.Location,[System.Drawing.Point]::Empty,$s.Size);`+
				`$g.Dispose();$b.Save('%s');$b.Dispose()`,
			path,
		)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("screenshot: %w\n%s", err, string(out))
		}
		return nil
	case "darwin":
		cmd := winhide.CommandContext(ctx, "screencapture", "-x", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("screenshot: %w\n%s", err, string(out))
		}
		return nil
	default:
		cmd := winhide.CommandContext(ctx, "gnome-screenshot", "-f", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("screenshot: %w\n%s", err, string(out))
		}
		return nil
	}
}

func imageToBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func desktopClick(ctx context.Context, x, y int) error {
	switch runtime.GOOS {
	case "windows":
		// BUGFIX: Move cursor to (x,y) before clicking. Previous code called
		// mouse_event with (0,0) and clicks always happened at current position.
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void SetCursorPos(int x,int y);
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint LEFTDOWN=0x02, LEFTUP=0x04;
}
"@;[Mouse]::SetCursorPos(%d,%d);Start-Sleep -Milliseconds 50;[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);Start-Sleep -Milliseconds 50;[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0)`,
			x, y)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		cmd := winhide.CommandContext(ctx, "cliclick", fmt.Sprintf("c:%d,%d", x, y))
		_, err := cmd.CombinedOutput()
		return err
	default:
		cmd := winhide.CommandContext(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1")
		_, err := cmd.CombinedOutput()
		return err
	}
}

func desktopDoubleClick(ctx context.Context, x, y int) error {
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void SetCursorPos(int x,int y);
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint LEFTDOWN=0x02, LEFTUP=0x04;
}
"@;[Mouse]::SetCursorPos(%d,%d);Start-Sleep -Milliseconds 50;
[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0);
Start-Sleep -Milliseconds 50;
[Mouse]::mouse_event([Mouse]::LEFTDOWN,0,0,0,0);[Mouse]::mouse_event([Mouse]::LEFTUP,0,0,0,0)`,
			x, y)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		cmd := winhide.CommandContext(ctx, "cliclick", fmt.Sprintf("d:%d,%d", x, y))
		_, err := cmd.CombinedOutput()
		return err
	default:
		cmd := winhide.CommandContext(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "--repeat", "2", "1")
		_, err := cmd.CombinedOutput()
		return err
	}
}

func desktopRightClick(ctx context.Context, x, y int) error {
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void SetCursorPos(int x,int y);
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint RIGHTDOWN=0x08, RIGHTUP=0x10;
}
"@;[Mouse]::SetCursorPos(%d,%d);Start-Sleep -Milliseconds 50;
[Mouse]::mouse_event([Mouse]::RIGHTDOWN,0,0,0,0);Start-Sleep -Milliseconds 50;[Mouse]::mouse_event([Mouse]::RIGHTUP,0,0,0,0)`,
			x, y)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		cmd := winhide.CommandContext(ctx, "cliclick", fmt.Sprintf("rc:%d,%d", x, y))
		_, err := cmd.CombinedOutput()
		return err
	default:
		cmd := winhide.CommandContext(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "3")
		_, err := cmd.CombinedOutput()
		return err
	}
}

func desktopType(ctx context.Context, text string) error {
	switch runtime.GOOS {
	case "windows":
		psScript := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`,
			escapeSendKeys(text),
		)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		escaped := strings.ReplaceAll(text, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		cmd := winhide.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, escaped))
		_, err := cmd.CombinedOutput()
		return err
	default:
		cmd := winhide.CommandContext(ctx, "xdotool", "type", "--delay", "50", text)
		_, err := cmd.CombinedOutput()
		return err
	}
}

func desktopKey(ctx context.Context, key string) error {
	switch runtime.GOOS {
	case "windows":
		var mapped string
		switch key {
		case "Enter":
			mapped = "{ENTER}"
		case "Tab":
			mapped = "{TAB}"
		case "Escape":
			mapped = "{ESC}"
		case "Backspace":
			mapped = "{BACKSPACE}"
		case "Delete":
			mapped = "{DELETE}"
		case "ArrowUp":
			mapped = "{UP}"
		case "ArrowDown":
			mapped = "{DOWN}"
		case "ArrowLeft":
			mapped = "{LEFT}"
		case "ArrowRight":
			mapped = "{RIGHT}"
		case "Ctrl+A":
			mapped = "^a"
		case "Ctrl+C":
			mapped = "^c"
		case "Ctrl+V":
			mapped = "^v"
		case "Ctrl+Z":
			mapped = "^z"
		case "Ctrl+S":
			mapped = "^s"
		case "Alt+Tab":
			mapped = "%{TAB}"
		case "Ctrl+Enter":
			mapped = "^{ENTER}"
		default:
			mapped = key
		}
		psScript := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`,
			mapped,
		)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		var mapped string
		switch key {
		case "Enter":
			mapped = "return"
		case "Tab":
			mapped = "tab"
		case "Escape":
			mapped = "escape"
		case "Backspace":
			mapped = "delete"
		case "Delete":
			mapped = "ForwardDelete"
		case "ArrowUp":
			mapped = "up"
		case "ArrowDown":
			mapped = "down"
		case "ArrowLeft":
			mapped = "left"
		case "ArrowRight":
			mapped = "right"
		case "Ctrl+A":
			mapped = "a using command down"
		case "Ctrl+C":
			mapped = "c using command down"
		case "Ctrl+V":
			mapped = "v using command down"
		case "Alt+Tab":
			mapped = "tab using command down"
		default:
			mapped = key
		}
		escapedMapped := strings.ReplaceAll(mapped, "\\", "\\\\")
		escapedMapped = strings.ReplaceAll(escapedMapped, "\"", "\\\"")
		cmd := winhide.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, escapedMapped))
		_, err := cmd.CombinedOutput()
		return err
	default:
		cmd := winhide.CommandContext(ctx, "xdotool", "key", key)
		_, err := cmd.CombinedOutput()
		return err
	}
}

func desktopScroll(ctx context.Context, clicks int) error {
	switch runtime.GOOS {
	case "windows":
		amount := clicks * 120
		psScript := fmt.Sprintf(
			`Add-Type @"
using System;using System.Runtime.InteropServices;
public class Mouse {
[DllImport("user32.dll")]public static extern void mouse_event(uint f,uint x,uint y,uint d,uint i);
public const uint WHEEL=0x800;
}
"@;[Mouse]::mouse_event([Mouse]::WHEEL,0,0,%d,0)`, amount)
		cmd := winhide.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
		_, err := cmd.CombinedOutput()
		return err
	case "darwin":
		cmd := winhide.CommandContext(ctx, "cliclick", fmt.Sprintf("wp:%d,0", clicks))
		_, err := cmd.CombinedOutput()
		return err
	default:
		btn := "4" // scroll down
		if clicks < 0 {
			btn = "5" // scroll up
			clicks = -clicks
		}
		cmd := winhide.CommandContext(ctx, "xdotool", "click", "--repeat", strconv.Itoa(clicks), btn)
		_, err := cmd.CombinedOutput()
		return err
	}
}

func escapeSendKeys(s string) string {
	s = strings.ReplaceAll(s, "'", "''") // PowerShell single-quote escaping
	repl := strings.NewReplacer(
		"+", "{+}", "^", "{^}", "%", "{%}",
		"~", "{~}", "(", "{(}", ")", "{)}",
		"{", "{{}", "}", "{}}",
	)
	return repl.Replace(s)
}
