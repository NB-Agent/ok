package memory

import (
	"path/filepath"
	"sync"
)

// MaxSharedMemoryEntries caps shared memory files. Shared memories load into
// every project's system prefix, so they must stay much leaner than per-project
// memories. Beyond this limit the oldest entries are pruned on each Save.
const MaxSharedMemoryEntries = 30

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
	return Store{Dir: filepath.Join(userDir, "shared", "memory"), mu: &sync.Mutex{}, maxEntries: MaxSharedMemoryEntries}
}
