package kernel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/semantic"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/winhide"
)

// ─── Constants (audit #6 — extracted from in-line magic numbers) ───────

const (
	defaultCommandTimeout = 300 * time.Second // default timeout for sandbox commands
	binaryDetectPeek      = 8192              // bytes to peek for binary file detection
	defaultReadLineLimit  = 2000              // default read_file line limit
	defaultGrepMaxMatches = 200               // default grep maximum match count
)

// ─── sandboxAdapter ──────────────────────────────────────────────────────

// sandboxAdapter wraps sandbox.Spec to implement kernel.Sandbox.
// It runs commands through the OS sandbox when the spec enforces.
type sandboxAdapter struct {
	spec    sandbox.Spec
	workDir string
}

// NewSandbox creates a kernel Sandbox backed by a sandbox.Spec.
func NewSandbox(spec sandbox.Spec, workDir string) Sandbox {
	return &sandboxAdapter{spec: spec, workDir: workDir}
}

func (a *sandboxAdapter) Run(ctx context.Context, command string, opts RunOptions) RunResult {
	timeout := time.Duration(opts.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workDir := opts.WorkDir
	if workDir == "" {
		workDir = a.workDir
	}
	spec := a.spec
	if !opts.Network {
		spec.Network = false
	}

	argv, sandboxed := sandbox.Command(spec, "bash", command)
	cmd := winhide.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if sandboxed {
		if err := cmd.Start(); err != nil {
			return RunResult{Output: buf.String(), ExitCode: -1, Error: fmt.Sprintf("sandbox start: %v", err)}
		}
		if err := sandbox.WrapProcess(cmd.Process.Pid, spec); err != nil {
			if killErr := cmd.Process.Kill(); killErr != nil {
				log.Warn("sandbox wrap: kill after failure", "killErr", killErr)
			}
			if waitErr := cmd.Wait(); waitErr != nil {
				log.Warn("sandbox wrap: wait after failure", "waitErr", waitErr)
			}
			return RunResult{Output: buf.String(), ExitCode: -1, Error: fmt.Sprintf("sandbox wrap: %v", err)}
		}
		if err := cmd.Wait(); err != nil {
			code := exitCode(err)
			return RunResult{Output: buf.String(), ExitCode: code, Error: err.Error()}
		}
		return RunResult{Output: buf.String(), ExitCode: 0}
	}

	if err := cmd.Run(); err != nil {
		code := exitCode(err)
		if ctx.Err() == context.DeadlineExceeded {
			return RunResult{Output: buf.String(), ExitCode: code, Error: fmt.Sprintf("command timed out (> %s)", timeout)}
		}
		return RunResult{Output: buf.String(), ExitCode: code, Error: err.Error()}
	}
	return RunResult{Output: buf.String(), ExitCode: 0}
}

func (a *sandboxAdapter) Available() bool { return sandbox.Available() }

func (a *sandboxAdapter) PluginSpec() *PluginIsolation { return nil }

// exitCode extracts the exit code from exec.ExitError, or returns -1.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// ─── sessionAdapter ───────────────────────────────────────────────────────

// sessionAdapter wraps agent.Session to implement kernel.Session.
type sessionAdapter struct {
	inner *agent.Session
}

// NewSession creates a kernel Session backed by an agent.Session.
func NewSession(inner *agent.Session) Session {
	return &sessionAdapter{inner: inner}
}

func (a *sessionAdapter) Add(msg Message) {
	a.inner.Add(provider.Message{
		Role:    provider.Role(msg.Role),
		Content: msg.Content,
		Name:    msg.Name,
	})
}

func (a *sessionAdapter) Snapshot() []Message {
	msgs := a.inner.Snapshot()
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = Message{
			Role:    string(m.Role),
			Content: m.Content,
			Name:    m.Name,
		}
	}
	return out
}

func (a *sessionAdapter) Compact(ctx context.Context) error {
	// Design note: this is intentionally a no-op. Agent-level compaction is
	// driven by Agent.maybeCompact which checks the session and context window
	// independently. The kernel-level Compact exists as a hook point for future
	// pluggable compaction strategies.
	return nil
}

// ─── providerAdapter ──────────────────────────────────────────────────────

// providerAdapter wraps provider.Provider to implement kernel.Provider.
type providerAdapter struct {
	inner provider.Provider
}

// NewProvider creates a kernel Provider backed by a provider.Provider.
func NewProvider(inner provider.Provider) Provider {
	return &providerAdapter{inner: inner}
}

func (a *providerAdapter) Name() string { return a.inner.Name() }

func (a *providerAdapter) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	preq := provider.Request{
		Messages:    make([]provider.Message, len(req.Messages)),
		Temperature: req.Temperature,
	}
	for i, m := range req.Messages {
		preq.Messages[i] = provider.Message{
			Role:    provider.Role(m.Role),
			Content: m.Content,
			Name:    m.Name,
		}
	}
	for _, t := range req.Tools {
		preq.Tools = append(preq.Tools, provider.ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Schema,
		})
	}

	pch, err := a.inner.Stream(ctx, preq)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 8)
	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "stream goroutine panic: %v\n", r)
			}
		}()
		for pc := range pch {
			select {
			case ch <- convertChunk(pc):
			case <-ctx.Done():
				return // caller abandoned the stream; stop forwarding
			}
		}
	}()
	return ch, nil
}

func convertChunk(pc provider.Chunk) Chunk {
	kc := Chunk{}
	switch pc.Type {
	case provider.ChunkText:
		kc.Type = ChunkText
		kc.Text = pc.Text
	case provider.ChunkReasoning:
		kc.Type = ChunkReasoning
		kc.Text = pc.Text
	case provider.ChunkToolCallStart, provider.ChunkToolCall:
		kc.Type = ChunkToolCall
		if pc.ToolCall != nil {
			kc.ToolID = pc.ToolCall.ID
			var err error
			kc.Data, err = json.Marshal(pc.ToolCall)
			if err != nil {
				fmt.Fprintf(os.Stderr, "adapters: marshal tool call: %v\n", err)
			}
		}
	case provider.ChunkUsage, provider.ChunkDone:
		kc.Type = ChunkUsage
	case provider.ChunkError:
		kc.Type = ChunkError
		if pc.Err != nil {
			kc.Text = pc.Err.Error()
		}
	}
	return kc
}

// ─── bashAdapter ──────────────────────────────────────────────────────────

// bashAdapter implements kernel.Bash by delegating to kernel.Sandbox.
type bashAdapter struct {
	sb Sandbox
}

// NewBash creates a kernel Bash backed by the Sandbox primitive.
func NewBash(sb Sandbox) Bash {
	return &bashAdapter{sb: sb}
}

func (a *bashAdapter) Exec(ctx context.Context, command string, bg bool) BashOut {
	if bg {
		// Background jobs are not supported at the kernel syscall level;
		// the builtin bash tool handles them. Return a non-zero exit code
		// so callers know it was rejected rather than silently "succeeded".
		return BashOut{
			Output:   "Background execution not available at kernel level; use the 'bash' tool for background jobs.",
			ExitCode: -1,
		}
	}
	res := a.sb.Run(ctx, command, RunOptions{Network: true})
	return BashOut{
		Output:   res.Output,
		ExitCode: res.ExitCode,
	}
}

// ─── readFileAdapter ──────────────────────────────────────────────────────

// readFileAdapter implements kernel.ReadFile as direct file I/O.
type readFileAdapter struct {
	roots   []string
	workDir string
}

// NewReadFile creates a kernel ReadFile with optional root confinement.
func NewReadFile(roots []string, workDir string) ReadFile {
	return &readFileAdapter{roots: roots, workDir: workDir}
}

func (a *readFileAdapter) Read(_ context.Context, path string, offset, limit int) FileContent {
	resolved := resolveIn(a.workDir, path)
	if err := confineRead(a.roots, resolved); err != nil {
		return FileContent{Path: path, Error: err.Error()}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return FileContent{Path: path, Error: fmt.Sprintf("stat %s: %v", resolved, err)}
	}
	if info.IsDir() {
		return FileContent{Path: path, Error: fmt.Sprintf("%s is a directory", resolved)}
	}

	f, err := os.Open(resolved)
	if err != nil {
		return FileContent{Path: path, Error: fmt.Sprintf("open %s: %v", resolved, err)}
	}
	defer log.Close(resolved, f)

	// Check for binary.
	peek := make([]byte, binaryDetectPeek)
	n, _ := io.ReadFull(f, peek)
	if bytes.IndexByte(peek[:n], 0) >= 0 {
		return FileContent{Path: path, Binary: true, Size: int(info.Size())}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return FileContent{Path: path, Error: fmt.Sprintf("seek %s: %v", resolved, err)}
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultReadLineLimit
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	var lines []string
	totalLines := 0
	for scanner.Scan() {
		totalLines++
		lineNo++
		if lineNo > offset && len(lines) < limit {
			lines = append(lines, scanner.Text())
		}
	}

	return FileContent{
		Path:    path,
		Content: strings.Join(lines, "\n"),
		Lines:   totalLines,
		Size:    int(info.Size()),
	}
}

// ─── writeFileAdapter ─────────────────────────────────────────────────────

// writeFileAdapter implements kernel.WriteFile as direct file I/O.
type writeFileAdapter struct {
	roots   []string
	workDir string
}

// NewWriteFile creates a kernel WriteFile with optional root confinement.
func NewWriteFile(roots []string, workDir string) WriteFile {
	return &writeFileAdapter{roots: roots, workDir: workDir}
}

func (a *writeFileAdapter) Write(_ context.Context, path, content string) error {
	resolved := resolveIn(a.workDir, path)
	if err := confineWrite(a.roots, resolved); err != nil {
		return err
	}
	if dir := filepath.Dir(resolved); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return os.WriteFile(resolved, []byte(content), 0644)
}

// ─── editFileAdapter ──────────────────────────────────────────────────────

// editFileAdapter implements kernel.EditFile as read-modify-write.
type editFileAdapter struct {
	roots   []string
	workDir string
}

// NewEditFile creates a kernel EditFile with optional root confinement.
func NewEditFile(roots []string, workDir string) EditFile {
	return &editFileAdapter{roots: roots, workDir: workDir}
}

func (a *editFileAdapter) Edit(_ context.Context, path, oldString, newString string) error {
	resolved := resolveIn(a.workDir, path)
	if err := confineWrite(a.roots, resolved); err != nil {
		return err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read %s: %w", resolved, err)
	}
	body := string(data)
	count := strings.Count(body, oldString)
	if count == 0 {
		return fmt.Errorf("old_string not found in %s", resolved)
	}
	if count > 1 {
		return fmt.Errorf("old_string is not unique in %s (%d occurrences); add more context to disambiguate", resolved, count)
	}
	updated := strings.Replace(body, oldString, newString, 1)
	return os.WriteFile(resolved, []byte(updated), 0644)
}

// ─── grepAdapter ──────────────────────────────────────────────────────────

// grepAdapter implements kernel.Grep as direct regex search.
type grepAdapter struct {
	roots   []string
	workDir string
}

// NewGrep creates a kernel Grep with optional root confinement.
func NewGrep(roots []string, workDir string) Grep {
	return &grepAdapter{roots: roots, workDir: workDir}
}

func (a *grepAdapter) Search(_ context.Context, pattern, path string) ([]Match, error) {
	if path == "" {
		path = "."
	}
	resolved := resolveIn(a.workDir, path)
	if err := confineRead(a.roots, resolved); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	var matches []Match
	const maxMatches = 200
	truncated := false

	searchFile := func(file string) error {
		f, err := os.Open(file)
		if err != nil {
			return nil
		}
		defer log.Close(file, f)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		ln := 0
		for sc.Scan() {
			ln++
			line := sc.Text()
			if re.MatchString(line) {
				matches = append(matches, Match{
					File: file,
					Line: ln,
					Text: strings.TrimRight(line, "\r\n"),
				})
				if len(matches) >= maxMatches {
					truncated = true
					return io.EOF
				}
			}
		}
		return sc.Err()
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", resolved, err)
	}
	if info.IsDir() {
		walkErr := filepath.WalkDir(resolved, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err // propagate walk error instead of swallowing it
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == ".next" ||
					name == "build" || name == "dist" || name == "__pycache__" || name == ".venv" {
					return filepath.SkipDir
				}
				return nil
			}
			if errors.Is(searchFile(p), io.EOF) {
				return filepath.SkipAll
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walk %s: %w", resolved, walkErr)
		}
	} else {
		if err := searchFile(resolved); err != nil {
			return nil, fmt.Errorf("grep %s: %w", resolved, err)
		}
	}
	if truncated {
		matches = append(matches, Match{File: fmt.Sprintf("... (truncated at %d matches)", maxMatches)})
	}
	return matches, nil
}

// ─── path helpers ─────────────────────────────────────────────────────────

// resolveIn resolves path relative to workDir when path is relative.
func resolveIn(workDir, path string) string {
	if workDir == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}

// confineRead checks that path is within one of the allowed roots.
func confineRead(roots []string, path string) error {
	if len(roots) == 0 {
		return nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("confine read: abs %s: %w", path, err)
	}
	for _, r := range roots {
		absRoot, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if strings.HasPrefix(resolved, absRoot+string(os.PathSeparator)) || resolved == absRoot {
			return nil
		}
	}
	return fmt.Errorf("confine read: %s is outside allowed roots", path)
}

// confineWrite checks that path is within one of the allowed write roots.
func confineWrite(roots []string, path string) error {
	if len(roots) == 0 {
		return nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("confine write: abs %s: %w", path, err)
	}
	for _, r := range roots {
		absRoot, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if strings.HasPrefix(resolved, absRoot+string(os.PathSeparator)) || resolved == absRoot {
			return nil
		}
	}
	return fmt.Errorf("confine write: %s is outside allowed roots", path)
}

// ─── identityAdapter ──────────────────────────────────────────────────────

// identityAdapter wraps OS environment to implement Identity.
type identityAdapter struct {
	mu   sync.RWMutex // protects user field (audit #13)
	user User
}

// NewIdentity creates a kernel Identity backed by local config and OS user.
func NewIdentity() Identity {
	u := User{
		ID:    "local",
		Label: os.Getenv("USER"),
		Roles: []string{"user"},
	}
	if u.Label == "" {
		u.Label = os.Getenv("USERNAME")
	}
	if u.Label == "" {
		u.Label = "anonymous"
	}
	for _, env := range []string{"LANG", "LC_ALL", "OK_LANG"} {
		if v := os.Getenv(env); v != "" {
			parts := strings.SplitN(v, ".", 2)
			u.Locale = parts[0]
			break
		}
	}
	if u.Locale == "" {
		u.Locale = "en"
	}
	if v := os.Getenv("OK_DEFAULT_MODEL"); v != "" {
		u.ModelPref = v
	}
	return &identityAdapter{user: u}
}

func (a *identityAdapter) Whoami(_ context.Context) User {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.user
}

func (a *identityAdapter) SetUser(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" {
		a.user = User{ID: "anonymous", Label: "anonymous", Roles: []string{}, Locale: "en"}
		return nil
	}
	a.user.ID = id
	a.user.Label = id
	return nil
}

func (a *identityAdapter) ListUsers(_ context.Context) ([]User, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return []User{a.user}, nil
}

// ─── recallAdapter ────────────────────────────────────────────────────────

// SemanticSearcher provides semantic search over memory.
// nil means semantic search is unavailable; fall back to substring match.
type SemanticSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]ScoredFact, error)
}

// SemanticEngineAdapter wraps semantic.Engine to implement SemanticSearcher.
// This bridges the code-search-oriented semantic.Engine into the
// memory-oriented SemanticSearcher interface so Recall can use vector search.
type SemanticEngineAdapter struct {
	eng *semantic.Engine
}

// NewSemanticEngineAdapter creates an adapter from a semantic engine.
func NewSemanticEngineAdapter(eng *semantic.Engine) SemanticSearcher {
	if eng == nil {
		return nil
	}
	return &SemanticEngineAdapter{eng: eng}
}

func (a *SemanticEngineAdapter) Search(ctx context.Context, query string, limit int) ([]ScoredFact, error) {
	results, err := a.eng.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	facts := make([]ScoredFact, 0, len(results))
	for _, r := range results {
		facts = append(facts, ScoredFact{
			Fact: Fact{
				Key:   r.Chunk.ID,
				Value: r.Chunk.Content,
				Tags: map[string]string{
					"file":     r.Chunk.File,
					"language": r.Chunk.Language,
					"kind":     r.Chunk.Kind,
					"line":     fmt.Sprintf("%d", r.Chunk.Line),
				},
			},
			Score: float64(r.Score),
		})
	}
	return facts, nil
}

// ScoredFact is a search result with a relevance score.
type ScoredFact struct {
	Fact
	Score float64
}

// recallAdapter wraps memory.Store to implement kernel.Recall.
type recallAdapter struct {
	store    *memory.Store
	semantic SemanticSearcher // nil disables semantic search

	// contentCache caches file contents by path, keyed by modtime string,
	// to avoid repeated full-disk walks on every Search call.
	// Invalidated on Save and Forget.
	contentCache sync.Map // map[string]cacheEntry
}

type cacheEntry struct {
	content string
	modTime string // file mod time string to detect staleness
}

// NewRecall creates a kernel Recall backed by memory.Store.
// store may be nil (returns empty results gracefully).
func NewRecall(store *memory.Store) Recall {
	return &recallAdapter{store: store}
}

// NewRecallWithSemantic creates a kernel Recall with an optional semantic
// search engine. When semantic is nil, falls back to substring matching.
func NewRecallWithSemantic(store *memory.Store, semantic SemanticSearcher) Recall {
	return &recallAdapter{store: store, semantic: semantic}
}

func (a *recallAdapter) Save(ctx context.Context, fact Fact) error {
	if a.store == nil {
		return nil
	}
	key := fact.Key
	if key == "" {
		key = fmt.Sprintf("fact-%d", time.Now().UnixNano())
	}
	scope := fact.Scope
	if scope == "" {
		scope = "project"
	}
	dir := filepath.Join(a.store.Dir, scope)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("recall mkdir: %w", err)
	}
	path := filepath.Join(dir, key+".md")
	content := fmt.Sprintf("---\nkey: %s\nsource: %s\ncreated: %s\n---\n\n%s",
		key, fact.Source, time.Now().UTC().Format(time.RFC3339), fact.Value)
	// Invalidate cache for the affected scope directory.
	a.contentCache.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), dir) {
			a.contentCache.Delete(k)
		}
		return true
	})
	return os.WriteFile(path, []byte(content), 0644)
}

func (a *recallAdapter) Search(ctx context.Context, query string, limit int) ([]Fact, error) {
	if a.store == nil || query == "" {
		return nil, nil
	}
	// Prefer semantic search when available.
	if a.semantic != nil {
		scored, err := a.semantic.Search(ctx, query, limit)
		if err == nil && len(scored) > 0 {
			facts := make([]Fact, len(scored))
			for i, s := range scored {
				facts[i] = s.Fact
			}
			return facts, nil
		}
	}
	// Fall back to substring matching.
	return a.substringSearch(query, limit)
}

func (a *recallAdapter) substringSearch(query string, limit int) ([]Fact, error) {
	var results []Fact
	base := a.store.Dir
	if base == "" {
		return nil, nil
	}
	walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			log.Warn("recall substring: rel failed", "base", base, "path", path, "err", relErr)
			return nil
		}

		// Check cache first; only read disk on cache miss or stale entry.
		body := ""
		modKey := info.ModTime().Format(time.RFC3339Nano)
		if cached, ok := a.contentCache.Load(path); ok {
			ce := cached.(cacheEntry)
			if ce.modTime == modKey {
				body = ce.content
			}
		}
		if body == "" {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				log.Warn("recall search: skip unreadable file", "path", path, "err", readErr)
				return nil
			}
			body = string(data)
			a.contentCache.Store(path, cacheEntry{content: body, modTime: modKey})
		}

		if !strings.Contains(strings.ToLower(body), strings.ToLower(query)) &&
			!strings.Contains(strings.ToLower(rel), strings.ToLower(query)) {
			return nil
		}
		results = append(results, Fact{
			Key:    strings.TrimSuffix(filepath.Base(path), ".md"),
			Value:  body,
			Scope:  filepath.Dir(rel),
			Source: "recall",
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("substring walk: %w", walkErr)
	}
	if len(results) > limit && limit > 0 {
		results = results[:limit]
	}
	return results, nil
}

func (a *recallAdapter) Forget(_ context.Context, query string) (int, error) {
	if a.store == nil || query == "" {
		return 0, nil
	}
	count := 0
	base := a.store.Dir
	walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		name := strings.TrimSuffix(info.Name(), ".md")
		ql := strings.ToLower(query)
		nameMatch := strings.Contains(strings.ToLower(name), ql)
		if !nameMatch {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				log.Warn("recall forget: skip unreadable file", "path", path, "err", readErr)
				return nil
			}
			if !strings.Contains(strings.ToLower(string(data)), ql) {
				return nil
			}
		}
		if rmErr := os.Remove(path); rmErr == nil {
			count++
			a.contentCache.Delete(path)
		}
		return nil
	})
	if walkErr != nil {
		return count, walkErr
	}
	return count, nil
}

// ─── trustAdapter ─────────────────────────────────────────────────────────

// trustAdapter wraps core.ProofChain + core.AuditChain to implement kernel.Trust.
type trustAdapter struct {
	proofChain *core.ProofChain
	auditChain *core.AuditChain
}

// NewTrust creates a kernel Trust backed by proof and audit chains.
func NewTrust(pc *core.ProofChain, ac *core.AuditChain) Trust {
	return &trustAdapter{proofChain: pc, auditChain: ac}
}

func (a *trustAdapter) Record(_ context.Context, entry ProofEntry) error {
	if a.proofChain == nil {
		return nil
	}
	// The canonical SHA256 hash is computed inside ProofChain.AppendWithPath
	// (using index + atomID + proposition + evidence + prevHash) and stored
	// in the chain's entries. No need to pre-compute at the kernel level.
	a.proofChain.AppendWithPath(entry.AtomID, entry.Proposition, entry.Evidence, entry.ParentID, entry.Path)
	return nil
}

func (a *trustAdapter) Verify(_ context.Context, atomID string) error {
	if a.proofChain == nil {
		return nil
	}
	// First, verify the entire hash chain is tamper-evident.
	if err := a.proofChain.VerifyChain(); err != nil {
		return fmt.Errorf("proof chain integrity broken: %w", err)
	}
	// Then check the specific atom exists.
	for _, e := range a.proofChain.Entries {
		if e.AtomID == atomID {
			return nil
		}
	}
	return fmt.Errorf("atom %q not found in proof chain", atomID)
}

func (a *trustAdapter) Export(_ context.Context) ([]ProofEntry, error) {
	if a.proofChain == nil {
		return nil, nil
	}
	out := make([]ProofEntry, len(a.proofChain.Entries))
	for i, e := range a.proofChain.Entries {
		out[i] = ProofEntry{
			AtomID:      e.AtomID,
			Proposition: e.Proposition,
			Evidence:    e.Evidence,
			ParentID:    e.ParentID,
			Path:        e.Path,
			SHA256:      e.Hash,
		}
	}
	return out, nil
}

func (a *trustAdapter) Summary(_ context.Context) TrustSummary {
	if a.proofChain == nil {
		return TrustSummary{}
	}
	last := ""
	healthy := true
	if len(a.proofChain.Entries) > 0 {
		last = a.proofChain.Entries[len(a.proofChain.Entries)-1].Proposition
	}
	// Verify the chain is intact for the health status.
	if err := a.proofChain.VerifyChain(); err != nil {
		healthy = false
	}
	return TrustSummary{
		EntryCount: len(a.proofChain.Entries),
		LastAction: last,
		Healthy:    healthy,
	}
}

// ─── learnAdapter ─────────────────────────────────────────────────────────

// learnAdapter implements kernel.Learn. It can operate in two modes:
//  1. Standalone (skillsDir + in-memory skills) — scaffolding, deprecated.
//  2. Delegating (engine set) — forwards to evolution.Engine, the canonical impl.
type learnAdapter struct {
	skillsDir string
	skills    []Skill
	engine    Learn // optional delegate; when non-nil, all calls forward here
}

// NewLearn creates a kernel Learn primitive. skillsDir is where generated
// skills are stored (empty = no persistence).
func NewLearn(skillsDir string) Learn {
	return &learnAdapter{skillsDir: skillsDir}
}

// NewLearnFromEngine creates a kernel Learn backed by an external engine
// (typically evolution.Engine). All calls are forwarded to engine.
func NewLearnFromEngine(engine Learn) Learn {
	return &learnAdapter{engine: engine}
}

func (a *learnAdapter) Extract(ctx context.Context, task TaskRecord) ([]Pattern, error) {
	if a.engine != nil {
		return a.engine.Extract(ctx, task)
	}
	if task.ToolCalls == nil || !task.Success {
		return nil, nil
	}
	seq := make([]string, 0, len(task.ToolCalls))
	for _, tc := range task.ToolCalls {
		seq = append(seq, tc.Name)
	}
	if len(seq) < 2 {
		return nil, nil
	}
	return []Pattern{{
		ID:           fmt.Sprintf("p-%d", time.Now().UnixNano()),
		Description:  fmt.Sprintf("Tool sequence: %s", strings.Join(seq, " → ")),
		ToolSequence: seq,
		Frequency:    1,
		Confidence:   0.5,
	}}, nil
}

func (a *learnAdapter) Generate(ctx context.Context, patterns []Pattern) (Skill, error) {
	if a.engine != nil {
		return a.engine.Generate(ctx, patterns)
	}
	if len(patterns) == 0 {
		return Skill{}, fmt.Errorf("no patterns to generate from; provide patterns via extract first")
	}
	p := patterns[0]
	name := "auto-" + strings.ReplaceAll(strings.ToLower(p.Description), " ", "-")
	if len(name) > 40 {
		name = name[:40]
	}
	body := fmt.Sprintf("## Auto-generated skill\n\nPattern: %s\n\nTool sequence:\n%s\n\n---\n\n",
		p.Description, strings.Join(p.ToolSequence, "\n"))
	return Skill{
		Name:        name,
		Description: p.Description,
		Body:        body,
		Source:      "extracted",
		Version:     1,
	}, nil
}

func (a *learnAdapter) Validate(ctx context.Context, skill Skill) error {
	if a.engine != nil {
		return a.engine.Validate(ctx, skill)
	}
	if skill.Name == "" || skill.Body == "" {
		return fmt.Errorf("skill name and body required")
	}
	return nil
}

func (a *learnAdapter) Publish(ctx context.Context, skill Skill) error {
	if a.engine != nil {
		return a.engine.Publish(ctx, skill)
	}
	if a.skillsDir == "" {
		a.skills = append(a.skills, skill)
		return nil
	}
	if err := os.MkdirAll(a.skillsDir, 0755); err != nil {
		return fmt.Errorf("skills dir: %w", err)
	}
	path := filepath.Join(a.skillsDir, skill.Name+".md")
	return os.WriteFile(path, []byte(skill.Body), 0644)
}

func (a *learnAdapter) Stats(ctx context.Context) LearnStats {
	if a.engine != nil {
		return a.engine.Stats(ctx)
	}
	// Count skills from disk when skillsDir is set, so Stats reflects
	// what's actually persisted rather than just the in-memory slice.
	total := len(a.skills)
	if a.skillsDir != "" {
		if entries, err := os.ReadDir(a.skillsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					total++
				}
			}
		} else {
			log.Warn("learn stats: read skills dir", "path", a.skillsDir, "err", err)
		}
	}
	return LearnStats{
		TotalSkills:   total,
		SuccessRate:   1.0,
		AvgConfidence: 0.5,
	}
}
