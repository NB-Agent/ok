// Package openai implements the OpenAI-compatible /chat/completions provider.
// It self-registers under the "openai" kind, so DeepSeek, MiMo, and any other
// OpenAI-compatible endpoint are just config instances rather than code.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/provider"
)

func init() {
	provider.Register("openai", New)
}

// New builds an OpenAI-compatible provider from a resolved config.
func New(cfg provider.Config) (provider.Provider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("openai: base_url is required for provider %q", cfg.Name)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai: model is required for provider %q", cfg.Name)
	}
	name := cfg.Name
	if name == "" {
		name = "openai"
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
				ExpectContinueTimeout: 1 * time.Second,
				MaxConnsPerHost:       10,
				MaxIdleConns:          10,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}, nil
}

var doneMarker = []byte("[DONE]")

type client struct {
	name    string
	apiKey  string
	keyEnv  string // api_key_env name, surfaced in auth errors
	baseURL string
	model   string
	http    *http.Client
}

func (c *client) Name() string { return c.name }

func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.name, err)
	}

	resp, err := c.sendWithRetry(ctx, body)
	if err != nil {
		return nil, err
	}

	out := make(chan provider.Chunk, 256) // buffered: early-caller-exit drains up to 256 before goroutine exits
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
				out <- provider.Chunk{Type: provider.ChunkError,
					Err: fmt.Errorf("openai: readStream panic: %v", r)}
			}
		}()
		c.readStream(ctx, resp, out)
	}()
	return out, nil
}

// sendWithRetry POSTs the request body and returns the streaming response,
// retrying on transient network errors and retryable HTTP statuses (408, 429,
// 5xx) with exponential backoff + jitter. Retries only cover the connection +
// header phase; once we hand the response to readStream, mid-stream failures
// surface as ChunkError without retry, since the model has already started
// emitting tokens we'd otherwise duplicate.
func (c *client) sendWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1))*500*time.Millisecond + time.Duration(rand.IntN(250))*time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %w", c.name, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := c.http.Do(httpReq)
		if err != nil {
			if !isTransientErr(err) {
				return nil, fmt.Errorf("%s: request failed: %w", c.name, err)
			}
			lastErr = fmt.Errorf("%s: request failed: %w", c.name, err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		msg, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if cerr := resp.Body.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "provider %s: close body: %v\n", c.name, cerr)
		}
		if err != nil {
			msg = []byte("(body unreadable)")
		}
		// A rejected key is a configuration problem, not a transient one — give
		// an actionable error instead of dumping the raw status body.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &provider.AuthError{Provider: c.name, KeyEnv: c.keyEnv, Status: resp.StatusCode}
		}
		statusErr := fmt.Errorf("%s: status %d: %s", c.name, resp.StatusCode, strings.TrimSpace(string(msg)))
		if !isRetryableStatus(resp.StatusCode) {
			return nil, statusErr
		}
		// Respect the server's Retry-After header on rate limits (429).
		if resp.StatusCode == http.StatusTooManyRequests {
			if s := resp.Header.Get("Retry-After"); s != "" {
				if sec, err := strconv.Atoi(s); err == nil && sec > 0 && sec <= 120 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(sec) * time.Second):
					}
					lastErr = statusErr
					continue
				}
			}
		}
		lastErr = statusErr
	}
	return nil, lastErr
}

// isRetryableStatus returns true for HTTP status codes a transient backoff can
// reasonably recover from: 408 (request timeout), 429 (rate limit), and 5xx.
// 4xx other than 408/429 (auth, validation, not-found) are caller bugs and
// won't fix themselves on retry.
func isRetryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

// isTransientErr classifies HTTP client errors. Canceled is caller intent and
// never retried. DeadlineExceeded IS retried — the HTTP client's internal
// timeout (now 15 min) can fire when the server is merely slow, not dead, and
// sendWithRetry's maxAttempts=3 cap prevents infinite loops.
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func (c *client) buildRequest(req provider.Request) chatRequest {
	msgs := make([]chatMessage, len(req.Messages))
	for i, m := range req.Messages {
		// reasoning_content is deliberately NOT sent back: it's a response-only
		// field. DeepSeek accepts it but counts it as ordinary prompt input
		// (measured ~500 extra tokens per turn on a reasoner chain), and the
		// OpenAI-compatible convention is not to echo it. The session still keeps
		// it (for display/archive); we just don't pay to re-upload it every turn.
		cm := chatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			wire := chatToolCall{ID: tc.ID, Type: "function"}
			wire.Function.Name = tc.Name
			wire.Function.Arguments = tc.Arguments
			cm.ToolCalls = append(cm.ToolCalls, wire)
		}
		msgs[i] = cm
	}

	tools := make([]chatTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, chatTool{
			Type:     "function",
			Function: chatFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}

	return chatRequest{
		Model:         c.model,
		Messages:      msgs,
		Tools:         tools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
	}
}

// readStream parses the SSE stream, emits text deltas live, accumulates tool-call
// fragments internally via strings.Builder (O(n) not O(n²)), and emits complete
// ToolCalls (by index) when done. Each call also gets a ChunkToolCallStart the
// moment its name is known, so a frontend can show the tool card while the
// arguments are still streaming.
func (c *client) readStream(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer log.Close("openai response", resp.Body)
	defer close(out)

	acc := map[int]*provider.ToolCall{}
	argBld := map[int]*strings.Builder{}
	started := map[int]bool{}
	var order []int
	var lastFinishReason string
	var malformedStreak int

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	emit := func(v provider.Chunk) bool {
		select {
		case out <- v:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if bytes.Equal(data, doneMarker) {
			break
		}

		var sr streamResponse
		if err := json.Unmarshal(data, &sr); err != nil {
			malformedStreak++
			if malformedStreak >= 5 {
				emit(provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: too many malformed SSE frames", c.name)})
				return
			}
			continue
		}
		malformedStreak = 0
		if sr.Error != nil {
			emit(provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: %s", c.name, sr.Error.Message)})
			return
		}
		if len(sr.Choices) > 0 && sr.Choices[0].FinishReason != nil && *sr.Choices[0].FinishReason != "" {
			lastFinishReason = *sr.Choices[0].FinishReason
		}
		if sr.Usage != nil {
			u := normaliseUsage(sr.Usage)
			u.FinishReason = lastFinishReason
			if !emit(provider.Chunk{Type: provider.ChunkUsage, Usage: u}) {
				return
			}
		}
		if len(sr.Choices) == 0 {
			continue
		}

		delta := sr.Choices[0].Delta
		if delta.ReasoningContent != "" {
			if !emit(provider.Chunk{Type: provider.ChunkReasoning, Text: delta.ReasoningContent}) {
				return
			}
		}
		if delta.Content != "" {
			if !emit(provider.Chunk{Type: provider.ChunkText, Text: delta.Content}) {
				return
			}
		}
		for _, tc := range delta.ToolCalls {
			cur, ok := acc[tc.Index]
			if !ok {
				cur = &provider.ToolCall{}
				acc[tc.Index] = cur
				argBld[tc.Index] = &strings.Builder{}
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Name = tc.Function.Name
			}
			argBld[tc.Index].WriteString(tc.Function.Arguments)
			// Signal the call's start the moment its name is known, so a frontend
			// can show the tool card immediately rather than only after its
			// (possibly large) arguments finish streaming.
			if !started[tc.Index] && cur.Name != "" {
				started[tc.Index] = true
				if !emit(provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: cur.ID, Name: cur.Name}}) {
					return
				}
			}
		}
	}

	// Flush builders into ToolCall.Arguments once, after the stream ends.
	for idx, b := range argBld {
		acc[idx].Arguments = b.String()
	}

	if err := scanner.Err(); err != nil {
		// Stream interrupted — emit error without incomplete tool calls.
		emit(provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: read stream: %w", c.name, err)})
		return
	}

	sort.Ints(order)
	for _, idx := range order {

		if !emit(provider.Chunk{Type: provider.ChunkToolCall, ToolCall: acc[idx]}) {
			return
		}
	}
	emit(provider.Chunk{Type: provider.ChunkDone})
}

// normaliseUsage folds the two cache-hit shapes the OpenAI-compatible ecosystem
// uses into a single Usage: DeepSeek puts prompt_cache_{hit,miss}_tokens at the
// top of usage; OpenAI and MiMo put it nested under prompt_tokens_details.
// Whichever side reports non-zero wins; miss is derived when only hit is given.
// Reasoning tokens land in completion_tokens_details on thinking-mode models.
func normaliseUsage(u *wireUsage) *provider.Usage {
	hit := u.PromptCacheHitTokens
	miss := u.PromptCacheMissTokens
	if hit == 0 && u.PromptTokensDetails != nil {
		hit = u.PromptTokensDetails.CachedTokens
	}
	if miss == 0 && hit > 0 && u.PromptTokens > hit {
		miss = u.PromptTokens - hit
	}
	reasoning := 0
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	return &provider.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
		ReasoningTokens:  reasoning,
	}
}

// --- OpenAI-compatible wire protocol ---

type chatRequest struct {
	Model         string         `json:"model"`
	Tools         []chatTool     `json:"tools,omitempty"`
	Messages      []chatMessage  `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role string `json:"role"`
	// content is always serialized, even when empty: an assistant turn that is
	// pure tool_calls (no preamble text) has empty content, and DeepSeek's
	// strict deserializer rejects a message missing the field ("missing field
	// `content`"). An empty string satisfies presence and is accepted by every
	// OpenAI-compatible backend for all roles (unlike null, which some reject
	// for a tool message).
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	// no reasoning_content field: it is a response-only signal and is never sent
	// back upstream (see buildRequest) — re-uploading it is paid prompt input.
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type streamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// wireUsage covers both DeepSeek's top-level cache fields and the
// OpenAI/MiMo nested details — normaliseUsage chooses whichever side
// reports values.
type wireUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails   *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}
