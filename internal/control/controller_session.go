package control

import (
	"context"
	"os"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/checkpoint"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/permission"
)

// Compact triggers an immediate context compaction on the executor.
func (c *Controller) Compact(ctx context.Context) error {
	if c.executor == nil {
		return nil
	}
	return c.executor.CompactNow(ctx)
}

// Snapshot saves the current session to disk.
func (c *Controller) Snapshot() error {
	metrics.Snapshot()
	if c.executor == nil {
		return nil
	}
	path := c.SessionPath()
	if path == "" {
		return nil
	}
	if err := c.executor.Session().Save(path); err != nil {
		return err
	}
	// Persist the proof chain alongside the session.
	// c.mu protects c.proofChain; pc.mu protects pc.Entries via pc.Len().
	c.mu.Lock()
	pc := c.proofChain
	ac := c.auditChain
	c.mu.Unlock()
	if pc != nil && pc.Len() > 0 {
		if err := pc.Save(path + ".proof.json"); err != nil {
			c.notice("proof chain save failed: " + err.Error())
		}
	}
	if ac != nil && ac.Len() > 0 {
		if err := ac.Save(path + ".audit.json"); err != nil {
			c.notice("audit chain save failed: " + err.Error())
		}
	}
	return nil
}

// SessionDir returns the directory where session files are persisted ("" when
// persistence is disabled).
func (c *Controller) SessionDir() string { return c.sessionDir }

// SessionPath returns the full path to the current session file ("" when
// persistence is disabled).
func (c *Controller) SessionPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionPath
}

// SetSessionPath sets the path for this session's auto-save file.
func (c *Controller) SetSessionPath(path string) {
	c.mu.Lock()
	c.sessionPath = path
	c.mu.Unlock()
	// Initialize or rebuild the checkpoint store when the session changes.
	if c.cpRoot != "" && path != "" {
		c.cp = checkpoint.New(path+".ckpt", c.cpRoot)
	} else {
		c.cp = nil
	}
}

// NewSession rotates to a fresh session file, archiving the old one, and
// creates a new in-memory conversation so the next turn starts from scratch.
func (c *Controller) NewSession() error {
	if c.executor == nil {
		return nil
	}
	c.mu.Lock()
	old := c.sessionPath
	c.mu.Unlock()
	if old != "" {
		if err := c.executor.Session().Save(old); err != nil {
			c.notice("auto-save failed on session rotate: " + err.Error())
		}
	}
	path := agent.NewSessionPath(c.sessionDir, c.label)
	if path == "" {
		return nil
	}
	// Reset the in-memory session so the conversation starts fresh.
	c.executor.SetSession(agent.NewSession(c.systemPrompt))
	c.mu.Lock()
	c.sessionPath = path
	c.mu.Unlock()
	c.executor.SetSessionPath(path)
	return nil
}

// EnableInteractiveApproval wires the Controller as the Asker (for `ask` tool)
// and installs an interactive permission gate (for approval prompts). Without
// this, headless runs resolve "ask" to allow. Deny rules remain active.
func (c *Controller) EnableInteractiveApproval() {
	if c.executor != nil {
		c.executor.SetAsker(c)
		gate := permission.NewGate(c.policy, c.approval.GateApprover())
		if c.onRemember != nil {
			gate.OnRemember = c.onRemember
		}
		c.executor.SetGate(gate)
	}
}

// Resume loads a saved session into the executor.
func (c *Controller) Resume(sess *agent.Session, path string) {
	if c.executor == nil {
		return
	}
	c.executor.SetSession(sess)
	c.mu.Lock()
	c.sessionPath = path
	c.mu.Unlock()
	c.executor.SetSessionPath(path)
	// Restore the proof chain if it was saved alongside the session
	if c.proofChain != nil && path != "" {
		proofPath := path + ".proof.json"
		if restored, err := core.LoadProofChain(proofPath); err == nil {
			for _, e := range restored.Entries {
				c.proofChain.AppendWithPath(e.AtomID, e.Proposition, e.Evidence, e.ParentID, e.Path)
			}
		}
	}
	// Restore the audit chain if saved alongside the session
	if c.auditChain != nil && path != "" {
		auditPath := path + ".audit.json"
		if data, err := os.ReadFile(auditPath); err == nil {
			if err := c.auditChain.UnmarshalJSON(data); err != nil {
				log.Warn("restore: unmarshal audit chain", "path", auditPath, "err", err)
			}
		}
	}
}
