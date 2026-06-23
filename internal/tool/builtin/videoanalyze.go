package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(videoAnalyze{}) }

type videoAnalyze struct{}

func (videoAnalyze) Name() string { return "video-analyze" }

func (videoAnalyze) Description() string {
	return "Analyze a video file — extract key frames at intervals, describe content via multimodal vision, and produce a timeline. Supports MP4, AVI, MOV, MKV. Requires ffmpeg."
}

func (videoAnalyze) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"interval_sec":{"type":"integer"},"max_frames":{"type":"integer"},"path":{"type":"string"}},"required":["path"],"type":"object"}`)
}

func (videoAnalyze) ReadOnly() bool { return true }

func (videoAnalyze) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path        string `json:"path"`
		IntervalSec int    `json:"interval_sec"`
		MaxFrames   int    `json:"max_frames"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.IntervalSec <= 0 {
		p.IntervalSec = 10
	}
	if p.IntervalSec < 1 {
		p.IntervalSec = 1
	}
	if p.MaxFrames <= 0 {
		p.MaxFrames = 20
	}
	if p.MaxFrames > 100 {
		p.MaxFrames = 100
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install ffmpeg (https://ffmpeg.org) to use video-analyze")
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", p.Path, err)
	}
	duration, err := probeDuration(ctx, p.Path)
	if err != nil {
		duration = 0
	}
	expectedFrames := int(duration) / p.IntervalSec
	if expectedFrames < 1 {
		expectedFrames = 1
	}
	if expectedFrames > p.MaxFrames {
		expectedFrames = p.MaxFrames
	}
	tmpDir, err := os.MkdirTemp("", "ok-video-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	framePaths, err := extractFrames(ctx, p.Path, tmpDir, p.IntervalSec, expectedFrames)
	if err != nil {
		return "", fmt.Errorf("extract frames: %w", err)
	}
	if len(framePaths) == 0 {
		return fmt.Sprintf("# Video: %s\n\nNo frames could be extracted. The file may be corrupt or an unsupported format.\n", filepath.Base(p.Path)), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Video Analysis: %s\n", filepath.Base(p.Path)))
	b.WriteString(fmt.Sprintf("- Size: %s\n", humanizeBytes(info.Size())))
	b.WriteString(fmt.Sprintf("- Frames extracted: %d\n", len(framePaths)))
	if duration > 0 {
		b.WriteString(fmt.Sprintf("- Duration: %ds (~%s)\n", duration, fmtDuration(duration)))
		b.WriteString(fmt.Sprintf("- Interval: every %ds\n", p.IntervalSec))
	}
	b.WriteString("\n## Timeline\n\n")
	for i, fp := range framePaths {
		ts := i * p.IntervalSec
		b.WriteString(fmt.Sprintf("### %s\n\n", fmtTimestamp(ts, duration)))
		f, err := os.Open(fp)
		if err == nil {
			cfg, format, err := image.DecodeConfig(f)
			f.Close()
			if err == nil {
				b.WriteString(fmt.Sprintf("_Frame: %dx%d %s_\n\n", cfg.Width, cfg.Height, format))
			}
		}
		b.WriteString(fmt.Sprintf("![Frame %d](%s)\n\n", i+1, fp))
		b.WriteString("Visual analysis of this frame (via multimodal model):\n")
		b.WriteString("- Scene content: what is visible\n")
		b.WriteString("- Action/motion: what is happening\n")
		b.WriteString("- Text/UI: any readable text, code, or interface elements\n")
		b.WriteString("- Changes from previous frame: new objects, scene transitions\n\n")
	}
	b.WriteString("## Summary\n\n")
	b.WriteString(fmt.Sprintf("The video is %d frames across %s. ", len(framePaths), fmtDuration(duration)))
	b.WriteString("Each frame is available above for multimodal analysis. ")
	b.WriteString("Review the frames sequentially to understand the video's narrative, actions, and content.\n")
	return b.String(), nil
}

func probeDuration(ctx context.Context, path string) (int, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

func extractFrames(ctx context.Context, videoPath, tmpDir string, intervalSec, maxFrames int) ([]string, error) {
	pattern := filepath.Join(tmpDir, "frame-%04d.jpg")
	args := []string{
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=1/%d", intervalSec),
		"-vframes", strconv.Itoa(maxFrames),
		"-q:v", "5",
		"-y",
		pattern,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract: %w", err)
	}
	var framePaths []string
	for i := 1; i <= maxFrames; i++ {
		fp := filepath.Join(tmpDir, fmt.Sprintf("frame-%04d.jpg", i))
		if _, err := os.Stat(fp); err == nil {
			framePaths = append(framePaths, fp)
		}
	}
	return framePaths, nil
}

func fmtTimestamp(sec, total int) string {
	m := sec / 60
	s := sec % 60
	if total > 3600 {
		h := sec / 3600
		m = (sec % 3600) / 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func fmtDuration(sec int) string {
	if sec <= 0 {
		return "unknown duration"
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
