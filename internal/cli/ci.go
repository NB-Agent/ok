package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/boot"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/log"
)

// ciResult is the JSON output structure for ok ci.
type ciResult struct {
	Version    string     `json:"version"`
	Success    bool       `json:"success"`
	Result     string     `json:"result,omitempty"`
	Usage      *usageInfo `json:"usage,omitempty"`
	ToolCalls  int        `json:"tool_calls"`
	ToolErrors int        `json:"tool_errors"`
	DurationMs int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheHitTokens   int `json:"cache_hit_tokens"`
}

// ciSink collects events for JSON output.
type ciSink struct {
	version    string
	start      time.Time
	usage      *usageInfo
	toolCalls  int
	toolErrors int
	result     string
	success    bool
	errMsg     string
	done       chan struct{}
}

func newCiSink(version string) *ciSink {
	return &ciSink{
		version: version,
		start:   time.Now(),
		done:    make(chan struct{}),
	}
}

func (s *ciSink) Emit(e *event.Event) {
	switch e.Kind {
	case event.Usage:
		if e.Usage != nil {
			s.usage = &usageInfo{
				PromptTokens:     e.Usage.PromptTokens,
				CompletionTokens: e.Usage.CompletionTokens,
				CacheHitTokens:   e.Usage.CacheHitTokens,
			}
		}
	case event.ToolDispatch:
		s.toolCalls++
	case event.ToolResult:
		if e.Tool.Output != "" && strings.Contains(e.Tool.Output, "error:") {
			s.toolErrors++
		}
	case event.TurnDone:
		if e.Err != nil {
			s.errMsg = e.Err.Error()
			s.success = false
		} else {
			s.success = true
		}
		close(s.done)
	}
}

// ciRun implements the "ok ci" subcommand.
func ciRun(args []string, version string) int {
	fs := flag.NewFlagSet("ci", flag.ContinueOnError)
	model := fs.String("model", "", "model name (default: config default_model)")
	format := fs.String("format", "json", "output format: json or text")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "ci: task prompt is required")
		return 2
	}
	prompt := strings.TrimSpace(fs.Arg(0))

	sink := newCiSink(version)
	ctrl, err := boot.Build(context.Background(), boot.Options{ // CLI entry point — no parent context
		Model:      *model,
		RequireKey: true,
		Sink:       sink,
	})
	if err != nil {
		res := ciResult{
			Version:    version,
			Success:    false,
			DurationMs: time.Since(sink.start).Milliseconds(),
			Error:      err.Error(),
		}
		outputCIResult(res, *format)
		return 1
	}

	ctrl.Submit(prompt)
	<-sink.done

	res := ciResult{
		Version:    version,
		Success:    sink.success,
		Result:     sink.result,
		Usage:      sink.usage,
		ToolCalls:  sink.toolCalls,
		ToolErrors: sink.toolErrors,
		DurationMs: time.Since(sink.start).Milliseconds(),
		Error:      sink.errMsg,
	}
	outputCIResult(res, *format)

	if !sink.success {
		return 1
	}
	return 0
}

func outputCIResult(res ciResult, format string) {
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			log.Error("ci: json encode", "err", err)
			os.Exit(1)
		}
	} else {
		if res.Error != "" {
			fmt.Fprintln(os.Stderr, "error:", res.Error)
		}
		fmt.Println(res.Result)
	}
}
