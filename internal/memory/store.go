package memory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/frontmatter"
)

// MaxMemoryEntries caps the number of auto-memory files per project. Beyond this
// limit the oldest entries are pruned on each Save AND at load time to keep the
// system-prompt prefix lean and prevent unbounded disk growth.
const MaxMemoryEntries = 80

// Store is the per-project auto-memory: a directory of one-fact-per-file
// Markdown notes with frontmatter, plus a MEMORY.md index of one line per fact.
// The model maintains it through the `remember` tool; the index loads into the
// cached system-prompt prefix at boot so the model always knows what it has
// saved, and reads individual facts on demand with read_file. The whole thing is
// plain files the user can edit by hand.
type Store struct {
	Dir        string // ...ok/projects/<slug>/memory
	mu         *sync.Mutex
	maxEntries int // prune cap; 0 means use default (MaxMemoryEntries)
}

// Type classifies a memory, mirroring the auto-memory taxonomy.
type Type string

const (
	TypeUser      Type = "user"      // who the user is: role, preferences, expertise
	TypeFeedback  Type = "feedback"  // guidance on how to work (with why + how-to-apply)
	TypeProject   Type = "project"   // ongoing work / goals / constraints not in the code
	TypeReference Type = "reference" // pointers to external resources (URLs, tickets)
)

// validTypes is the closed set the `remember` tool accepts; anything else
// normalises to TypeProject.
var validTypes = map[Type]bool{TypeUser: true, TypeFeedback: true, TypeProject: true, TypeReference: true}

// NormalizeType coerces an arbitrary string to a known Type, defaulting to
// TypeProject so a sloppy tool argument never blocks a save.
func NormalizeType(s string) Type {
	t := Type(strings.ToLower(strings.TrimSpace(s)))
	if validTypes[t] {
		return t
	}
	return TypeProject
}

// Memory is one stored fact.
type Memory struct {
	Name        string // kebab-case slug; also the file stem (<name>.md)
	Description string // one-line summary used for the index and recall
	Type        Type
	Body        string // the fact itself (Markdown)
}

// StoreFor resolves the auto-memory directory for a project working dir under
// the user config root, e.g. ~/.config/ok/projects/-Users-me-proj/memory.
// A "" userDir (config dir unresolvable) yields a zero Store, which all methods
// treat as a disabled no-op.
func StoreFor(userDir, cwd string) Store {
	if userDir == "" {
		return Store{}
	}
	return Store{Dir: filepath.Join(userDir, "projects", slugify(absOf(cwd)), "memory"), mu: &sync.Mutex{}, maxEntries: MaxMemoryEntries}
}

// indexFile is the human-readable index of saved memories.
const indexFile = "MEMORY.md"

// slugify turns an absolute project path into a single filesystem-safe segment,
// matching the auto-memory convention (path separators → '-'), e.g.
// "/Users/me/proj" → "-Users-me-proj".
func slugify(absPath string) string {
	r := strings.NewReplacer(string(os.PathSeparator), "-", "/", "-", "\\", "-", ":", "-")
	return r.Replace(absPath)
}

// Index returns the MEMORY.md contents (the per-line index of saved memories),
// or "" if there are none yet. This is what loads into the cached prefix.
func (s Store) Index() string {
	if s.Dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, indexFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Path returns the absolute file path a memory with the given name lives at.
func (s Store) Path(name string) string {
	return filepath.Join(s.Dir, slug(name)+".md")
}

// Save writes (or overwrites) a memory file and refreshes its MEMORY.md index
// line. It is the single mutation entry point — the `remember` tool, the desktop
// editor, and any future importer all go through here so the index never drifts
// from the files. Returns the path written.
func (s Store) Save(m Memory) (string, error) {
	if s.Dir == "" {
		return "", fmt.Errorf("memory store unavailable (no user config dir)")
	}
	name := slug(m.Name)
	if name == "" {
		return "", fmt.Errorf("memory needs a name")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, name+".md")
	// Write the .md file atomically (temp+rename) so a crash never leaves a
	// partially-written file. Each call uses a unique temp file name to avoid
	// collisions between concurrent Save() calls.
	tmpMD := path + "." + name + "." + randHex(4) + ".tmp"
	if err := os.WriteFile(tmpMD, []byte(render(m, name)), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpMD, path); err != nil {
		return "", err
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	if err := s.reindex(name, m); err != nil {
		return path, err
	}
	// Prune oldest entries when over the cap so the index never grows unbounded.
	cap := s.maxEntries
	if cap <= 0 {
		cap = MaxMemoryEntries
	}
	s.prune(cap)
	return path, nil
}

// prune deletes the oldest memory files when the store exceeds maxFiles,
// keeping the index lean so the system prompt prefix stays small.
// Returns true if any files were deleted.
func (s Store) prune(maxFiles int) bool {
	if s.Dir == "" || maxFiles <= 0 {
		return false
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return false
	}
	type entry struct {
		name string
		mod  time.Time
	}
	var mems []entry
	for _, e := range entries {
		if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mems = append(mems, entry{name: e.Name(), mod: info.ModTime()})
	}
	if len(mems) <= maxFiles {
		return false
	}
	// Sort by modification time ascending (oldest first) and delete the overflow.
	sort.Slice(mems, func(i, j int) bool { return mems[i].mod.Before(mems[j].mod) })
	toDelete := mems[:len(mems)-maxFiles]
	for _, e := range toDelete {
		os.Remove(filepath.Join(s.Dir, e.name))
	}
	return true
}

// Compact prunes oldest entries beyond MaxMemoryEntries. When files are
// actually deleted, it also rebuilds the index so MEMORY.md never references
// missing entries. When nothing is pruned, the index is left untouched — this
// preserves cache stability (byte-identical system prefix) and any hand-edits
// the user made to MEMORY.md. Idempotent; nil store or empty dir is a no-op.
func (s Store) Compact() {
	if s.Dir == "" {
		return
	}
	cap := s.maxEntries
	if cap <= 0 {
		cap = MaxMemoryEntries
	}
	if !s.prune(cap) {
		return // nothing deleted — index is already correct, don't touch it
	}
	// Files were deleted — rebuild the index from remaining files.
	mems := s.List()
	if len(mems) == 0 {
		return
	}
	idxPath := filepath.Join(s.Dir, indexFile)
	names := make([]string, 0, len(mems))
	lines := make(map[string]string, len(mems))
	for _, m := range mems {
		n := slug(m.Name)
		names = append(names, n)
		lines[n] = fmt.Sprintf("- [%s](%s.md) — %s", n, n, oneLine(m.Description))
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Memory\n\n")
	for _, n := range names {
		b.WriteString(lines[n])
		b.WriteString("\n")
	}
	tmp := idxPath + "." + randHex(4) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	os.Rename(tmp, idxPath)
}

// render serializes a memory to frontmatter + body. The frontmatter mirrors the
// auto-memory shape (name / description / metadata.type) so the files are
// interchangeable with that ecosystem and re-readable by loadMemory.
func render(m Memory, name string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + oneLine(m.Description) + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  type: " + string(NormalizeType(string(m.Type))) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(m.Body))
	b.WriteString("\n")
	return b.String()
}

// indexLineRe matches a managed index line so reindex can replace the line for a
// given memory without disturbing the rest of a hand-edited MEMORY.md.
var indexLineRe = regexp.MustCompile(`\]\(([^)]+)\.md\)`)

// reindex rewrites the MEMORY.md line for name, preserving every other line and
// keeping the list sorted by filename. The line format matches the auto-memory
// index: "- [<name>](<name>.md) — <description>".
func (s Store) reindex(name string, m Memory) error {
	path := filepath.Join(s.Dir, indexFile)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err // permission error — don't silently overwrite
	}

	keep := map[string]string{}
	for _, line := range strings.Split(string(existing), "\n") {
		if mt := indexLineRe.FindStringSubmatch(line); mt != nil && mt[1] != name {
			keep[mt[1]] = strings.TrimRight(line, "\r")
		}
	}
	keep[name] = fmt.Sprintf("- [%s](%s.md) — %s", name, name, oneLine(m.Description))

	names := make([]string, 0, len(keep))
	for n := range keep {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Memory\n\n")
	for _, n := range names {
		b.WriteString(keep[n])
		b.WriteString("\n")
	}
	// Atomic temp+rename to avoid TOCTOU with concurrent writes.
	// Each reindex call gets a unique temp file name to prevent concurrent
	// Save() goroutines from clobbering each other's MEMORY.md update.
	tmp := path + "." + randHex(4) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// List returns the saved memories parsed from their files, sorted by name. Used
// by `/memory` and the desktop memory panel. Files that fail to parse are
// skipped so one bad file never hides the rest.
func (s Store) List() []Memory {
	if s.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "memory: cannot read directory %s: %v\n", s.Dir, err)
		}
		return nil
	}
	var out []Memory
	for _, e := range entries {
		if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if m, ok := loadMemory(filepath.Join(s.Dir, e.Name())); ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loadMemory parses one fact file back into a Memory. It tolerates the minimal
// frontmatter render writes; a file without frontmatter still loads with its
// body and a name derived from the filename.
func loadMemory(path string) (Memory, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, false
	}
	content := frontmatter.Normalize(b)
	fm, body := frontmatter.Parse(content)
	m := Memory{
		Name:        fm["name"],
		Description: fm["description"],
		Type:        NormalizeType(fm["type"]),
		Body:        strings.TrimSpace(body),
	}
	if m.Name == "" {
		m.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return m, true
}

// slugRe strips everything but lowercase alphanumerics and dashes.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug normalises a name into a kebab-case, filesystem-safe stem.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use time as entropy (not crypto-grade but avoids collisions)
		return hex.EncodeToString([]byte(fmt.Sprint(os.Getpid())))
	}
	return hex.EncodeToString(b)
}

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

// oneLine collapses whitespace so a description can't break the single-line
// index or frontmatter format.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
