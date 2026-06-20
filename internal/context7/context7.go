// Package context7 integrates the Context7 MCP server
// (https://github.com/upstash/context7) as an auto-discovered built-in plugin.
// Context7 provides up-to-date library documentation for LLMs, eliminating
// API hallucinations from stale training data.
//
// When the CONTEXT7_API_KEY environment variable is set, the MCP server at
// https://mcp.context7.com/mcp is automatically registered. Without the key,
// the feature is silently unavailable — agents fall back to their training data.
//
// A free API key can be obtained at https://context7.com/dashboard.
package context7

import (
	"os"

	"github.com/NB-Agent/ok/internal/plugin"
)

// ServerURL is the Context7 hosted MCP endpoint.
const ServerURL = "https://mcp.context7.com/mcp"

// HeaderKey is the HTTP header used to pass the API key.
const HeaderKey = "CONTEXT7_API_KEY"

// EnvVar is the environment variable that carries the API key.
const EnvVar = "CONTEXT7_API_KEY"

// Spec returns a plugin.Spec for the Context7 MCP server when an API key is
// configured, or (Spec{}, false) when the key is absent. The server is always
// enabled when a key is present — there is no per-project opt-out because the
// MCP tools are only called when the agent explicitly invokes them.
func Spec() (plugin.Spec, bool) {
	key := os.Getenv(EnvVar)
	if key == "" {
		return plugin.Spec{}, false
	}
	return plugin.Spec{
		Name: "context7",
		Type: "http",
		URL:  ServerURL,
		Headers: map[string]string{
			HeaderKey: key,
		},
	}, true
}
