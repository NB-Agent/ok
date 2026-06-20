// Package plugin is OK's MCP client. It connects to external MCP servers and
// adapts their tools to the tool.Tool interface, so the agent treats plugin
// tools and built-ins uniformly. The wire protocol is JSON-RPC 2.0 in every
// case; only the transport differs (stdio subprocess, Streamable HTTP, or the
// legacy HTTP+SSE). A transport interface hides that difference so the MCP-level
// logic — handshake, tools/list, tools/call — is written once.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

// ProtocolVersion is the MCP revision OK advertises during initialize.
// Exported so external commands (e.g. ok-plugin-example) can reference
// the single source of truth.
const ProtocolVersion = "2024-11-05"

// Spec declares an external MCP server. Type selects the transport: "stdio"
// (default) runs Command/Args/Env as a subprocess; "http" / "streamable-http"
// and "sse" connect to URL with optional static Headers.
type Spec struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	// Dir, when set, is the working directory of a stdio subprocess. Empty means
	// inherit ok's cwd (the default for user-configured plugins). It exists
	// for cwd-aware servers like CodeGraph, which detect the project from the
	// directory they are launched in — they must be pinned to the project root.
	Dir string
}

// transport carries JSON-RPC messages to and from one MCP server. call sends a
// request and returns its result (correlating by id internally); notify sends a
// fire-and-forget notification; close releases resources. Server-initiated
// notifications are routed through onNotify when set.
type transport interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	setNotifyHandler(fn func(method string, params json.RawMessage))
	close()
}

// Host owns the running plugin connections and closes them together. It also
// aggregates the prompts and resources discovered across servers, which the
// chat UI surfaces (prompts as slash commands, resources as @-references).
// Safe for concurrent use.
type Host struct {
	mu        sync.Mutex
	clients   []*Client
	prompts   []Prompt
	resources []Resource
}

// Prompts returns a copy of every MCP prompt discovered across connected servers.
func (h *Host) Prompts() []Prompt {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Prompt, len(h.prompts))
	copy(out, h.prompts)
	return out
}

// Resources returns a copy of every MCP resource discovered across connected servers.
func (h *Host) Resources() []Resource {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Resource, len(h.resources))
	copy(out, h.resources)
	return out
}

// ServerNames returns the connected servers' names, in connection order.
func (h *Host) ServerNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, len(h.clients))
	for i, c := range h.clients {
		names[i] = c.name
	}
	return names
}

// ReadResource reads a resource uri from the named server. It is how the chat
// UI resolves an @server:uri reference — the uri need not be one listed by
// resources/list (servers may expose templated uris), so we read it directly.
func (h *Host) ReadResource(ctx context.Context, server, uri string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		if c.name == server {
			return c.readResource(ctx, uri)
		}
	}
	return "", fmt.Errorf("no MCP server named %q", server)
}

// StartAll connects every plugin, performs the MCP handshake, and returns the
// union of their tools (namespaced "mcp__<server>__<tool>"). On any failure it
// tears down everything started so far. The caller must Close the Host.
//
// For stdio plugins, subprocess lifetime is bound to ctx (via
// exec.CommandContext): canceling ctx kills the children and unblocks reads.
func StartAll(ctx context.Context, specs []Spec) (*Host, []tool.Tool, error) {
	h := &Host{}
	var tools []tool.Tool
	for _, s := range specs {
		c, err := start(ctx, s)
		if err != nil {
			h.Close()
			return nil, nil, fmt.Errorf("start plugin %q: %w", s.Name, err)
		}
		h.clients = append(h.clients, c)

		ts, err := c.listTools(ctx)
		if err != nil {
			h.Close()
			return nil, nil, fmt.Errorf("list tools from %q: %w", s.Name, err)
		}
		tools = append(tools, ts...)
		c.toolCount = len(ts)

		// Prompts and resources are auxiliary: only fetched when the server
		// advertised the capability, and a listing error is tolerated (skipped)
		// rather than failing the whole session over a non-essential surface.
		if c.hasPrompts {
			if ps, perr := c.listPrompts(ctx); perr == nil {
				h.prompts = append(h.prompts, ps...)
			}
		}
		if c.hasResources {
			if rs, rerr := c.listResources(ctx); rerr == nil {
				h.resources = append(h.resources, rs...)
			}
		}
	}
	return h, tools, nil
}

// Close terminates all plugin connections.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.close()
	}
}

// Client is one MCP server connection: a name plus the transport carrying its
// JSON-RPC. The MCP-level methods (initialize, listTools, …) are transport-
// agnostic — they go through t.
type Client struct {
	name string
	t    transport

	// onNotify is the callback for server-initiated notifications.
	// Set by Host when starting the client to enable re-discovery.
	onNotify func(method string, params json.RawMessage)

	// Capabilities advertised by the server at initialize. prompts/list and
	// resources/list are only called when advertised, so we never provoke a
	// "method not found" on a tools-only server.
	hasPrompts   bool
	hasResources bool

	toolCount int    // tools discovered, for /mcp status
	transport string // declared transport type, for /mcp status ("stdio"/"http")

	closeOnce sync.Once // guards t.close() against double-close panic
}

// ServerStatus summarizes one connected server for the /mcp command.
type ServerStatus struct {
	Name      string
	Transport string
	Tools     int
	Prompts   int
	Resources int
	Healthy   bool // false when the last health check failed
}

// HealthResult reports the outcome of pinging one server.
type HealthResult struct {
	Name  string
	Err   string // empty when healthy
	Tools int
	Alive bool
}

// HealthCheck pings every connected server and returns results. Unhealthy
// servers are not automatically restarted — callers decide policy.
func (h *Host) HealthCheck(ctx context.Context) []HealthResult {
	h.mu.Lock()
	clients := make([]*Client, len(h.clients))
	copy(clients, h.clients)
	h.mu.Unlock()

	results := make([]HealthResult, len(clients))
	for i, c := range clients {
		r := HealthResult{Name: c.name, Tools: c.toolCount}
		if err := c.ping(ctx); err != nil {
			r.Err = err.Error()
		} else {
			r.Alive = true
		}
		results[i] = r
	}
	return results
}

// Servers returns a status summary per connected server, in connection order.
func (h *Host) Servers() []ServerStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ServerStatus, 0, len(h.clients))
	for _, c := range h.clients {
		s := ServerStatus{Name: c.name, Transport: c.transport, Tools: c.toolCount}
		for _, p := range h.prompts {
			if p.Server == c.name {
				s.Prompts++
			}
		}
		for _, r := range h.resources {
			if r.Server == c.name {
				s.Resources++
			}
		}
		out = append(out, s)
	}
	return out
}

// NewHost returns an empty Host. Boot always constructs one — even with no
// plugins configured — so servers can be hot-added later via Add (the `/mcp add`
// command), which keeps the controller's host pointer stable for the session.
func NewHost() *Host { return &Host{} }

// hasLocked reports whether a server with this name is already connected.
// Caller must hold h.mu.
func (h *Host) hasLocked(name string) bool {
	for _, c := range h.clients {
		if c.name == name {
			return true
		}
	}
	return false
}

// Add connects one server live: it performs the MCP handshake, discovers the
// server's tools (and prompts/resources when advertised), appends it to the
// host, and returns its namespaced tools for the caller to register. ctx bounds a
// stdio child's lifetime, so pass the session-scoped context — not a per-turn one
// — or the subprocess dies when that turn ends. Errors if the name is taken.
func (h *Host) Add(ctx context.Context, s Spec) ([]tool.Tool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hasLocked(s.Name) {
		return nil, fmt.Errorf("server %q is already connected", s.Name)
	}
	c, err := start(ctx, s)
	if err != nil {
		return nil, err
	}
	ts, err := c.listTools(ctx)
	if err != nil {
		c.close()
		return nil, fmt.Errorf("list tools: %w", err)
	}
	c.toolCount = len(ts)
	h.clients = append(h.clients, c)
	if c.hasPrompts {
		if ps, perr := c.listPrompts(ctx); perr == nil {
			h.prompts = append(h.prompts, ps...)
		}
	}
	if c.hasResources {
		if rs, rerr := c.listResources(ctx); rerr == nil {
			h.resources = append(h.resources, rs...)
		}
	}
	return ts, nil
}

// Remove disconnects the named server and drops its prompts/resources, returning
// the namespaced tool-name prefix ("mcp__<server>__") the caller unregisters from
// the tool registry, and whether the server was connected.
func (h *Host) Remove(name string) (toolPrefix string, found bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	idx := -1
	for i, c := range h.clients {
		if c.name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	h.clients[idx].close()
	h.clients = append(h.clients[:idx], h.clients[idx+1:]...)

	keptP := h.prompts[:0]
	for _, p := range h.prompts {
		if p.Server != name {
			keptP = append(keptP, p)
		}
	}
	h.prompts = keptP

	keptR := h.resources[:0]
	for _, r := range h.resources {
		if r.Server != name {
			keptR = append(keptR, r)
		}
	}
	h.resources = keptR

	return "mcp__" + normalizeName(name) + "__", true
}

func start(ctx context.Context, s Spec) (*Client, error) {
	t, err := newTransport(ctx, s)
	if err != nil {
		return nil, err
	}
	tt := strings.ToLower(strings.TrimSpace(s.Type))
	if tt == "" {
		tt = "stdio"
	}
	c := &Client{name: s.Name, t: t, transport: tt}
	if err := c.initialize(ctx); err != nil {
		c.close()
		return nil, err
	}
	// Wire server→client notification handling.
	t.setNotifyHandler(c.handleNotification)
	return c, nil
}

// TransportFactory creates a transport from a Spec.  RegisterTransport
// allows third-party transport implementations to register without
// modifying newTransport's switch statement.
type TransportFactory func(ctx context.Context, s Spec) (transport, error)

var transportRegistry = map[string]TransportFactory{}

// RegisterTransport registers a transport factory under the given type name.
// Called from init() in transport implementation packages.  A duplicate
// panics at init time — that is a compile-time wiring mistake.
func RegisterTransport(kind string, fn TransportFactory) {
	lower := strings.ToLower(strings.TrimSpace(kind))
	if _, dup := transportRegistry[lower]; dup {
		panic(fmt.Sprintf("plugin: duplicate transport %q", kind))
	}
	transportRegistry[lower] = fn
}

// newTransport builds the transport for a spec's declared type. Empty / unknown
// defaults to stdio.  Registered transports are checked first; built-in
// transports (stdio/http/sse) provide the fallback.
func newTransport(ctx context.Context, s Spec) (transport, error) {
	kind := strings.ToLower(strings.TrimSpace(s.Type))
	if fn, ok := transportRegistry[kind]; ok {
		return fn(ctx, s)
	}
	switch kind {
	case "", "stdio":
		return newStdioTransport(ctx, s)
	case "http", "streamable-http", "streamable_http":
		return newHTTPTransport(s)
	case "sse":
		return newSSETransport(ctx, s)
	default:
		return nil, fmt.Errorf("unknown transport type %q (want stdio|http|sse — or register via plugin.RegisterTransport)", s.Type)
	}
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.t.call(ctx, method, params)
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.t.notify(ctx, method, params)
}

func (c *Client) close() { c.closeOnce.Do(func() { c.t.close() }) }

// handleNotification processes server-initiated MCP notifications.
// Currently handles tools/list_changed (re-discovers tools). Other
// notifications (prompts/list_changed, resources/list_changed, cancelled,
// logging/message) are noted but deferred.
func (c *Client) handleNotification(method string, params json.RawMessage) {
	if c.onNotify != nil {
		c.onNotify(method, params)
	}
	switch method {
	case "notifications/tools/list_changed":
		// Forwarded to Host callback for re-discovery via onNotify above.
	default:
	}
}

func (c *Client) initialize(ctx context.Context) error {
	res, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ok", "version": "dev"},
	})
	if err != nil {
		return err
	}
	// Record which optional capabilities the server advertises. Presence of the
	// key (even with an empty object) signals support.
	var ir struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &ir); err != nil {
		// Malformed capabilities block — not fatal (we just won't discover
		// prompts/resources), but warn so the operator can check the server.
		fmt.Fprintf(os.Stderr, "plugin %q: initialize: decode capabilities: %v\n", c.name, err)
	}
	_, c.hasPrompts = ir.Capabilities["prompts"]
	_, c.hasResources = ir.Capabilities["resources"]

	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Annotations carries MCP's optional tool hints. We read readOnlyHint: a
	// plugin that declares a tool read-only opts it into OK's parallel-dispatch
	// path and the permission layer's "readers default to allow". Absent
	// annotations stay false — opaque by default, never trusted implicitly.
	Annotations *struct {
		ReadOnlyHint bool `json:"readOnlyHint"`
	} `json:"annotations"`
}

// ping sends an MCP ping to check server liveness. Returns nil when the
// server responds; non-nil when the server is unreachable or times out.
func (c *Client) ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", map[string]any{})
	return err
}

func (c *Client) listTools(ctx context.Context) ([]tool.Tool, error) {
	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("plugin %q: decode tools/list: %w", c.name, err)
	}

	tools := make([]tool.Tool, 0, len(out.Tools))
	for _, t := range out.Tools {
		tools = append(tools, &remoteTool{
			client:   c,
			name:     toolName(c.name, t.Name),
			rawName:  t.Name,
			desc:     t.Description,
			schema:   t.InputSchema,
			readOnly: t.Annotations != nil && t.Annotations.ReadOnlyHint,
		})
	}
	return tools, nil
}

// toolName builds the model-visible namespaced name "mcp__<server>__<tool>",
// matching Claude Code. Spaces in either part are normalised to underscores so
// the name is a clean identifier the model can call.
func toolName(server, raw string) string {
	return "mcp__" + normalizeName(server) + "__" + normalizeName(raw)
}

// normalizeName ensures the name contains only characters valid for an LLM
// provider tool/function name: [a-zA-Z0-9_-]. Other characters (dots, colons,
// unicode, etc.) are replaced with underscores to avoid 400 errors from APIs
// that enforce this regex pattern (e.g. DeepSeek, OpenAI).
func normalizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// --- JSON-RPC message types (shared by every transport) ---

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"` // omitted for notifications (id 0 unused)
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// --- remote tool adapter ---

type remoteTool struct {
	client   *Client
	name     string // namespaced "mcp__<server>__<tool>"
	rawName  string // original name for tools/call
	desc     string
	schema   json.RawMessage
	readOnly bool // from the tool's MCP readOnlyHint annotation
}

func (t *remoteTool) Name() string        { return t.name }
func (t *remoteTool) Description() string { return t.desc }

// ReadOnly reflects the tool's MCP readOnlyHint annotation. It defaults to
// false (opaque — we can't inspect a remote tool's side effects), so plugins
// opt into parallel-batch dispatch and the permission layer's reader-default
// by explicitly declaring readOnlyHint: true in tools/list.
func (t *remoteTool) ReadOnly() bool { return t.readOnly }

func (t *remoteTool) Schema() json.RawMessage {
	if len(t.schema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.schema
}

func (t *remoteTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var argMap map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	res, err := t.client.call(ctx, "tools/call", map[string]any{
		"name":      t.rawName,
		"arguments": argMap,
	})
	if err != nil {
		return "", err
	}
	return parseToolResult(res)
}

// parseToolResult flattens an MCP tools/call result into plain text.
func parseToolResult(res json.RawMessage) (string, error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", fmt.Errorf("decode tool result: %w", err)
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	text := sb.String()
	if out.IsError {
		return text, fmt.Errorf("plugin tool reported error: %s", text)
	}
	return text, nil
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
