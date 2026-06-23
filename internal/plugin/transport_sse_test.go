package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseTestServer returns an httptest server that speaks the legacy MCP HTTP+SSE
// transport: GET returns an SSE stream with an endpoint event, then keeps the
// connection open sending periodic comments; POST to /mcp handles JSON-RPC.
func sseTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	const sessionID = "sse-sess-1"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flusher", 500)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.WriteHeader(200)
			// Send the endpoint discovery event.
			fmt.Fprintf(w, "event: endpoint\ndata: http://%s/mcp\n\n", r.Host)
			flusher.Flush()
			// Keep the connection open with periodic comments so the
			// readLoop doesn't see EOF immediately.
			timer := time.NewTicker(200 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
					fmt.Fprint(w, ": keepalive\n\n")
					flusher.Flush()
				}
			}

		case http.MethodPost:
			w.Header().Set("Mcp-Session-Id", sessionID)
			var req struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if req.ID == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{
					"protocolVersion": ProtocolVersion,
					"serverInfo":      map[string]any{"name": "sse-test", "version": "1"},
					"capabilities": map[string]any{
						"prompts":   map[string]any{},
						"resources": map[string]any{},
					},
				}
			case "tools/list":
				result = map[string]any{"tools": []map[string]any{{
					"name":        "sse_echo",
					"description": "Echo over SSE.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"msg": map[string]any{"type": "string"}},
					},
					"annotations": map[string]any{"readOnlyHint": true},
				}}}
			case "tools/call":
				var p struct {
					Arguments struct {
						Msg string `json:"msg"`
					} `json:"arguments"`
				}
				_ = json.Unmarshal(req.Params, &p)
				result = map[string]any{"content": []map[string]any{
					{"type": "text", "text": "sse: " + p.Arguments.Msg},
				}}
			case "prompts/list":
				result = map[string]any{"prompts": []map[string]any{{
					"name": "greet", "description": "A greeting prompt.",
				}}}
			case "resources/list":
				result = map[string]any{"resources": []map[string]any{{
					"uri": "doc://readme", "name": "README",
				}}}
			}
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
			b, _ := json.Marshal(resp)
			w.Write(b)
		}
	}))
}

func TestSSETransportEndToEnd(t *testing.T) {
	srv := sseTestServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	host, tools, err := StartAll(ctx, []Spec{{
		Name: "sse-srv",
		Type: "sse",
		URL:  srv.URL,
	}})
	if err != nil {
		t.Fatalf("StartAll SSE: %v", err)
	}
	defer host.Close()

	if len(tools) != 1 || tools[0].Name() != "mcp__sse-srv__sse_echo" {
		t.Fatalf("tools = %v, want [mcp__sse-srv__sse_echo]", names(tools))
	}
	if !tools[0].ReadOnly() {
		t.Error("readOnlyHint not honored over SSE")
	}

	got, err := tools[0].Execute(ctx, json.RawMessage(`{"msg":"hello sse"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "sse: hello sse" {
		t.Errorf("Execute = %q, want %q", got, "sse: hello sse")
	}

	// Prompts + resources must be discovered.
	prompts := host.Prompts()
	if len(prompts) != 1 || prompts[0].Name != "mcp__sse-srv__greet" {
		t.Errorf("prompts = %+v, want one greet", prompts)
	}
	resources := host.Resources()
	if len(resources) != 1 || resources[0].URI != "doc://readme" {
		t.Errorf("resources = %+v, want one doc://readme", resources)
	}
}

func TestSSETransportBadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// An address nothing listens on.
	_, _, err := StartAll(ctx, []Spec{{Name: "bad", Type: "sse", URL: "http://127.0.0.1:1"}})
	if err == nil {
		t.Fatal("unreachable SSE server should error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should name the server, got %v", err)
	}
}
