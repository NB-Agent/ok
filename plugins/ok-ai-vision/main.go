// @ok/ai-vision — MCP plugin: Image and video analysis.
package main

import (
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
)

func main() {
	s := &mcpServer{name: "ok-ai-vision", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int
	Message string
}
type mcpServer struct{ name, version string }

func (s *mcpServer) handle(req jsonRPC) jsonRPC {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(schemas())}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.exec(params.Name, params.Arguments)
		if err != nil {
			return jsonRPC{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32000, Message: err.Error()}}
		}
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})}
	default:
		return jsonRPC{JSONRPC: "2.0", ID: id}
	}
}

func schemas() map[string]any {
	return map[string]any{"tools": []map[string]any{
		{"name": "image_read", "description": "Read an image file — metadata and visual content",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string"},
			}, "required": []string{"path"}}},
		{"name": "video_analyze", "description": "Analyze a video file — extract key frames and describe",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"path":         map[string]any{"type": "string"},
				"interval_sec": map[string]any{"type": "integer"},
				"max_frames":   map[string]any{"type": "integer"},
			}, "required": []string{"path"}}},
	}}
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
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
			"-i", path, "-vframes", "1", "-q:v", "2", frame, "-y")
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
		"-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, err
	}
	var dur float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &dur)
	return dur, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
