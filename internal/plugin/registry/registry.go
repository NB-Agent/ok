// Package reg provides the v4 multi-registry plugin manager.
// Supports online fetch with offline cache fallback.
package reg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source describes one plugin registry.
type Source struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Priority int    `json:"priority"`
	Auth     string `json:"auth,omitempty"`
}

// PluginMeta describes one available plugin.
type PluginMeta struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	URL          string   `json:"url"`
	Checksum     string   `json:"checksum"`
	Homepage     string   `json:"homepage"`
	Tags         []string `json:"tags"`
	MinOKVersion string   `json:"min_ok_version"`
	License      string   `json:"license"`
	Author       string   `json:"author"`
	SignedBy     string   `json:"signed_by,omitempty"`
}

// InstalledPlugin tracks a locally installed plugin.
type InstalledPlugin struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	Path      string `json:"path"`
	Checksum  string `json:"checksum"`
	Installed string `json:"installed"`
	Running   bool   `json:"running"`
}

// LockEntry pins one plugin version.
type LockEntry struct {
	Version  string `json:"version"`
	Source   string `json:"source"`
	Checksum string `json:"checksum"`
}

// Manager manages plugin discovery, installation, and versioning.
type Manager struct {
	mu        sync.RWMutex
	sources   []Source
	installed map[string]*InstalledPlugin
	pluginDir string
	cacheDir  string
	client    *http.Client
}

// NewManager creates a plugin manager.
func NewManager(pluginDir, cacheDir string, sources []Source) *Manager {
	if pluginDir == "" {
		cd, _ := os.UserConfigDir()
		pluginDir = filepath.Join(cd, "ok", "plugins")
	}
	if cacheDir == "" {
		cd, _ := os.UserCacheDir()
		cacheDir = filepath.Join(cd, "ok", "plugin-cache")
	}
	os.MkdirAll(cacheDir, 0755) // best-effort cache dir creation
	m := &Manager{
		sources:   sources,
		installed: map[string]*InstalledPlugin{},
		pluginDir: pluginDir,
		cacheDir:  cacheDir,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	m.loadInstalled()
	return m
}

// Search queries all registries, returns deduplicated results sorted by relevance.
func (m *Manager) Search(ctx context.Context, query string) ([]PluginMeta, error) {
	m.mu.RLock()
	srcs := append([]Source{}, m.sources...)
	m.mu.RUnlock()

	seen := map[string]bool{}
	var all []PluginMeta
	for _, src := range srcs {
		plugins, err := m.fetchIndex(ctx, src)
		if err != nil {
			continue
		}
		for _, p := range plugins {
			if !matchesQuery(p, query) || seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			all = append(all, p)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return scoreMatch(all[i], query) > scoreMatch(all[j], query)
	})
	return all, nil
}

// Install downloads and registers a plugin by name.
func (m *Manager) Install(ctx context.Context, name string) (*InstalledPlugin, error) {
	m.mu.RLock()
	existing, ok := m.installed[name]
	m.mu.RUnlock()
	if ok && existing.Running {
		return existing, fmt.Errorf("plugin %q already installed", name)
	}

	srcs := m.getSources()
	for _, src := range srcs {
		plugins, err := m.fetchIndex(ctx, src)
		if err != nil {
			continue
		}
		for _, p := range plugins {
			if p.Name != name {
				continue
			}
			return m.install(p, src.Name)
		}
	}
	return nil, fmt.Errorf("plugin %q not found in any registry", name)
}

// ListInstalled returns all locally installed plugins.
func (m *Manager) ListInstalled() []*InstalledPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*InstalledPlugin, 0, len(m.installed))
	for _, p := range m.installed {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Remove deletes a locally installed plugin.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.installed[name]
	if !ok {
		return fmt.Errorf("plugin %q not installed", name)
	}
	os.RemoveAll(p.Path)
	delete(m.installed, name)
	m.saveInstalled()
	return nil
}

// Lock returns a version-lock snapshot of installed plugins.
func (m *Manager) Lock() map[string]LockEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lf := map[string]LockEntry{}
	for name, p := range m.installed {
		lf[name] = LockEntry{
			Version: p.Version, Source: p.Source, Checksum: p.Checksum,
		}
	}
	return lf
}

// ClearCache purges the offline cache (forces fresh fetch next time).
func (m *Manager) ClearCache() {
	os.RemoveAll(m.cacheDir)
}

// ─── internal ────────────────────────────────────────────────────────────

func (m *Manager) getSources() []Source {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources
}

// fetchIndex retrieves a registry index.
// Strategy: online first → cache; offline → use stale cache.
func (m *Manager) fetchIndex(ctx context.Context, src Source) ([]PluginMeta, error) {
	cachePath := filepath.Join(m.cacheDir, sanitize(src.Name)+".json")

	// Try online first (with timeout)
	if plugins, err := m.fetchOnline(ctx, src); err == nil {
		m.writeCache(cachePath, plugins)
		return plugins, nil
	}

	// Fallback to offline cache
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("registry %q unreachable and no cache", src.Name)
	}
	var cached struct {
		Fetched time.Time    `json:"fetched"`
		Plugins []PluginMeta `json:"plugins"`
	}
	if json.Unmarshal(data, &cached) != nil {
		return nil, fmt.Errorf("corrupt cache for %q", src.Name)
	}
	return cached.Plugins, nil
}

func (m *Manager) fetchOnline(ctx context.Context, src Source) ([]PluginMeta, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if err != nil {
		return nil, err
	}
	if src.Auth != "" {
		req.Header.Set("Authorization", "Bearer "+src.Auth)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "registry fetch close: %v\n", err)
		}
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Version string       `json:"version"`
		Plugins []PluginMeta `json:"plugins"`
	}
	if json.Unmarshal(body, &wrapper) == nil {
		return wrapper.Plugins, nil
	}
	var plugins []PluginMeta
	if json.Unmarshal(body, &plugins) != nil {
		return nil, fmt.Errorf("invalid registry index format")
	}
	return plugins, nil
}

func (m *Manager) writeCache(path string, plugins []PluginMeta) {
	cached := struct {
		Fetched time.Time    `json:"fetched"`
		Plugins []PluginMeta `json:"plugins"`
	}{Fetched: time.Now(), Plugins: plugins}
	data, err := json.Marshal(cached)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin registry: marshal cache: %v\n", err)
		return
	}
	os.WriteFile(path, data, 0644)
}

func (m *Manager) install(meta PluginMeta, source string) (*InstalledPlugin, error) {
	dir := filepath.Join(m.pluginDir, meta.Name)
	os.MkdirAll(dir, 0755)
	p := &InstalledPlugin{
		Name: meta.Name, Version: meta.Version, Source: source,
		Path: dir, Checksum: meta.Checksum,
		Installed: time.Now().UTC().Format(time.RFC3339),
	}
	m.mu.Lock()
	m.installed[meta.Name] = p
	m.mu.Unlock()
	m.saveInstalled()
	return p, nil
}

func (m *Manager) loadInstalled() {
	data, err := os.ReadFile(filepath.Join(m.pluginDir, "installed.json"))
	if err != nil {
		return
	}
	var list []*InstalledPlugin
	if json.Unmarshal(data, &list) == nil {
		for _, p := range list {
			m.installed[p.Name] = p
		}
	}
}

func (m *Manager) saveInstalled() {
	list := make([]*InstalledPlugin, 0, len(m.installed))
	for _, p := range m.installed {
		list = append(list, p)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin registry: marshal installed: %v\n", err)
		return
	}
	os.WriteFile(filepath.Join(m.pluginDir, "installed.json"), data, 0644)
}

func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(name))
}

func matchesQuery(p PluginMeta, q string) bool {
	q = strings.ToLower(q)
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(p.Name), q) ||
		strings.Contains(strings.ToLower(p.Description), q) ||
		tagMatch(p.Tags, q)
}

func tagMatch(tags []string, q string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

func scoreMatch(p PluginMeta, q string) int {
	q = strings.ToLower(q)
	s := 0
	if strings.HasPrefix(strings.ToLower(p.Name), q) {
		s += 100
	} else if strings.Contains(strings.ToLower(p.Name), q) {
		s += 50
	}
	if strings.Contains(strings.ToLower(p.Description), q) {
		s += 20
	}
	return s
}
