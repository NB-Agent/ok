// @ok/web-fetch — MCP plugin: Fetch URLs and return text content. (migrated to plugin.StdioServer)
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

	"github.com/NB-Agent/ok/internal/plugin"
)

// --- harness-backed server ---

type server struct{}

func (server) Info() (string, string) { return "ok-web-fetch", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{
			Name:        "web_fetch",
			Description: "Fetch a URL and return its text content. HTML is reduced to readable text; JSON/markdown pass through verbatim.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "Absolute URL beginning with http:// or https://"},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (server) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
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

func main() { plugin.RunStdio(server{}) }
