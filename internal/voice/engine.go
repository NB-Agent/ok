// Package voice provides the full voice interaction loop for OK.
// It combines STT (Whisper.cpp), TTS (Piper), and the Agent into
// a complete speak-listen-respond cycle.
package voice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
)

// Engine manages a voice interaction session.
type Engine struct {
	mu   sync.Mutex
	stt  *STT
	tts  *TTS
	rec  *Recorder
	lang string // current language
}

// NewEngine creates a voice engine for the given language.
func NewEngine(lang string) *Engine {
	modelDir := ModelDir()
	return &Engine{
		stt:  NewSTT(modelDir, lang),
		tts:  NewTTS(modelDir, lang),
		rec:  &Recorder{},
		lang: lang,
	}
}

// ListenAndRespond performs one complete voice interaction turn.
func (e *Engine) ListenAndRespond(ctx context.Context) (string, error) {
	// 1. Play ready tone
	_ = e.tts.Speak(ctx, "listening") // non-critical; swallow audio-playback errors

	// 2. Record user speech
	audio, err := e.rec.Record(ctx, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("record: %w", err)
	}

	// 3. Transcribe (read stt under lock to avoid race with SetLanguage)
	e.mu.Lock()
	stt := e.stt
	e.mu.Unlock()
	text, err := stt.Transcribe(ctx, audio)
	if err != nil {
		return "", fmt.Errorf("transcribe: %w", err)
	}

	// 4. Detect and switch language if needed
	e.mu.Lock()
	detected := e.stt.DetectLanguage(audio)
	if detected != "" && detected != e.lang {
		e.SetLanguage(detected)
	}
	e.mu.Unlock()

	return text, nil
}

// SpeakOutput speaks text to the user.
func (e *Engine) SpeakOutput(ctx context.Context, text string) error {
	e.mu.Lock()
	tts := e.tts
	e.mu.Unlock()
	return tts.Speak(ctx, text)
}

// SetLanguage switches the voice engine to a different language.
// Must be called with e.mu held, OR externally when no other goroutine
// is concurrently accessing stt/tts.
func (e *Engine) SetLanguage(lang string) {
	e.lang = lang
	e.stt = NewSTT(ModelDir(), lang)
	e.tts = NewTTS(ModelDir(), lang)
}

// AgentVoiceLoop runs a complete voice interaction with the agent.
func AgentVoiceLoop(ctx context.Context, eng *Engine, ag *agent.Agent) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			text, err := eng.ListenAndRespond(ctx)
			if err != nil {
				if w, ok := ctx.Value("stderr").(interface{ Write([]byte) (int, error) }); ok {
					fmt.Fprintf(w, "voice: %v\n", err) //nolint:errcheck
				}
				continue
			}
			if text == "" {
				continue
			}
			if err := ag.Run(ctx, text); err != nil {
				_ = eng.SpeakOutput(ctx, "Sorry, I encountered an error.") // non-fatal audio error
				continue
			}
			_ = eng.SpeakOutput(ctx, "Task completed.") // non-fatal audio error
		}
	}
}
