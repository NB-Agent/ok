package config

import "os"

// SandboxConfig bounds the blast radius of tool calls. WorkspaceRoot is the
// directory file writers/readers may access; empty means the current working
// directory. AllowWrite lists extra dirs writers may also touch; AllowRead lists
// extra dirs readable beyond WorkspaceRoot (defaults to AllowWrite when unset,
// so reads and writes share the same boundary). Both support ${VAR} expansion.
type SandboxConfig struct {
	WorkspaceRoot string   `toml:"workspace_root"`
	AllowWrite    []string `toml:"allow_write"`
	AllowRead     []string `toml:"allow_read"`
	// Bash is the OS-sandbox mode for the bash tool: "enforce" (default) jails
	// each command, "off" runs it unconfined.
	Bash string `toml:"bash"`
	// Network allows network egress from inside the bash sandbox. Defaults true
	// so module/package downloads keep working; the boundary is then writes.
	Network bool `toml:"network"`
	// OnUnavailable controls behaviour when the OS sandbox is not available on
	// the current platform: "warn" (default) prints a warning and continues;
	// "block" refuses to run so you never operate without the sandbox.
	OnUnavailable string `toml:"on_unavailable"`
}

// workspaceRoot resolves the configured WorkspaceRoot (with ${VAR} expansion),
// falling back to the current working directory, then ".".
func (c *Config) workspaceRoot() string {
	root := ExpandVars(c.Sandbox.WorkspaceRoot)
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return "."
	}
	return root
}

// WriteRoots returns the directories file-writer tools may modify: the
// workspace root (defaulting to the current working directory when unset) plus
// any AllowWrite extras, with ${VAR} expanded. The roots are returned as given
// (relative or absolute); the confiner resolves them to absolute, symlink-free
// paths. The result is always non-empty, so confinement is on by default.
func (c *Config) WriteRoots() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.writeRootsLocked()
}

// writeRootsLocked returns write roots without acquiring the lock — callers
// must hold c.mu (read or write) before calling.
func (c *Config) writeRootsLocked() []string {
	roots := []string{c.workspaceRoot()}
	for _, d := range c.Sandbox.AllowWrite {
		if d = ExpandVars(d); d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

// BashMode normalises the bash-sandbox mode: only an explicit "off" disables
// it; empty or any other value resolves to "enforce", so the sandbox is on by
// default and fails safe.
// bashMode returns the normalised bash-sandbox mode without locking.
// Caller must hold c.mu.RLock or c.mu.Lock.
func (c *Config) bashMode() string {
	switch c.Sandbox.Bash {
	case "off":
		return "off"
	case "appcontainer":
		return "appcontainer"
	}
	return "enforce"
}

// BashMode normalises the bash-sandbox mode: only an explicit "off" disables
// it; empty or any other value resolves to "enforce", so the sandbox is on by
// default and fails safe.
func (c *Config) BashMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bashMode()
}

// ReadRoots returns the directories read-only tools may access. When AllowRead
// is set, the roots are the workspace root plus those extras. When AllowRead is
// empty, ReadRoots falls back to WriteRoots(), so reads and writes share the
// same boundary by default.
func (c *Config) ReadRoots() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.Sandbox.AllowRead) > 0 {
		roots := []string{c.workspaceRoot()}
		for _, d := range c.Sandbox.AllowRead {
			if d = ExpandVars(d); d != "" {
				roots = append(roots, d)
			}
		}
		return roots
	}
	return c.writeRootsLocked()
}

// SandboxOnUnavailable returns the sandbox unavailability mode, normalising
// empty to "warn" so consumers don't need their own fallback logic.
func (c *Config) SandboxOnUnavailable() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Sandbox.OnUnavailable == "" {
		return "warn"
	}
	return c.Sandbox.OnUnavailable
}
