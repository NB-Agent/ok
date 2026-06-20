// Package voice provides speech-to-text and text-to-speech engines for
// voice-based interaction with OK. It wraps Whisper.cpp (STT) and Piper TTS
// as external processes — no CGo required, pure Go build stays intact.
//
// Architecture:
//
//	User speaks → STT (Whisper.cpp) → text → Agent processes → response text
//	→ TTS (Piper) → audio → User hears
package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/winhide"
)

// ModelDir returns the directory where voice models are stored.
func ModelDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ok-voice")
	}
	return filepath.Join(configDir, "ok", "voice")
}

// ── STT (Speech-to-Text) ──

// STT wraps Whisper.cpp for speech recognition.
type STT struct {
	modelPath string
	lang      string // auto-detect if empty
}

// NewSTT creates a speech recognizer using the given whisper model.
func NewSTT(modelDir, lang string) *STT {
	return &STT{
		modelPath: filepath.Join(modelDir, "ggml-base.bin"),
		lang:      lang,
	}
}

// Transcribe converts audio data (WAV) to text.
func (s *STT) Transcribe(ctx context.Context, audio []byte) (string, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ok-stt-%d.wav", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, audio, 0644); err != nil {
		return "", fmt.Errorf("write temp audio: %w", err)
	}
	defer os.Remove(tmpFile)

	args := []string{"--model", s.modelPath, "--output-txt", "-f", tmpFile}
	if s.lang != "" {
		args = append(args, "--language", s.lang)
	}
	args = append(args, "--file", tmpFile)

	cmd := winhide.CommandContext(ctx, "whisper", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	return string(bytes.TrimSpace(out)), nil
}

// DetectLanguage detects the language of audio.
func (s *STT) DetectLanguage(audio []byte) string {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ok-stt-detect-%d.wav", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, audio, 0644); err != nil {
		return "en"
	}
	defer os.Remove(tmpFile)

	cmd := winhide.Command("whisper", "--model", s.modelPath, "--detect-language", "-f", tmpFile)
	out, err := cmd.Output()
	if err != nil {
		return "en"
	}
	return string(bytes.TrimSpace(out))
}

// ── TTS (Text-to-Speech) ──

// TTS wraps Piper for speech synthesis.
type TTS struct {
	modelPath string
	lang      string
}

// NewTTS creates a speech synthesizer using the given Piper voice model.
func NewTTS(modelDir, lang string) *TTS {
	return &TTS{
		modelPath: filepath.Join(modelDir, "voice-"+lang+".onnx"),
		lang:      lang,
	}
}

// Speak converts text to audio and plays it.
func (t *TTS) Speak(ctx context.Context, text string) error {
	cmd := winhide.CommandContext(ctx, "piper",
		"--model", t.modelPath,
		"--output-raw",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("piper stdin: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
				fmt.Fprintf(os.Stderr, "voice: panic in piper stdin goroutine: %v\n", r)
			}
			wg.Done()
		}()
		defer log.Close("stt stdin", stdin)
		// Write to piper stdin, canceling if the context expires (piper
		// itself is managed by cmd which also respects ctx). Without this
		// the goroutine blocks forever if the process hangs.
		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("goroutine panic", "recover", r)
					fmt.Fprintf(os.Stderr, "voice: panic writing to piper stdin: %v\n", r)
				}
			}()
			if _, err := io.WriteString(stdin, text); err != nil {
				fmt.Fprintf(os.Stderr, "voice: write to piper stdin: %v\n", err)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "voice: piper stdin write cancelled: %v\n", ctx.Err())
			<-done // wait for the write goroutine to finish before closing stdin
		}
	}()

	// Play via aplay (Linux) / sox (macOS) / PowerShell (Windows)
	var playCmd *exec.Cmd
	switch detectOS() {
	case "windows":
		playCmd = winhide.Command("powershell", "-c",
			`$stream = [Console]::OpenStandardOutput(); $reader = [System.IO.BinaryReader]::new([Console]::OpenStandardInput()); while($true) { $data = $reader.ReadBytes(4096); if($data.Length -eq 0) { break }; $stream.Write($data, 0, $data.Length) }`)
	case "darwin":
		playCmd = winhide.Command("sox", "-t", "s16le", "-r", "22050", "-c", "1", "-", "-d")
	default:
		playCmd = winhide.Command("aplay", "-r", "22050", "-f", "S16_LE", "-c", "1")
	}

	piperOut, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("piper stdout pipe: %w", err)
	}
	playCmd.Stdin = piperOut

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("piper start: %w", err)
	}
	if err := playCmd.Start(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintf(os.Stderr, "voice: kill piper after player start fail: %v\n", killErr)
		}
		return fmt.Errorf("player start: %w", err)
	}

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "voice: piper wait error: %v\n", err)
	}
	if err := playCmd.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "voice: player wait error: %v\n", err)
	}
	return nil
}

// Synthesize converts text to WAV audio bytes (doesn't play).
func (t *TTS) Synthesize(ctx context.Context, text string) ([]byte, error) {
	cmd := winhide.CommandContext(ctx, "piper",
		"--model", t.modelPath,
		"--output-raw",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		if cerr := stdin.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "voice: stdin close after write error: %v\n", cerr)
		}
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintf(os.Stderr, "voice: kill piper after write fail: %v\n", killErr)
		}
		return nil, fmt.Errorf("write to piper stdin: %w", err)
	}
	if err := stdin.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "voice: stdin close after write: %v\n", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func detectOS() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

// ── Audio capture ──

// Recorder captures audio from the microphone.
type Recorder struct{}

// Record captures audio for the given duration and returns WAV bytes.
func (r *Recorder) Record(ctx context.Context, duration time.Duration) ([]byte, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ok-record-%d.wav", time.Now().UnixNano()))

	var cmd *exec.Cmd
	switch detectOS() {
	case "windows":
		cmd = winhide.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-c",
			windowsRecordScript(tmpFile, int(duration.Seconds())))
	case "darwin":
		cmd = winhide.CommandContext(ctx, "sox", "-d", "-t", "wav", tmpFile, "trim", "0", fmt.Sprintf("%.0f", duration.Seconds()))
	default:
		cmd = winhide.CommandContext(ctx, "arecord", "-d", fmt.Sprintf("%.0f", duration.Seconds()), "-f", "cd", "-t", "wav", tmpFile)
	}

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("record: %w", err)
	}

	data, err := os.ReadFile(tmpFile)
	os.Remove(tmpFile)
	return data, err
}

// windowsRecordScript returns a PowerShell script that captures audio from the
// default recording device using the Windows MCI (Media Control Interface) API,
// which is built into every version of Windows and requires no extra dependencies.
// Falls back to ffmpeg if available, which produces better quality WAV output.
func windowsRecordScript(outputPath string, durationSec int) string {
	// Try ffmpeg first (better quality, industry standard), then MCI as fallback.
	return fmt.Sprintf(`
$duration = %d
$output = '%s'

# Prefer ffmpeg for high-quality, properly formatted WAV output.
$ffmpeg = Get-Command ffmpeg -ErrorAction SilentlyContinue
if ($ffmpeg) {
    & ffmpeg -f dshow -i audio="default" -t $duration -acodec pcm_s16le -ar 16000 -ac 1 -y $output 2>$null
    if ($LASTEXITCODE -eq 0) { exit 0 }
}

# Fallback: use Windows MCI (Media Control Interface) to record from the
# default microphone. This works on every Windows version without extra deps.
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public class MCI {
    [DllImport("winmm.dll", CharSet=CharSet.Unicode)]
    public static extern int mciSendString(string cmd, System.Text.StringBuilder ret, int retLen, IntPtr cb);
}
'@
$null = New-Object System.Text.StringBuilder 256
[MCI]::mciSendString("open new type waveaudio alias okrec", $null, 0, [IntPtr]::Zero)
[MCI]::mciSendString("set okrec time format ms bitspersample 16 channels 1 samplespersec 16000", $null, 0, [IntPtr]::Zero)
[MCI]::mciSendString("record okrec", $null, 0, [IntPtr]::Zero)
Start-Sleep -Seconds $duration
[MCI]::mciSendString("stop okrec", $null, 0, [IntPtr]::Zero)
[MCI]::mciSendString("save okrec $output", $null, 0, [IntPtr]::Zero)
[MCI]::mciSendString("close okrec", $null, 0, [IntPtr]::Zero)
`, durationSec, outputPath)
}

// WriteWAV writes PCM audio data as a WAV file.
func WriteWAV(w io.Writer, samples []int16, sampleRate int) error {
	// WAV header
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, []byte("RIFF"))
	binary.Write(buf, binary.LittleEndian, uint32(36+len(samples)*2))
	binary.Write(buf, binary.LittleEndian, []byte("WAVE"))
	binary.Write(buf, binary.LittleEndian, []byte("fmt "))
	binary.Write(buf, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(buf, binary.LittleEndian, uint16(1))  // PCM
	binary.Write(buf, binary.LittleEndian, uint16(1))  // mono
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	binary.Write(buf, binary.LittleEndian, uint16(2))            // block align
	binary.Write(buf, binary.LittleEndian, uint16(16))           // bits per sample
	binary.Write(buf, binary.LittleEndian, []byte("data"))
	binary.Write(buf, binary.LittleEndian, uint32(len(samples)*2))
	for _, s := range samples {
		binary.Write(buf, binary.LittleEndian, s)
	}
	data := buf.Bytes()
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("wav: short write (%d of %d bytes)", n, len(data))
	}
	return nil
}
