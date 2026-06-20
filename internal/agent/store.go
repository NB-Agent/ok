package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// StoreEntry is one agent listing in the community store index.
type StoreEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	URL         string `json:"url"`
	Tools       string `json:"tools"`
}

const DefaultStoreURL = "https://raw.githubusercontent.com/colbymchenry/ok-agents/main/index.json"

// storeClient is a hardened HTTP client: no redirects, strict timeout.
var storeClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func storeFetch(ctx context.Context, url string) (*http.Response, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("store: only HTTPS URLs are allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("store: request: %w", err)
	}
	resp, err := storeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store: fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("store: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// ListAgents fetches the agent store index.
func ListAgents(ctx context.Context, storeURL string) ([]StoreEntry, error) {
	if storeURL == "" {
		storeURL = DefaultStoreURL
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := storeFetch(ctx, storeURL)
	if err != nil {
		return nil, fmt.Errorf("fetch agent store: %w", err)
	}
	defer log.Close("store response", resp.Body)

	var entries []StoreEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode agent store: %w", err)
	}
	return entries, nil
}

// InstallAgent downloads an agent definition from URL and saves to .ok/agents/.
func InstallAgent(ctx context.Context, projectRoot, name, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := storeFetch(ctx, url)
	if err != nil {
		return fmt.Errorf("download agent: %w", err)
	}
	defer log.Close("store response", resp.Body)

	dir := filepath.Join(projectRoot, ".ok", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create agents dir: %w", err)
	}

	// Resolve symlinks on dir so traversal checks work even if projectRoot is a symlink.
	realDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		realDir = resolved
	}

	path := filepath.Join(dir, name+".md")
	// Prevent directory traversal: the resolved path must be inside the agents dir.
	if abs, err := filepath.Abs(path); err != nil || !strings.HasPrefix(abs, filepath.Clean(realDir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid agent name %q: path traversal denied", name)
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("agent %q already exists at %s (delete first or use a different name)", name, path)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // max 1MB
	if err != nil {
		return fmt.Errorf("read agent: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write agent: %w", err)
	}
	fmt.Printf("Installed agent %q to %s\n", name, path)
	return nil
}

// PublishAgent formats an AgentDef as a shareable Markdown string.
func PublishAgent(def AgentDef) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "description: %s\n", def.Description)
	if def.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", def.Model)
	}
	if len(def.Tools) > 0 {
		b.WriteString("tools: [")
		for i, t := range def.Tools {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(t)
		}
		b.WriteString("]\n")
	}
	if def.PermissionMode != "" {
		fmt.Fprintf(&b, "permission_mode: %s\n", def.PermissionMode)
	}
	b.WriteString("---\n\n")
	b.WriteString(def.SystemPrompt)
	b.WriteString("\n")
	return b.String()
}
