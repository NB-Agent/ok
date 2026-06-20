package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// sseTransport speaks MCP's legacy HTTP+SSE transport (2024-11-05):
// a persistent GET connection carries server→client messages (notifications
// and an endpoint-discovery event), while client→client requests are POSTed
// to the url discovered from the GET stream's "endpoint" event.
//
// This transport is deprecated upstream — Streamable HTTP (`type = "http"`)
// is the recommended replacement. SSE is kept for compatibility with older
// MCP servers that have not yet adopted the new transport.
type sseTransport struct {
	name    string
	url     string
	headers map[string]string
	client  *http.Client

	mu       sync.Mutex
	nextID   int
	session  string // Mcp-Session-Id, captured from POST responses
	endpoint string // POST url discovered from GET stream

	cancel context.CancelFunc // cancels the background GET reader
	done   chan struct{}      // closed when the background reader exits

	onNotify func(method string, params json.RawMessage)
}

func newSSETransport(ctx context.Context, s Spec) (*sseTransport, error) {
	if s.URL == "" {
		return nil, fmt.Errorf("sse plugin %q: url is required", s.Name)
	}
	t := &sseTransport{
		name:    s.Name,
		url:     s.URL,
		headers: s.Headers,
		client:  ssrfSafeClient(30 * time.Second),
		done:    make(chan struct{}),
	}

	// Open the GET connection and discover the POST endpoint.
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ep, sid, err := t.connect(connectCtx)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", s.Name, err)
	}
	t.endpoint = ep
	if sid != "" {
		t.session = sid
	}

	// Spawn a background goroutine to keep the GET stream alive and drain
	// server→client messages (notifications are discarded). A canceled
	// context kills the child, which unblocks the read loop.
	// Background read loop is independent of the initialization context.
	bgCtx, bgCancel := context.WithCancel(context.Background())
	t.cancel = bgCancel
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
			}
		}()
		t.readLoop(bgCtx)
	}()

	return t, nil
}

// connect does one GET request and blocks until it receives the SSE
// `event: endpoint` line that carries the POST URL. Returns the endpoint,
// any Mcp-Session-Id header, and an error. On success the response body
// is still open — readLoop takes over reading it.
func (t *sseTransport) connect(ctx context.Context) (endpoint, session string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode/100 != 2 {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if readErr != nil {
			return "", "", fmt.Errorf("sse connect: http %d (body unreadable: %w)", resp.StatusCode, readErr)
		}
		return "", "", fmt.Errorf("sse connect: http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	session = resp.Header.Get("Mcp-Session-Id")

	// Read the SSE stream until we find the endpoint event or timeout.
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxHTTPBody))
	sc.Buffer(make([]byte, 0, 64*1024), maxHTTPBody)

	var eventType string
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			// Event boundary.
			if eventType == "endpoint" && data.Len() > 0 {
				ep := strings.TrimSpace(data.String())
				// The body is still being read by sc.Scanner; we need to
				// hand it off. Since bufio.Scanner buffers, we've already
				// consumed past the endpoint event. Spin up a new GET
				// in readLoop instead and close this one.
				if err := resp.Body.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "plugin sse: close body after endpoint: %v\n", err)
				}
				return ep, session, nil
			}
			eventType = ""
			data.Reset()
			continue
		}
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			eventType = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "plugin sse: close body after scan err: %v\n", cerr)
		}
		return "", "", fmt.Errorf("sse connect read: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "plugin sse: close body: %v\n", err)
	}
	return "", "", fmt.Errorf("sse connect: no endpoint event received")
}

// readLoop maintains the persistent GET connection, reconnecting on failure.
// Server→client notifications are discarded — OK is a tools/prompts/
// resources consumer, not a sampling/roots provider.
func (t *sseTransport) readLoop(ctx context.Context) {
	defer close(t.done)
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "plugin sse: readLoop panic: %v\n", r)
		}
	}()
	backoff := 500 * time.Millisecond
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := t.drainGET(ctx)
		if err == nil {
			return // clean shutdown
		}
		// Reconnect after backoff.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// drainGET opens a GET connection and reads SSE events until ctx is canceled
// or the connection closes. Server notifications are discarded.
func (t *sseTransport) drainGET(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}

	// Use an SSRF-safe client without an HTTP-level timeout for the long-lived
	// SSE stream — the dialer still has the protection and timeout.
	streamClient := ssrfSafeStreamClient(30 * time.Second)
	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer log.Close("plugin sse response", resp.Body)

	if resp.StatusCode/100 != 2 {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		errMsg := "(cannot read body)"
		if readErr == nil {
			errMsg = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("sse readLoop: http %d: %s", resp.StatusCode, errMsg)
	}
	t.mu.Lock()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.session = sid
	}
	t.mu.Unlock()

	// Drain the stream — we only care about keeping it alive; notifications
	// are discarded.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxHTTPBody)); err != nil {
		fmt.Fprintf(os.Stderr, "plugin sse: drain readLoop body: %v\n", err)
	}
	return nil
}

func (t *sseTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.endpoint == "" {
		return nil, fmt.Errorf("plugin %q: sse endpoint not yet discovered", t.name)
	}

	t.nextID++
	id := t.nextID
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	resp, err := t.doPost(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %s: %w", t.name, method, err)
	}
	defer log.Close("plugin sse response", resp.Body)
	t.captureSessionLocked(resp)

	if resp.StatusCode/100 != 2 {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			b = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("plugin %q: %s: http %d: %s", t.name, method, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		return t.readSSEResponseLocked(resp.Body, id)
	}
	return decodeRPCResult(resp.Body, t.name)
}

func (t *sseTransport) notify(ctx context.Context, method string, params any) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.endpoint == "" {
		return fmt.Errorf("plugin %q: sse endpoint not yet discovered", t.name)
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := t.doPost(ctx, body)
	if err != nil {
		return fmt.Errorf("plugin %q: %s: %w", t.name, method, err)
	}
	defer log.Close("plugin sse response", resp.Body)
	t.captureSessionLocked(resp)
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxHTTPBody)); err != nil {
		fmt.Fprintf(os.Stderr, "plugin sse: drain call body: %v\n", err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("plugin %q: %s: http %d", t.name, method, resp.StatusCode)
	}
	return nil
}

func (t *sseTransport) close() {
	if t.cancel != nil {
		t.cancel()
	}
	<-t.done
	t.client.CloseIdleConnections()
}

func (t *sseTransport) setNotifyHandler(fn func(method string, params json.RawMessage)) {
	t.onNotify = fn
}

func (t *sseTransport) doPost(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	return t.client.Do(req)
}

func (t *sseTransport) captureSessionLocked(resp *http.Response) {
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.session = sid
	}
}

// readSSEResponseLocked scans an SSE body for the JSON-RPC response matching id,
// skipping notifications. Caller holds t.mu.
func (t *sseTransport) readSSEResponseLocked(body io.Reader, id int) (json.RawMessage, error) {
	sc := bufio.NewScanner(io.LimitReader(body, maxHTTPBody))
	sc.Buffer(make([]byte, 0, 64*1024), maxHTTPBody)

	var data bytes.Buffer
	match := func() (json.RawMessage, bool, error) {
		if data.Len() == 0 {
			return nil, false, nil
		}
		payload := data.Bytes()
		data.Reset()
		var resp rpcResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return nil, false, nil //nolint:nilerr // non-JSON-RPC messages are silently skipped
		}
		if resp.ID != id {
			return nil, false, nil
		}
		if resp.Error != nil {
			return nil, false, fmt.Errorf("plugin %q: %w", t.name, resp.Error)
		}
		return resp.Result, true, nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if res, ok, err := match(); err != nil || ok {
				return res, err
			}
			continue
		}
		if v, found := strings.CutPrefix(line, "data:"); found {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("plugin %q: read SSE: %w", t.name, err)
	}
	if res, ok, err := match(); err != nil || ok {
		return res, err
	}
	return nil, fmt.Errorf("plugin %q: SSE stream ended without a response to id %d", t.name, id)
}
