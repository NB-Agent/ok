// Package mcpserver exposes OK's tool registry as an MCP server over stdio.
// Any MCP client (Claude Code, Cursor, VS Code extensions) can connect and
// call OK's built-in tools (bash, read_file, write_file, edit_file, grep,
// glob, web_fetch, task, ask, todo_write, complete_step) plus all configured
// plugin tools — turning OK into a drop-in code-agent backend.
//
// Protocol: JSON-RPC 2.0, one message per line (NDJSON), matching the MCP
// spec (https://spec.modelcontextprotocol.io). stdin/stdout are the JSON-RPC
// channel; all diagnostics go to stderr.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

// ToolProvider is the minimal interface the MCP server needs from the caller.
// The caller (cli/mcp_serve.go) builds the full registry from config.
type ToolProvider interface {
	// ListTools returns every available tool's name + description + schema.
	ListTools() []ToolInfo
	// CallTool executes a tool by name with the given JSON args.
	CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error)
}

// ToolInfo describes one tool for the MCP tools/list response.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// RegistryAdapter adapts a *tool.Registry to ToolProvider.
type RegistryAdapter struct {
	reg *tool.Registry
}

func NewRegistryAdapter(reg *tool.Registry) *RegistryAdapter {
	return &RegistryAdapter{reg: reg}
}

func (a *RegistryAdapter) ListTools() []ToolInfo {
	names := a.reg.Names()
	out := make([]ToolInfo, 0, len(names))
	for _, n := range names {
		t, ok := a.reg.Get(n)
		if !ok {
			continue
		}
		out = append(out, ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return out
}

func (a *RegistryAdapter) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	t, ok := a.reg.Get(name)
	if !ok {
		return "", false, fmt.Errorf("unknown tool: %s", name)
	}
	result, err := t.Execute(ctx, args)
	if err != nil {
		return result, false, err
	}
	return result, t.ReadOnly(), nil
}

// ─── JSON-RPC types ────────────────────────────────────────────

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	errParse    = -32700
	errInvalid  = -32600
	errMethod   = -32601
	errInternal = -32603
)

// ─── MCP event types ──────────────────────────────────────────

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      serverInfo      `json:"serverInfo"`
}

type mcpCapabilities struct {
	Tools     *toolsCap     `json:"tools,omitempty"`
	Resources *resourcesCap `json:"resources,omitempty"`
	Prompts   *promptsCap   `json:"prompts,omitempty"`
}

type toolsCap struct {
	ListChanged bool `json:"listChanged"`
}

type resourcesCap struct {
	ListChanged bool `json:"listChanged"`
	Subscribe   bool `json:"subscribe"`
}

type promptsCap struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type mcpToolCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type mcpResourceResult struct {
	Resources []mcpResource `json:"resources"`
}

type mcpPrompt struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Arguments   []mcpArg `json:"arguments,omitempty"`
}

type mcpArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// mcpPromptResult is reserved for future prompt support.
//
//lint:ignore U1000 reserved for future prompt support
type mcpPromptResult struct {
	Messages []mcpPromptMsg `json:"messages"`
}

//nolint:unused
type mcpPromptMsg struct {
	Role    string     `json:"role"`
	Content mcpContent `json:"content"`
}

// ─── Server ────────────────────────────────────────────────────

// Server runs the MCP stdio server. It reads JSON-RPC requests from r,
// writes responses to w, and dispatches tool calls through the ToolProvider.
type Server struct {
	r     io.Reader
	w     io.Writer
	tools ToolProvider

	// wmu serialises writes so concurrent handler goroutines don't interleave.
	wmu sync.Mutex
	enc *json.Encoder
}

// New creates an MCP server over the given reader/writer pair.
func New(r io.Reader, w io.Writer, tools ToolProvider) *Server {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Server{r: r, w: w, tools: tools, enc: enc}
}

// Run reads requests until EOF and dispatches them. Returns on clean EOF
// or a read error.
func (s *Server) Run(ctx context.Context) error {
	sc := bufio.NewScanner(s.r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg rpcMsg
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			s.writeError(nil, errParse, "parse error")
			continue
		}
		s.dispatch(ctx, &msg)
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, msg *rpcMsg) {
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"

	switch msg.Method {
	case "initialize":
		if !hasID {
			s.writeError(nil, errInvalid, "initialize requires id")
			return
		}
		// Return capabilities
		params := struct {
			ProtocolVersion string `json:"protocolVersion"`
		}{}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			fmt.Fprintf(os.Stderr, "mcpserver: unmarshal initialize params: %v\n", err)
			s.writeError(msg.ID, errInvalid, "invalid params")
			return
		}

		result := mcpInitializeResult{
			ProtocolVersion: params.ProtocolVersion,
			Capabilities: mcpCapabilities{
				Tools:     &toolsCap{ListChanged: false},
				Resources: &resourcesCap{ListChanged: false, Subscribe: false},
				Prompts:   &promptsCap{ListChanged: false},
			},
			ServerInfo: serverInfo{
				Name:    "ok",
				Version: "cli", // filled by caller if desired
			},
		}
		s.writeResult(msg.ID, result)

	case "notifications/initialized":
		// No response needed for notifications

	case "tools/list":
		if !hasID {
			return
		}
		tools := s.tools.ListTools()
		mcpTools := make([]mcpTool, 0, len(tools))
		for _, t := range tools {
			mcpTools = append(mcpTools, mcpTool(t))
		}
		s.writeResult(msg.ID, map[string]any{"tools": mcpTools})

	case "tools/call":
		if !hasID {
			return
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.Name == "" {
			s.writeError(msg.ID, errInvalid, "tools/call requires name and arguments")
			return
		}
		output, readOnly, err := s.tools.CallTool(ctx, params.Name, params.Arguments)
		res := mcpToolCallResult{
			Content: []mcpContent{{Type: "text", Text: output}},
		}
		if err != nil {
			res.IsError = true
			// Append error to content so the model sees it
			res.Content[0].Text = fmt.Sprintf("error: %v\n%s", err, output)
		}
		_ = readOnly // could be used for logging; not sent in MCP response
		s.writeResult(msg.ID, res)

	case "resources/list":
		if !hasID {
			return
		}
		// No custom resources by default; caller can extend.
		s.writeResult(msg.ID, mcpResourceResult{Resources: []mcpResource{}})

	case "prompts/list":
		if !hasID {
			return
		}
		// No custom prompts by default.
		s.writeResult(msg.ID, map[string]any{"prompts": []mcpPrompt{}})

	case "ping":
		// MCP health check — returns empty result to confirm server is alive.
		if hasID {
			s.writeResult(msg.ID, map[string]any{})
		}

	case "resources/read":
		if !hasID {
			return
		}
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.URI == "" {
			s.writeError(msg.ID, errInvalid, "resources/read requires uri")
			return
		}
		s.writeError(msg.ID, errMethod, "resource not found: "+params.URI)

	case "resources/templates/list":
		if !hasID {
			return
		}
		// No URI templates by default.
		s.writeResult(msg.ID, map[string]any{"resourceTemplates": []any{}})

	case "prompts/get":
		if !hasID {
			return
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.Name == "" {
			s.writeError(msg.ID, errInvalid, "prompts/get requires name")
			return
		}
		s.writeError(msg.ID, errMethod, "prompt not found: "+params.Name)

	case "completion/complete":
		if !hasID {
			return
		}
		// Return empty completion list — extendable by callers that want to
		// provide argument completion for their tools.
		s.writeResult(msg.ID, map[string]any{"completion": map[string]any{"values": []any{}}})

	case "notifications/cancelled":
		// Notification — no response. Receivers should cancel in-flight
		// requests matching the params.requestId, but the stdio transport
		// is serial per-request so this is a no-op in the current design.

	case "logging/setLevel":
		// Notification — accept and ignore. Future: could wire to the log
		// package to dynamically adjust verbosity.

	default:
		if hasID {
			s.writeError(msg.ID, errMethod, "method not found: "+msg.Method)
		}
	}
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	data, err := json.Marshal(result)
	if err != nil {
		s.writeError(id, errInternal, "marshal: "+err.Error())
		return
	}
	resp := rpcMsg{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
	s.wmu.Lock()
	if err := s.enc.Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "mcpserver: encode response: %v\n", err)
	}
	s.wmu.Unlock()
}

func (s *Server) writeError(id json.RawMessage, code int, msg string) {
	resp := rpcMsg{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
	s.wmu.Lock()
	if err := s.enc.Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "mcpserver: encode error: %v\n", err)
	}
	s.wmu.Unlock()
}
