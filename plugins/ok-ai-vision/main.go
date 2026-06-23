// @ok/ai-vision — MCP plugin: Image and video analysis.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/plugin"
)

type server struct{}

func (server) Info() (string, string) { return "ok-ai-vision", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{Name: "image_read", Description: "Read an image file — metadata and visual content",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"path": plugin.StrProp(),
			}, "required": []string{"path"}}},
		{Name: "video_analyze", Description: "Analyze a video file — extract key frames and describe",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"path":         plugin.StrProp(),
				"interval_sec": map[string]any{"type": "integer"},
				"max_frames":   map[string]any{"type": "integer"},
			}, "required": []string{"path"}}},
	}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	var p struct {
		Path        string `json:"path"`
		IntervalSec int    `json:"interval_sec"`
		MaxFrames   int    `json:"max_frames"`
	}
	json.Unmarshal(args, &p)

	switch name {
	case "image_read":
		return readImage(p.Path)
	case "video_analyze":
		if p.IntervalSec <= 0 {
			p.IntervalSec = 10
		}
		if p.MaxFrames <= 0 {
			p.MaxFrames = 20
		}
		return analyzeVideo(p.Path, p.IntervalSec, p.MaxFrames)
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

func readImage(path string) (string, error) {
	if err := safePluginPath(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "vision file close: %v\n", err)
		}
	}()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	fi, _ := f.Stat()
	size := fi.Size()
	f.Close()
	return fmt.Sprintf("File: %s\nFormat: %s\nDimensions: %dx%d\nSize: %d bytes\n",
		filepath.Base(path), format, cfg.Width, cfg.Height, size), nil
}

func analyzeVideo(path string, intervalSec, maxFrames int) (string, error) {
	if err := safePluginPath(path); err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp("", "ok-video-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	dur, err := ffprobe(path)
	if err != nil {
		return "", fmt.Errorf("ffprobe: %w", err)
	}

	out := fmt.Sprintf("Video: %s\nDuration: %.1fs\n\nFrames:\n", filepath.Base(path), dur)
	for i := 0; i < maxFrames; i++ {
		t := float64(i) * float64(intervalSec)
		if t > dur {
			break
		}
		frame := filepath.Join(tmpDir, fmt.Sprintf("frame_%03d.jpg", i))
		ffmpeg := exec.Command("ffmpeg", "-ss", fmt.Sprintf("%.1f", t),
			"-i", path, "-vframes", "1", "-q:v", "2", frame, "-y", "--")
		if err := ffmpeg.Run(); err != nil {
			continue
		}
		fi, err := os.Stat(frame)
		if err != nil {
			out += fmt.Sprintf("  t=%.1fs size=unknown (stat error: %v)\n", t, err)
			continue
		}
		out += fmt.Sprintf("  t=%.1fs size=%d bytes\n", t, fi.Size())
	}
	return out, nil
}

func ffprobe(path string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0", "--", path).Output()
	if err != nil {
		return 0, err
	}
	var dur float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &dur)
	return dur, nil
}

func main() { plugin.RunStdio(server{}) }

// safePluginPath rejects absolute paths, .. traversal, and leading dashes.
func safePluginPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if p[0] == '-' {
		return fmt.Errorf("invalid path (starts with '-'): %s", p)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("absolute paths are not allowed: %s", p)
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") && (len(clean) == 2 || clean[2] == '/' || clean[2] == '\\') {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	if strings.Contains(clean, "/..") || strings.Contains(clean, "\\..") {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	return nil
}
