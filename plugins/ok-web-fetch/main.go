// @ok/web-fetch — MCP plugin: Fetch URLs and return text content.
// SSRF-protected: refuses private/link-local IPs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	s := &mcpServer{name: "ok-web-fetch", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(context.Background(), req)
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	name    string
	version string
}

func (s *mcpServer) handle(ctx context.Context, req jsonRPC) jsonRPC {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"tools": []map[string]any{
				{
					"name":        "web_fetch",
					"description": "Fetch a URL and return its text content. HTML is reduced to readable text; JSON/markdown pass through verbatim.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{"type": "string", "description": "Absolute URL beginning with http:// or https://"},
						},
						"required": []string{"url"},
					},
				},
			},
		})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.executeTool(ctx, params.Name, params.Arguments)
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

func (s *mcpServer) executeTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if name != "web_fetch" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		URL string `json:"url"`
	}
	json.Unmarshal(args, &p)
	if p.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	return fetch(ctx, p.URL)
}

const (
	fetchTimeout = 15 * time.Second
	fetchMaxRead = 1 << 20 // 1 MiB
)

var urlRe = regexp.MustCompile(`^https?://`)

func fetch(ctx context.Context, rawURL string) (string, error) {
	if !urlRe.MatchString(rawURL) {
		return "", fmt.Errorf("url must start with http:// or https://")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}

	// SSRF check at dial time
	client := &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					host = addr
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if isPrivateIP(ip.IP) {
						return nil, fmt.Errorf("ssrf: refused connection to %s (%s)", host, ip.IP)
					}
				}
				return (&net.Dialer{Timeout: fetchTimeout}).DialContext(ctx, network, addr)
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("bad request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "web-fetch resp close: %v\n", err)
		}
	}()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return "", fmt.Errorf("404: page not found at %s (may have moved or been deleted)", rawURL)
	}
	if resp.StatusCode == 403 {
		return "", fmt.Errorf("403: access denied at %s (likely blocks automated requests)", rawURL)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d at %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxRead))
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	content := string(body)
	ct := resp.Header.Get("Content-Type")

	// HTML → plain text
	if strings.Contains(ct, "text/html") || strings.HasSuffix(u.Path, ".html") || strings.HasSuffix(u.Path, ".htm") {
		content = stripHTML(content)
	}

	return fmt.Sprintf("URL: %s\nStatus: %d\nContent-Type: %s\nSize: %d bytes\n\n%s",
		rawURL, resp.StatusCode, ct, len(body), truncate(content, 32000)), nil
}

func stripHTML(html string) string {
	// Remove script/style (RE2 does not support backreferences)
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = re.ReplaceAllString(html, "")
	// Remove tags
	re = regexp.MustCompile(`(?is)<[^>]+>`)
	html = re.ReplaceAllString(html, " ")
	// Collapse whitespace
	re = regexp.MustCompile(`\s+`)
	html = re.ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n\n[… truncated at %d bytes]", n)
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false // loopback allowed
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true
		case ip4[0] == 192 && ip4[1] == 168:
			return true
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
			return true // CGNAT
		case ip4[0] == 169 && ip4[1] == 254:
			return true // link-local unicast (blocked)
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
