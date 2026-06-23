// Package anthropic implements the Anthropic Claude Messages API provider.
// It self-registers under the "anthropic" kind.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/provider"
)

func init() {
	provider.Register("anthropic", New)
}

func New(cfg provider.Config) (provider.Provider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("anthropic: base_url is required for provider %q", cfg.Name)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("anthropic: model is required for provider %q", cfg.Name)
	}
	name := cfg.Name
	if name == "" {
		name = "anthropic"
	}
	keyEnv := cfg.APIKeyEnv
	if keyEnv == "" {
		if v, ok := cfg.Extra["api_key_env"].(string); ok {
			keyEnv = v
		}
	}
	return &client{
		name:    name,
		apiKey:  cfg.APIKey,
		keyEnv:  keyEnv,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		http: &http.Client{
			Timeout: 15 * time.Minute,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   30 * time.Second,
				ResponseHeaderTimeout: 120 * time.Second,
				MaxConnsPerHost:       10,
				MaxIdleConns:          10,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}, nil
}

type client struct {
	name    string
	apiKey  string
	keyEnv  string
	baseURL string
	model   string
	http    *http.Client
}

func (c *client) Name() string { return c.name }

func (c *client) sendOpts() provider.SendOptions {
	return provider.SendOptions{
		Provider:   c.name,
		KeyEnv:     c.keyEnv,
		KeyPresent: c.apiKey != "",
	}
}

func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal: %w", c.name, err)
	}
	newReq := func(ctx context.Context) (*http.Request, error) {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("x-api-key", c.apiKey)
		hreq.Header.Set("anthropic-version", "2023-06-01")
		return hreq, nil
	}
	resp, err := provider.SendWithRetry(ctx, c.http, c.sendOpts(), newReq)
	if err != nil {
		return nil, err
	}
	out := make(chan provider.Chunk, 256)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
			}
		}()
		c.readStream(ctx, resp, out)
	}()
	return out, nil
}

// ─── wire types ──────────────────────────────────────────────

type acMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type cacheControl struct {
	Type string `json:"type"`
}

var ephemeralCache = cacheControl{Type: "ephemeral"}

// acContentBlock is a typed content block for Anthropic API.
type acContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type sysBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type acTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type acReq struct {
	Model       string     `json:"model"`
	MaxTokens   int        `json:"max_tokens"`
	System      []sysBlock `json:"system,omitempty"`
	Messages    []acMsg    `json:"messages"`
	Tools       []acTool   `json:"tools,omitempty"`
	Stream      bool       `json:"stream"`
	Temperature float64    `json:"temperature,omitempty"`
}

func (c *client) buildRequest(req provider.Request) acReq {
	var sys []sysBlock
	var msgs []acMsg

	for _, m := range req.Messages {
		switch m.Role {
		case provider.RoleSystem:
			sys = append(sys, sysBlock{Type: "text", Text: m.Content, CacheControl: &ephemeralCache})
			continue
		case provider.RoleTool:
			// Tool result: wrap content in a tool_result block array.
			result := acContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   mustMarshal(m.Content),
			}
			arr := safeMarshal([]acContentBlock{result})
			msgs = append(msgs, acMsg{Role: "user", Content: arr})
			continue
		case provider.RoleAssistant:
			var blks []acContentBlock
			if m.Content != "" {
				blks = append(blks, acContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blks = append(blks, acContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: json.RawMessage(tc.Arguments),
				})
			}
			raw := safeMarshal(blks)
			msgs = append(msgs, acMsg{Role: "assistant", Content: raw})
			continue
		case provider.RoleUser:
			raw := safeMarshal(m.Content)
			msgs = append(msgs, acMsg{Role: "user", Content: raw})
		default: // unknown role — skip
		}
	}

	tools := make([]acTool, 0, len(req.Tools))
	for i, t := range req.Tools {
		is := t.Parameters
		if is == nil || string(is) == "null" {
			is = json.RawMessage(`{"type":"object"}`)
		}
		at := acTool{Name: t.Name, Description: t.Description, InputSchema: is}
		// Only cache the first tool entry (largest, most stable prefix).
		if i == 0 && len(req.Tools) > 1 {
			at.CacheControl = &ephemeralCache
		}
		tools = append(tools, at)
	}

	mt := req.MaxTokens
	if mt <= 0 {
		mt = 4096
	}

	ar := acReq{
		Model: c.model, MaxTokens: mt, Messages: msgs, Tools: tools,
		Stream: true, Temperature: req.Temperature,
	}
	if len(sys) > 0 {
		ar.System = sys
	}
	return ar
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return json.RawMessage(data)
}

func safeMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return json.RawMessage(data)
}

// ─── SSE ─────────────────────────────────────────────────────

type acUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens,omitempty"`
}

type blkSt struct {
	idx   int
	typ   string
	tID   string
	tName string
	jAcc  bytes.Buffer
	done  bool
}

func (c *client) readStream(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer log.Close("anthropic response", resp.Body)
	defer close(out)

	var blks []*blkSt
	getB := func(idx int) *blkSt {
		for _, b := range blks {
			if b.idx == idx {
				return b
			}
		}
		b := &blkSt{idx: idx}
		blks = append(blks, b)
		return b
	}

	var lu *provider.Usage
	var sr string

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	emit := func(v provider.Chunk) bool {
		select {
		case out <- v:
			return true
		case <-ctx.Done():
			return false
		}
	}

	malformed := 0

	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if len(data) == 0 {
			continue
		}
		dataBytes := data // already []byte from scanner.Bytes + TrimSpace

		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(dataBytes, &ev); err != nil {
			malformed++
			if malformed >= 10 {
				emit(provider.Chunk{Type: provider.ChunkError,
					Err: fmt.Errorf("%s: too many malformed SSE frames", c.name)})
				return
			}
			continue
		}
		malformed = 0

		switch ev.Type {
		case "ping":

		case "message_start":
			var s struct {
				Message struct {
					Usage *acUsage `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(dataBytes, &s) == nil && s.Message.Usage != nil {
				lu = toUsage(s.Message.Usage)
			}

		case "content_block_start":
			var s struct {
				Index int             `json:"index"`
				Block json.RawMessage `json:"content_block"`
			}
			if json.Unmarshal(dataBytes, &s) != nil {
				continue
			}
			var raw struct {
				Type string `json:"type"`
				ID   string `json:"id,omitempty"`
				Name string `json:"name,omitempty"`
				Text string `json:"text,omitempty"`
			}
			if json.Unmarshal(s.Block, &raw) != nil {
				continue
			}
			b := getB(s.Index)
			b.typ = raw.Type
			if raw.Type == "tool_use" {
				b.tID = raw.ID
				b.tName = raw.Name
				if !emit(provider.Chunk{Type: provider.ChunkToolCallStart,
					ToolCall: &provider.ToolCall{ID: raw.ID, Name: raw.Name}}) {
					return
				}
			} else if raw.Text != "" {
				if !emit(provider.Chunk{Type: provider.ChunkText, Text: raw.Text}) {
					return
				}
			}

		case "content_block_delta":
			var s struct {
				Index int             `json:"index"`
				Delta json.RawMessage `json:"delta"`
			}
			if json.Unmarshal(dataBytes, &s) != nil {
				continue
			}
			var dt struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
				PJ   string `json:"partial_json,omitempty"`
			}
			if json.Unmarshal(s.Delta, &dt) != nil {
				continue
			}
			b := getB(s.Index)
			switch dt.Type {
			case "text_delta":
				if !emit(provider.Chunk{Type: provider.ChunkText, Text: dt.Text}) {
					return
				}
			case "input_json_delta":
				b.jAcc.WriteString(dt.PJ)
			default:
				// unknown content block delta — ignore
			}

		case "content_block_stop":
			// BUGFIX: Parse the index field to identify which block stopped,
			// rather than iterating over all blocks and picking the first !done.
			// Without this, multi-tool scenarios can mark wrong blocks.
			var stopIdx struct {
				Index int `json:"index"`
			}
			if json.Unmarshal(dataBytes, &stopIdx) != nil {
				continue
			}
			b := getB(stopIdx.Index)
			b.done = true
			if b.typ == "tool_use" {
				args := b.jAcc.Bytes()
				var v any
				if json.Unmarshal(args, &v) != nil {
					args = []byte("{}")
				}
				if !emit(provider.Chunk{Type: provider.ChunkToolCall,
					ToolCall: &provider.ToolCall{ID: b.tID, Name: b.tName, Arguments: string(args)}}) {
					return
				}
			}

		case "message_delta":
			var s struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *acUsage `json:"usage"`
			}
			if json.Unmarshal(dataBytes, &s) == nil {
				if s.Delta.StopReason != "" {
					sr = s.Delta.StopReason
				}
				if s.Usage != nil {
					lu = toUsage(s.Usage)
				}
			}

		case "message_stop":
			if lu != nil {
				lu.FinishReason = mapStop(sr)
				if !emit(provider.Chunk{Type: provider.ChunkUsage, Usage: lu}) {
					return
				}
			}
			emit(provider.Chunk{Type: provider.ChunkDone})
			return

		case "error":
			var s struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(dataBytes, &s) == nil && s.Error.Message != "" {
				emit(provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: %s", c.name, s.Error.Message)})
				return
			}
		default:
			// unknown SSE event type — ignore
		}
	}

	for _, b := range blks {
		if !b.done && b.typ == "tool_use" {
			args := b.jAcc.Bytes()
			if len(args) == 0 || !json.Valid(args) {
				args = []byte("{}")
			}
			if !emit(provider.Chunk{Type: provider.ChunkToolCall,
				ToolCall: &provider.ToolCall{ID: b.tID, Name: b.tName, Arguments: string(args)}}) {
				return
			}
		}
	}

	if err := sc.Err(); err != nil {
		emit(provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: read: %w", c.name, err)})
		return
	}
	emit(provider.Chunk{Type: provider.ChunkDone})
}

func toUsage(u *acUsage) *provider.Usage {
	hit := u.CacheRead
	miss := u.InputTokens
	if hit > 0 && miss >= hit {
		miss -= hit
	}
	if hit == 0 {
		miss = u.InputTokens
	}
	return &provider.Usage{
		PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens,
		TotalTokens:    u.InputTokens + u.OutputTokens,
		CacheHitTokens: hit, CacheMissTokens: miss,
	}
}

func mapStop(r string) string {
	switch r {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return r
	}
}
