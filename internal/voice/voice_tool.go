// Package voice provides speech interaction with OK.
// This file registers the voice builtin tool so the agent can speak and listen.
// Registration happens in boot.go, not via init(), to avoid circular imports.
package voice

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool implements the voice interaction builtin tool.
type Tool struct {
	Engine *Engine
}

func (v *Tool) Name() string        { return "voice" }
func (v *Tool) ReadOnly() bool      { return false }
func (v *Tool) Description() string { return "Speak to the user or listen for voice input." }

func (v *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"description": "Voice interaction — speak text, listen for speech, or full converse cycle.",
		"type": "object",
		"properties": {
			"action": {
				"enum": ["speak", "listen", "converse"],
				"type": "string"
			},
			"text": {
				"description": "Text to speak (for speak action)",
				"type": "string"
			}
		},
		"required": ["action"]
	}`)
}

func (v *Tool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if v == nil || v.Engine == nil {
		return "", fmt.Errorf("voice engine not initialized")
	}
	var p struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "speak":
		if p.Text == "" {
			return "", fmt.Errorf("text is required for speak action")
		}
		if err := v.Engine.SpeakOutput(ctx, p.Text); err != nil {
			return "", fmt.Errorf("speak: %w", err)
		}
		return "spoken", nil

	case "listen":
		text, err := v.Engine.ListenAndRespond(ctx)
		if err != nil {
			return "", fmt.Errorf("listen: %w", err)
		}
		return text, nil

	case "converse":
		// Full converse cycle: listen → transcribe → speak back → return
		text, err := v.Engine.ListenAndRespond(ctx)
		if err != nil {
			return "", fmt.Errorf("converse listen: %w", err)
		}
		if text == "" {
			return "", fmt.Errorf("no speech detected")
		}
		// Speak the transcribed text back to confirm
		if speakErr := v.Engine.SpeakOutput(ctx, text); speakErr != nil {
			return fmt.Sprintf("Heard: %s (but speak-back failed: %v)", text, speakErr), nil
		}
		return fmt.Sprintf("Converse complete: %s", text), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}
