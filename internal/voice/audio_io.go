// Package voice provides speech-to-text and text-to-speech engines.
//
// Audio I/O interface: AudioRecorder and AudioPlayer define the
// platform-independent contract. The default implementation delegates
// to the recorder/player in stt.go (os/exec, no CGo needed).
package voice

import (
	"context"
	"time"
)

// AudioRecorder records audio from the microphone.
type AudioRecorder interface {
	Record(ctx context.Context, duration time.Duration) ([]byte, error)
}

// AudioPlayer plays raw PCM audio data to the speaker.
type AudioPlayer interface {
	Play(ctx context.Context, samples []byte) error
}

// NewRecorder returns the platform's best available audio recorder.
func NewRecorder() AudioRecorder { return &Recorder{} }
