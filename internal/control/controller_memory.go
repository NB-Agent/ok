package control

import (
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/memory"
)

// --- memory ---
//
// c.mem is treated as an immutable snapshot guarded by c.mu: reads take the lock
// and return the pointer; writes mutate disk then swap in a freshly discovered
// snapshot. A turn-tail note is queued for each write so the change applies this
// session without disturbing the cache-stable system prefix.

// QuickAdd appends a one-line note to the doc-memory file for scope (project
// OK.md by default) — the write side of "#<note>". Returns the file written.
// Protected by a cap (256 entries) to prevent unbounded growth when notes are
// added without a corresponding turn to drain them.
func (c *Controller) QuickAdd(scope memory.Scope, note string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mem == nil {
		return "", nil
	}
	path := c.mem.DocPath(scope)
	if path == "" {
		return "", fmt.Errorf("no target file for memory scope %q", scope)
	}
	if err := memory.AppendDoc(path, note); err != nil {
		return "", err
	}
	const maxPending = 256
	if len(c.pendingMemory) < maxPending {
		c.pendingMemory = append(c.pendingMemory, note)
	} else {
		// Cap reached — drain oldest half to make room.
		drain := maxPending / 2
		c.pendingMemory = append(c.pendingMemory[drain:], note)
	}
	c.refreshMemoryLocked()
	if c.msgbus != nil {
		c.msgbus.Pub("mem:quickadd", agent.MemMsg{Path: path, Scope: string(scope), Note: note})
	}
	return path, nil
}

// SaveDoc overwrites a recognized memory doc with body — the save side of the
// desktop panel's in-place editor. Returns the file written.
func (c *Controller) SaveDoc(path, body string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mem == nil {
		return "", nil
	}
	written, err := c.mem.WriteDoc(path, body)
	if err != nil {
		return "", err
	}
	c.pendingMemory = append(c.pendingMemory,
		"Memory file "+written+" was just edited. Its current contents:\n"+strings.TrimSpace(body))
	c.refreshMemoryLocked()
	if c.msgbus != nil {
		c.msgbus.Pub("mem:savedoc", agent.MemMsg{Path: written, Scope: "project", Note: "edited"})
	}
	return written, nil
}

// Memory returns the loaded memory snapshot (nil when memory is disabled), for
// frontends that surface a memory panel or the /memory command. The returned
// *Set is immutable — mutations go through QuickAdd / SaveDoc.
func (c *Controller) Memory() *memory.Set {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mem
}

// refreshMemoryLocked re-discovers memory from disk so a later Memory() reflects
// a just-applied write. Caller holds c.mu.
func (c *Controller) refreshMemoryLocked() {
	if c.mem == nil {
		return
	}
	c.mem = memory.Load(memory.Options{
		CWD:     c.mem.CWD,
		UserDir: c.mem.UserDir,
		Store:   c.mem.Store, // reuse existing store so the `remember` tool stays consistent
	})
}
