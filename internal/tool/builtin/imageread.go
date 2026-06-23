package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(imageRead{}) }

// imageRead reads an image file and returns its metadata. When a multimodal model
// is available (gpt-4o, claude-3.5-sonnet, gemini-pro-vision), the returned
// content is passed as a vision attachment; until then, the model sees file stats
// and can decide whether the image is what it's looking for.
type imageRead struct{}

func (imageRead) Name() string { return "image-read" }

func (imageRead) Description() string {
	return "Read an image file — metadata (dimensions, format, size) and visual content for multimodal models. Supports PNG, JPEG, GIF."
}

func (imageRead) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`)
}

func (imageRead) ReadOnly() bool { return true }

func (imageRead) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	f, err := os.Open(p.Path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", p.Path, err)
	}
	defer log.Close("image file", f)

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", p.Path, err)
	}

	// Decode image config (doesn't read pixel data)
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w (is this a supported image format? PNG/JPEG/GIF are supported)", p.Path, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Image: %s\n", filepath.Base(p.Path)))
	b.WriteString(fmt.Sprintf("- Format: %s\n", format))
	b.WriteString(fmt.Sprintf("- Dimensions: %d×%d pixels\n", cfg.Width, cfg.Height))
	b.WriteString(fmt.Sprintf("- Size: %s\n", humanizeBytes(info.Size())))
	b.WriteString(fmt.Sprintf("- Path: %s\n", p.Path))

	b.WriteString("\n## Visual Analysis\n")
	b.WriteString("To analyze this image, use a multimodal model (gpt-4o, claude-3.5-sonnet, gemini-pro-vision). ")
	b.WriteString("When available, the image content will be passed directly to the model's vision context. ")
	b.WriteString("For text-only models, use the metadata above to decide if this is the correct image, ")
	b.WriteString("then describe what you expect to see to me — I'll confirm whether it matches.\n")

	return b.String(), nil
}

func humanizeBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
