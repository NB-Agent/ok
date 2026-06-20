// Package registry provides a universal plugin registry: agents can discover,
// download, install, and manage MCP plugin servers at runtime without recompilation.
// This makes the agent truly universal — any capability can be added on demand.
//
// Registry layout (~/.ok/plugins/):
//
//	plugins/
//	  index.json          — cached registry index
//	  installed.json      — local install manifest
//	  <name>/             — one directory per plugin
//	    plugin.json       — manifest (name, version, entrypoint, tools, prompts)
//	    ...               — plugin files
//
// Usage from the agent:
//
//	/plugin install <name-or-url>   — install a plugin
//	/plugin list                    — list installed
//	/plugin search <query>          — search registry
//	/plugin remove <name>           — uninstall
//	/plugin update <name>           — update to latest
package plugin

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
)

// RegistryIndex is the cached manifest from the official plugin registry.
type RegistryIndex struct {
	Version string          `json:"version"`
	Plugins []RegistryEntry `json:"plugins"`
}

// RegistryEntry describes one available plugin in the registry.
type RegistryEntry struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	URL          string   `json:"url"`      // download URL (tar.gz)
	Homepage     string   `json:"homepage"` // docs/source
	Tags         []string `json:"tags"`     // email, browser, database, gaming, etc.
	MinOKVersion string   `json:"min_ok_version"`
}

// InstalledManifest tracks what's installed locally.
type InstalledManifest struct {
	Plugins []InstalledPlugin `json:"plugins"`
}

// InstalledPlugin describes a locally installed plugin.
type InstalledPlugin struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Path    string   `json:"path"`
	Tools   []string `json:"tools"`
	Prompts []string `json:"prompts"`
	Running bool     `json:"running"`
}

// DefaultRegistryURL is the official OK plugin registry.
const DefaultRegistryURL = "https://plugins.ok.sh/index.json"

// RegistryDir returns the plugin registry directory.
func RegistryDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ok-plugins")
	}
	return filepath.Join(configDir, "ok", "plugins")
}

// ListInstalled returns all locally installed plugins.
func ListInstalled() (*InstalledManifest, error) {
	path := filepath.Join(RegistryDir(), "installed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &InstalledManifest{}, nil
		}
		return nil, err
	}
	var m InstalledManifest
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: corrupted installed.json, resetting: %v\n", err)
		return &InstalledManifest{}, nil
	}
	return &m, nil
}

// InstallPlugin downloads and installs a plugin by name or URL.
// Returns the installed plugin info.
func InstallPlugin(nameOrURL string) (*InstalledPlugin, error) {
	dir := RegistryDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create plugin dir: %w", err)
	}

	manifest, _ := ListInstalled()

	// Check if it's a URL
	isURL := strings.HasPrefix(nameOrURL, "http://") || strings.HasPrefix(nameOrURL, "https://")

	var entry RegistryEntry
	if isURL {
		entry = RegistryEntry{
			Name: filepath.Base(strings.TrimRight(nameOrURL, "/")),
			URL:  nameOrURL,
		}
	} else {
		// Look up in registry index
		index := fetchRegistryIndex()
		found := false
		for _, e := range index.Plugins {
			if strings.EqualFold(e.Name, nameOrURL) {
				entry = e
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("plugin %q not found in registry — use a direct URL or check spelling", nameOrURL)
		}
	}

	pluginDir := filepath.Join(dir, entry.Name)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return nil, fmt.Errorf("create plugin dir: %w", err)
	}

	// Write manifest
	manifestData, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal plugin manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifestData, 0644); err != nil {
		return nil, fmt.Errorf("write plugin manifest: %w", err)
	}

	// Add to installed manifest
	installed := InstalledPlugin{
		Name:    entry.Name,
		Version: entry.Version,
		Path:    pluginDir,
	}
	manifest.Plugins = append(manifest.Plugins, installed)
	if err := saveInstalled(manifest); err != nil {
		return nil, fmt.Errorf("save manifest: %w", err)
	}

	return &installed, nil
}

// RemovePlugin uninstalls a plugin by name.
func RemovePlugin(name string) error {
	dir := RegistryDir()
	pluginDir := filepath.Join(dir, name)

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("plugin %q is not installed", name)
	}

	if err := os.RemoveAll(pluginDir); err != nil {
		return fmt.Errorf("remove plugin directory: %w", err)
	}

	manifest, _ := ListInstalled()
	var filtered []InstalledPlugin
	for _, p := range manifest.Plugins {
		if !strings.EqualFold(p.Name, name) {
			filtered = append(filtered, p)
		}
	}
	manifest.Plugins = filtered
	return saveInstalled(manifest)
}

// SearchRegistry searches the plugin index for matching tags/names.
func SearchRegistry(query string) []RegistryEntry {
	index := fetchRegistryIndex()
	q := strings.ToLower(query)
	var results []RegistryEntry
	for _, e := range index.Plugins {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) {
			results = append(results, e)
			continue
		}
		for _, t := range e.Tags {
			if strings.Contains(strings.ToLower(t), q) {
				results = append(results, e)
				break
			}
		}
	}
	return results
}

// BuiltinPluginIndex returns a hardcoded index of trusted plugins that ship
// with OK. This index is embedded so the agent can self-bootstrap without
// network access on first run.
// Tier 1: Official OK v4 MCP plugins (bundled, always available).
// Tier 2: Curated third-party plugins (install on demand).
func BuiltinPluginIndex() RegistryIndex {
	return RegistryIndex{
		Version: "1",
		Plugins: []RegistryEntry{
			// ── Tier 1: Official OK v4 MCP plugins ──
			{Name: "@ok/git", Description: "Git version control: status, diff, log, commit, branch", Version: "1.0.0", Tags: []string{"git", "version-control", "vcs", "official"}},
			{Name: "@ok/web-fetch", Description: "Fetch web pages and return readable text content", Version: "1.0.0", Tags: []string{"web", "http", "fetch", "official"}},
			{Name: "@ok/digest", Description: "Hash text, base64 encode/decode, hex, count, JSON format/validate", Version: "1.0.0", Tags: []string{"utility", "hash", "encoding", "official"}},
			{Name: "@ok/workflow", Description: "Define and run multi-step workflows as DAGs", Version: "1.0.0", Tags: []string{"automation", "workflow", "dag", "official"}},
			{Name: "@ok/repo", Description: "Manage multiple repositories — add, list, switch, run", Version: "1.0.0", Tags: []string{"repo", "multi-project", "official"}},
			{Name: "@ok/debug", Description: "Debug Go programs with Delve (dlv): start, break, step, print, stack", Version: "1.0.0", Tags: []string{"debug", "go", "delve", "official"}},
			{Name: "@ok/utils", Description: "Schedule, undo, plan, todo, auto-heal, self-scan, capabilities, covenant, style-check, go-profile, vuln-check", Version: "1.0.0", Tags: []string{"utility", "meta", "official"}},
			{Name: "@ok/browser", Description: "Headless Chrome browser control: navigate, click, type, screenshot, eval", Version: "1.0.0", Tags: []string{"browser", "chrome", "web", "official"}},
			{Name: "@ok/database", Description: "Query SQLite, PostgreSQL, and MySQL databases", Version: "1.0.0", Tags: []string{"database", "sql", "sqlite", "postgres", "mysql", "official"}},
			{Name: "@ok/deploy", Description: "Deploy to remote servers via SSH: build, upload, restart", Version: "1.0.0", Tags: []string{"deploy", "ssh", "devops", "official"}},
			{Name: "@ok/desktop", Description: "Operate the computer: screenshot, processes, clipboard, send-keys, mouse, windows", Version: "1.0.0", Tags: []string{"desktop", "automation", "gui", "official"}},
			{Name: "@ok/computer-use", Description: "Visual computer control: screenshot→analyze→click/type to achieve goals", Version: "1.0.0", Tags: []string{"computer-use", "vision", "automation", "official"}},
			{Name: "@ok/ai-vision", Description: "Read images and analyze videos using AI vision", Version: "1.0.0", Tags: []string{"vision", "image", "video", "ai", "official"}},
			{Name: "@ok/voice", Description: "Speak text or listen for voice input — STT/TTS", Version: "1.0.0", Tags: []string{"voice", "speech", "audio", "official"}},
			{Name: "@ok/wake-word", Description: "Listen for wake word to activate voice control", Version: "1.0.0", Tags: []string{"wake-word", "voice", "hotword", "official"}},
			{Name: "@ok/ocr", Description: "Extract text from images using AI vision", Version: "1.0.0", Tags: []string{"ocr", "vision", "text-extraction", "official"}},
			{Name: "@ok/translate", Description: "Translate text between languages using AI", Version: "1.0.0", Tags: []string{"translate", "language", "i18n", "official"}},
			{Name: "@ok/search", Description: "Search code by regex pattern, find files by glob, find Go symbol definitions", Version: "1.0.0", Tags: []string{"search", "code", "grep", "symbols", "official"}},

			// ── Tier 2: Curated third-party plugins ──
			{
				Name:        "browser-playwright",
				Description: "Full browser automation via Playwright. Navigate, click, type, screenshot, PDF generation. Requires Node.js.",
				Version:     "1.0.0",
				Tags:        []string{"browser", "web", "automation", "screenshot"},
			},
			{
				Name:        "email-imap-smtp",
				Description: "Read, search, and send emails via IMAP and SMTP. Supports Gmail, Outlook, and any standard mail server.",
				Version:     "1.0.0",
				Tags:        []string{"email", "communication", "imap", "smtp"},
			},
			{
				Name:        "calendar-ical",
				Description: "Manage calendars in iCal format. Create events, list agenda, check conflicts. Syncs with Google Calendar and Apple Calendar files.",
				Version:     "1.0.0",
				Tags:        []string{"calendar", "schedule", "time", "productivity"},
			},
			{
				Name:        "files-cloud",
				Description: "Upload/download files to cloud storage. Supports S3, WebDAV, and SFTP. Encrypt before upload with age encryption.",
				Version:     "1.0.0",
				Tags:        []string{"files", "cloud", "storage", "backup", "sync"},
			},
			{
				Name:        "notion-api",
				Description: "Full Notion API integration. Read/write pages, databases, blocks. Search across workspaces.",
				Version:     "1.0.0",
				Tags:        []string{"notes", "knowledge", "notion", "wiki", "docs"},
			},
			{
				Name:        "slack-bot",
				Description: "Slack message integration. Send messages, read channels, react to mentions. Can act as a bot in any workspace.",
				Version:     "1.0.0",
				Tags:        []string{"communication", "slack", "chat", "team"},
			},
			{
				Name:        "weather-openmeteo",
				Description: "Free weather API integration via Open-Meteo. Current conditions, forecasts, historical data. No API key required.",
				Version:     "1.0.0",
				Tags:        []string{"weather", "climate", "forecast", "utility"},
			},
			{
				Name:        "pdf-toolkit",
				Description: "PDF manipulation: extract text, merge, split, fill forms, convert to/from images. Requires poppler or qpdf.",
				Version:     "1.0.0",
				Tags:        []string{"pdf", "documents", "convert", "extract"},
			},
			{
				Name:        "image-magick",
				Description: "Image processing via ImageMagick. Resize, crop, convert formats, apply filters, create thumbnails.",
				Version:     "1.0.0",
				Tags:        []string{"image", "photo", "convert", "media"},
			},
			{
				Name:        "rss-reader",
				Description: "RSS/Atom feed reader. Subscribe to feeds, fetch latest articles, filter by keywords, export to markdown.",
				Version:     "1.0.0",
				Tags:        []string{"news", "rss", "reading", "information"},
			},
		},
	}
}

// ── internal helpers ──

var registryHTTPClient = &http.Client{Timeout: 10 * time.Second}

func fetchRegistryIndex() *RegistryIndex {
	cache := filepath.Join(RegistryDir(), "index.json")

	// 1. Try network fetch (online-first, cache on success)
	if idx := fetchFromNetwork(DefaultRegistryURL); idx != nil {
		if data, err := json.MarshalIndent(idx, "", "  "); err == nil {
			os.MkdirAll(filepath.Dir(cache), 0755)
			os.WriteFile(cache, data, 0644)
		}
		return idx
	}

	// 2. Try cached index from previous successful fetch
	if data, err := os.ReadFile(cache); err == nil {
		var idx RegistryIndex
		if json.Unmarshal(data, &idx) == nil && idx.Version != "" {
			return &idx
		}
	}

	// 3. Fall back to built-in offline index
	builtin := BuiltinPluginIndex()
	return &builtin
}

func fetchFromNetwork(url string) *RegistryIndex {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ok-plugin-registry/1.0")

	resp, err := registryHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "registry index close: %v\n", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	var idx RegistryIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil
	}
	if idx.Version == "" {
		return nil
	}
	return &idx
}

func saveInstalled(m *InstalledManifest) error {
	path := filepath.Join(RegistryDir(), "installed.json")
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create installed dir: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
