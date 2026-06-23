package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/brief"
	"github.com/NB-Agent/ok/internal/codegraph"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/context7"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/tool"
)

// v4PluginMapping maps MCP plugin binary names to the builtin tools they replace.
// When a plugin binary is found in plugins/, its tools replace the builtin equivalents,
// reducing kernel schema tokens by ~90%.
// pluginManifest is read from plugin.json in each plugin directory.
type pluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Entrypoint  string   `json:"entrypoint"`
	Tools       []string `json:"tools"`
	Transport   string   `json:"transport"`
	Description string   `json:"description"`
	MinOKVer    string   `json:"min_ok_version"`
}

// detectV4Plugins scans the plugins/ directory for MCP plugin binaries by
// reading each subdirectory's plugin.json. This is fully dynamic — adding a new
// plugin means dropping its directory under plugins/; no code change needed.
func detectV4Plugins(cwd string) ([]plugin.Spec, []string) {
	pluginDir := filepath.Join(cwd, "plugins")
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil, nil
	}
	var specs []plugin.Spec
	var toolNames []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pj := filepath.Join(pluginDir, e.Name(), "plugin.json")
		data, err := os.ReadFile(pj)
		if err != nil {
			continue
		}
		var m pluginManifest
		if json.Unmarshal(data, &m) != nil || m.Entrypoint == "" || len(m.Tools) == 0 {
			continue
		}
		winPath := filepath.Join(pluginDir, e.Name(), m.Entrypoint+".exe")
		unixPath := filepath.Join(pluginDir, e.Name(), m.Entrypoint)
		var found string
		if _, err := os.Stat(winPath); err == nil {
			found = winPath
		} else if _, err := os.Stat(unixPath); err == nil {
			found = unixPath
		}
		if found == "" {
			continue
		}
		pluginName := e.Name()
		if m.Name != "" {
			pluginName = m.Name
		}
		specs = append(specs, plugin.Spec{
			Name:    pluginName,
			Command: found,
			Dir:     cwd,
		})
		toolNames = append(toolNames, m.Tools...)
	}
	return specs, toolNames
}

func PluginSpecs(entries []config.PluginEntry) []plugin.Spec {
	specs := make([]plugin.Spec, len(entries))
	for i, e := range entries {
		e = e.ExpandedPlugin()
		specs[i] = plugin.Spec{
			Name: e.Name, Type: e.Type, Command: e.Command,
			Args: e.Args, Env: e.Env, URL: e.URL, Headers: e.Headers,
		}
	}
	return specs
}

// loadPlugins discovers, wires, and starts all plugins (configured, v4
// auto-detected, Context7, codegraph). Returns the host, a cleanup function,
// and a map of auto-discovered specs for later health-check restarts.
func loadPlugins(ctx context.Context, cfg *config.Config, reg *tool.Registry, cwd string, sink event.Sink) (*plugin.Host, func(), map[string]plugin.Spec, error) {
	pluginHost := plugin.NewHost()
	specs := PluginSpecs(cfg.Plugins)
	// v4: auto-detect MCP plugin binaries and replace builtin equivalents.
	// This reduces kernel tool schema from ~8000 to ~1500 tokens.
	autoSpecs, autoTools := detectV4Plugins(cwd)

	// Context7: auto-discover when CONTEXT7_API_KEY is set. Provides up-to-date
	// library documentation, eliminating API hallucinations from stale training data.
	if ctx7, ok7 := context7.Spec(); ok7 {
		autoSpecs = append(autoSpecs, ctx7)
	}

	if len(autoSpecs) > 0 {
		if !cfg.PluginQuiet {
			var names []string
			for _, s := range autoSpecs {
				names = append(names, s.Name)
			}
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
				Text: "auto-discovered " + fmt.Sprintf("%d plugin(s)", len(autoSpecs)) + ": " + strings.Join(names, ", ") +
					" — verify these are trusted before use"})
		}
		for _, tn := range autoTools {
			reg.RemovePrefix(tn)
		}
		specs = append(specs, autoSpecs...)
	}
	// Store auto-discovered plugin specs for health-check restart.
	autoSpecMap := map[string]plugin.Spec{}
	for _, s := range autoSpecs {
		autoSpecMap[s.Name] = s
	}
	if cfg.Codegraph.Enabled {
		if bin, ok := codegraph.Resolve(cfg.Codegraph.Path); ok {
			if err := codegraph.EnsureInit(ctx, bin, cwd); err != nil {
				sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
					Text: "codegraph: init failed (" + err.Error() + ") — running in degraded mode (no symbol index)"})
			} else {
				// Project brief (Stacklit): generate a concise overview that
				// gives the agent immediate context at session start.
				if err := brief.Generate(cwd); err != nil {
					sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
						Text: "brief: " + err.Error()})
				}
			}
			specs = append(specs, plugin.Spec{
				Name:    "codegraph",
				Command: bin,
				Args:    []string{"serve", "--mcp"},
				Dir:     cwd,
			})
		}
	}
	if len(specs) > 0 {
		host, ptools, err := plugin.StartAll(ctx, specs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("plugin: %w", err)
		}
		pluginHost = host
		for _, t := range ptools {
			reg.Add(t)
		}
	}
	return pluginHost, pluginHost.Close, autoSpecMap, nil
}
