package memory

import (
	"path/filepath"
	"sync"
)

// SharedStoreFor returns a Store that is NOT scoped to a specific project.
// Facts saved here load for every project the user runs. Use sparingly —
// only for truly cross-cutting knowledge (e.g. "user prefers tab indentation
// in every project", "global coding conventions across all projects", etc.).
//
// Shared memories appear in the system prompt alongside project-specific
// memories, but are stored under ~/.config/ok/shared/memory/.
func SharedStoreFor(userDir string) Store {
	if userDir == "" {
		return Store{}
	}
	return Store{Dir: filepath.Join(userDir, "shared", "memory"), mu: &sync.Mutex{}}
}
