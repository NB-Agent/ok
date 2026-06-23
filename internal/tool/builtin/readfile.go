// Package builtin provides OK's compile-time built-in tools. Each tool
// self-registers via init(); main blank-imports this package to wire them in.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(readFile{}) }

// readFile reads a text file. roots, when non-empty, confines reads to the
// workspace (see confineRead); the zero value registered at init is unconfined
// and is overridden per run when Workspace.ReadRoots is set. workDir, when
// non-empty, is the directory a relative path resolves against (see resolveIn).
type readFile struct {
	roots   []string
	workDir string
}

const (
	readFileDefaultLimit = 2000     // lines returned when limit is unset
	readFileBinaryPeek   = 8 * 1024 // bytes scanned for a NUL to flag binary
)

func (readFile) Name() string { return "read_file" }

func (readFile) Description() string {
	return "Read a text file with offset/limit. Line-numbered output. Supports paging for large files."
}

func (readFile) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"limit":{"minimum":1,"type":"integer"},"offset":{"minimum":0,"type":"integer"},"path":{"type":"string"}},"required":["path"],"type":"object"}`)
}

func (readFile) ReadOnly() bool { return true }

func (r readFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	p.Path = resolveIn(r.workDir, p.Path)
	if err := confineRead(r.roots, p.Path); err != nil {
		return "", err
	}
	resolved, err := realPath(p.Path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p.Path, err)
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	if p.Limit <= 0 {
		p.Limit = readFileDefaultLimit
	}

	// A directory can be os.Open'd but not read as text.
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file — use the ls tool to list it, or read a specific file inside it", resolved)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}
	defer log.Close("read file", f)

	// Refuse binary files up front. A NUL byte anywhere in the leading 8 KB
	// is the cheapest reliable signal — UTF-16 / textual config files don't
	// embed NULs in the way executables, archives, or images do.
	peek := make([]byte, readFileBinaryPeek)
	n, _ := io.ReadFull(f, peek) // err is io.ErrUnexpectedEOF for small files — expected, n is correct
	if bytes.IndexByte(peek[:n], 0) >= 0 {
		return "", fmt.Errorf("binary file %s (NUL byte detected); use `bash hexdump` or another tool", p.Path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek %s: %w", p.Path, err)
	}

	// Scan up to offset+limit+1 lines (the extra is just to know whether
	// trimming a trailer is warranted). 1 MB per-line cap matches what other
	// scanners in this package allow — well above any reasonable source line.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	upTo := p.Offset + p.Limit + 1

	var collected []string
	var collectedBytes int
	const readFileMaxBytes = 10 * 1024 * 1024 // 10 MB total cap
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo > p.Offset && len(collected) < p.Limit {
			line := scanner.Text()
			collected = append(collected, line)
			collectedBytes += len(line)
			if collectedBytes > readFileMaxBytes {
				break
			}
		}
		if lineNo >= upTo {
			break
		}
	}
	// Drain any remainder to learn the true total without buffering the rest.
	remaining := 0
	for scanner.Scan() {
		remaining++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}
	totalSeen := lineNo + remaining

	if totalSeen == 0 {
		return "(empty file)", nil
	}
	if len(collected) == 0 {
		return fmt.Sprintf("(offset %d is past EOF — file has %d lines)", p.Offset, totalSeen), nil
	}

	// Right-align line numbers to the largest one we'll print, so the arrow
	// "→" column lines up. Add 1 for the 1-based display.
	maxShown := p.Offset + len(collected)
	w := len(fmt.Sprint(maxShown))

	var b strings.Builder
	for i, line := range collected {
		fmt.Fprintf(&b, "%*d→%s\n", w, p.Offset+i+1, line)
	}
	more := totalSeen - (p.Offset + len(collected))
	if more > 0 {
		fmt.Fprintf(&b, "\n[%d more line(s); pass offset=%d to continue]\n",
			more, p.Offset+len(collected))
	}
	return b.String(), nil
}
