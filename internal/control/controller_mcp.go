package control

import (
	"fmt"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/plugin"
)

// Host returns the running MCP host (nil when no plugins), for frontends that
// list servers / resolve MCP prompts.
func (c *Controller) Host() *plugin.Host { return c.host }

// AddMCPServer connects an MCP server live and persists it to the config file. Its
// tools are registered immediately and become available on the next turn (the
// agent reads the registry per turn). Returns the number of tools the server
// exposed. A save failure after a successful connect is reported but non-fatal.
func (c *Controller) AddMCPServer(e config.PluginEntry) (int, error) {
	c.mu.Lock()
	host := c.host
	if host == nil {
		host = plugin.NewHost()
		c.host = host
	}
	c.mu.Unlock()

	exp := e.ExpandedPlugin()
	tools, err := host.Add(c.pluginCtx, plugin.Spec{
		Name:    exp.Name,
		Type:    exp.Type,
		Command: exp.Command,
		Args:    exp.Args,
		Env:     exp.Env,
		URL:     exp.URL,
		Headers: exp.Headers,
	})
	if err != nil {
		return 0, err
	}
	if c.reg != nil {
		c.mu.Lock()
		for _, t := range tools {
			c.reg.Add(t)
		}
		c.mu.Unlock()
	}
	// Persist the plugin entry to config — best-effort, non-fatal.
	cfg, lerr := config.Load()
	if lerr != nil {
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("MCP %q: connected but saving config failed: %v", e.Name, lerr)})
		return len(tools), nil
	}
	if err := cfg.UpsertPlugin(e); err != nil {
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("MCP %q: connected but config entry rejected: %v", e.Name, err)})
		return len(tools), nil
	}
	if err := cfg.Save(); err != nil {
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("MCP %q: connected but saving config failed: %v", e.Name, err)})
	}
	return len(tools), nil
}

// RemoveMCPServer disconnects a live MCP server — its tools vanish from the next
// turn — and removes it from the config file. A server declared in .mcp.json
// disconnects for this session but returns on the next start.
func (c *Controller) RemoveMCPServer(name string) (disconnected bool, err error) {
	c.mu.Lock()
	if c.host != nil {
		if prefix, ok := c.host.Remove(name); ok {
			disconnected = true
			if c.reg != nil {
				c.reg.RemovePrefix(prefix)
			}
		}
	}
	c.mu.Unlock()
	cfg, lerr := config.Load()
	if lerr != nil {
		return disconnected, lerr
	}
	inConfig := cfg.RemovePlugin(name)
	if inConfig {
		if serr := cfg.Save(); serr != nil {
			return disconnected, serr
		}
	}
	if !disconnected && !inConfig {
		return false, fmt.Errorf("no MCP server named %q", name)
	}
	return disconnected, nil
}
