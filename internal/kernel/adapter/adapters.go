// Package adapter provides concrete implementations of the kernel interfaces.
// These adapters bridge kernel's abstract interfaces to OK's concrete
// infrastructure (sandbox, agent sessions, providers, file I/O, memory stores).
//
// The adapter package depends on many internal packages — this is by design.
// kernel/ itself defines only interfaces (zero internal dependencies); adapter/
// supplies the implementations so kernel/ stays pure.
//
// See internal/kernel/kernel.go for interface definitions.
package adapter

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

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/kernel"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/winhide"
)

// ─── Re-exported types for convenience ────────────────────────────────────

type (
	Sandbox    = kernel.Sandbox
	Session    = kernel.Session
	Provider   = kernel.Provider
	Controller = kernel.Controller
	Bash       = kernel.Bash
	ReadFile   = kernel.ReadFile
	WriteFile  = kernel.WriteFile
	EditFile   = kernel.EditFile
	Grep       = kernel.Grep
	Identity   = kernel.Identity
	Recall     = kernel.Recall
	Trust      = kernel.Trust
	Learn      = kernel.Learn
	Kernel     = kernel.Kernel
)

type (
	RunOptions      = kernel.RunOptions
	RunResult       = kernel.RunResult
	PluginIsolation = kernel.PluginIsolation
	Message         = kernel.Message
	Request         = kernel.Request
	Chunk           = kernel.Chunk
	ToolSchema      = kernel.ToolSchema
	BashOut         = kernel.BashOut
	FileContent     = kernel.FileContent
	Match           = kernel.Match
	User            = kernel.User
	Fact            = kernel.Fact
	ProofEntry      = kernel.ProofEntry
	TrustSummary    = kernel.TrustSummary
	TaskRecord      = kernel.TaskRecord
	Pattern         = kernel.Pattern
	Skill           = kernel.Skill
	LearnStats      = kernel.LearnStats
)

const (
	ChunkText      = kernel.ChunkText
	ChunkReasoning = kernel.ChunkReasoning
	ChunkToolCall  = kernel.ChunkToolCall
	ChunkUsage     = kernel.ChunkUsage
	ChunkError     = kernel.ChunkError
)

// ─── Constants ────────────────────────────────────────────────────────────

const (
	defaultCommandTimeout = 300 * time.Second
	binaryDetectPeek      = 8192
	defaultReadLineLimit  = 2000
	defaultGrepMaxMatches = 200
)

// ─── Semantic search types ────────────────────────────────────────────────

type SemanticSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]ScoredFact, error)
}

type ScoredFact struct {
	Fact
	Score float64
}

// ─── sandboxAdapter ──────────────────────────────────────────────────────

type sandboxAdapter struct {
	spec    sandbox.Spec
	workDir string
}

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

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// ─── sessionAdapter ───────────────────────────────────────────────────────

type sessionAdapter struct {
	inner *agent.Session
}

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
	return nil // intentionally no-op; agent-level compaction is independent
}

// ─── providerAdapter ─────────────────────────────────────────────────────

type providerAdapter struct {
	inner provider.Provider
}

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
				return
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

// ─── bashAdapter ─────────────────────────────────────────────────────────

type bashAdapter struct {
	sb Sandbox
}

func NewBash(sb Sandbox) Bash {
	return &bashAdapter{sb: sb}
}

func (a *bashAdapter) Exec(ctx context.Context, command string, bg bool) BashOut {
	if bg {
		return BashOut{
			Output:   "Background execution not available at kernel level; use the 'bash' tool for background jobs.",
			ExitCode: -1,
		}
	}
	res := a.sb.Run(ctx, command, RunOptions{Network: true})
	return BashOut{Output: res.Output, ExitCode: res.ExitCode}
}

// ─── readFileAdapter ─────────────────────────────────────────────────────

type readFileAdapter struct {
	roots   []string
	workDir string
}

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

// ─── writeFileAdapter ────────────────────────────────────────────────────

type writeFileAdapter struct {
	roots   []string
	workDir string
}

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

// ─── editFileAdapter ─────────────────────────────────────────────────────

type editFileAdapter struct {
	roots   []string
	workDir string
}

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

// ─── grepAdapter ─────────────────────────────────────────────────────────

type grepAdapter struct {
	roots   []string
	workDir string
}

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
	if !info.IsDir() {
		if err := searchFile(resolved); err != nil && err != io.EOF {
			return nil, fmt.Errorf("grep %s: %w", resolved, err)
		}
	} else {
		_ = filepath.Walk(resolved, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if err := searchFile(path); err != nil && err != io.EOF {
				return err
			}
			return nil
		})
	}

	if truncated {
		return matches, fmt.Errorf("grep truncated at %d matches", maxMatches)
	}
	return matches, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────

func resolveIn(workDir, path string) string {
	if workDir == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}

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

// ─── identityAdapter ─────────────────────────────────────────────────────

type identityAdapter struct {
	mu   sync.RWMutex
	user User
}

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

// ─── recallAdapter ───────────────────────────────────────────────────────

type recallAdapter struct {
	store    *memory.Store
	semantic SemanticSearcher

	contentCache sync.Map
}

type cacheEntry struct {
	content string
	modTime string
}

func NewRecall(store *memory.Store) Recall {
	return &recallAdapter{store: store}
}

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

// ─── trustAdapter ────────────────────────────────────────────────────────

type trustAdapter struct {
	proofChain *core.ProofChain
}

func NewTrust(pc *core.ProofChain) Trust {
	return &trustAdapter{proofChain: pc}
}

func (a *trustAdapter) Record(_ context.Context, entry ProofEntry) error {
	if a.proofChain == nil {
		return nil
	}
	a.proofChain.AppendWithPath(entry.AtomID, entry.Proposition, entry.Evidence, entry.ParentID, entry.Path)
	return nil
}

func (a *trustAdapter) Verify(_ context.Context, atomID string) error {
	if a.proofChain == nil {
		return nil
	}
	if err := a.proofChain.VerifyChain(); err != nil {
		return fmt.Errorf("proof chain integrity broken: %w", err)
	}
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
	if err := a.proofChain.VerifyChain(); err != nil {
		healthy = false
	}
	return TrustSummary{
		EntryCount: len(a.proofChain.Entries),
		LastAction: last,
		Healthy:    healthy,
	}
}

// ─── learnAdapter ────────────────────────────────────────────────────────

type learnAdapter struct {
	skillsDir string
	skills    []Skill
}

func NewLearn(skillsDir string) Learn {
	return &learnAdapter{skillsDir: skillsDir}
}

func (a *learnAdapter) Extract(_ context.Context, task TaskRecord) ([]Pattern, error) {
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
		Description:  fmt.Sprintf("Tool sequence: %s", strings.Join(seq, " \u2192 ")),
		ToolSequence: seq,
		Frequency:    1,
		Confidence:   0.5,
	}}, nil
}

func (a *learnAdapter) Generate(_ context.Context, patterns []Pattern) (Skill, error) {
	if len(patterns) == 0 {
		return Skill{}, fmt.Errorf("no patterns to generate from")
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

func (a *learnAdapter) Validate(_ context.Context, skill Skill) error {
	if skill.Name == "" || skill.Body == "" {
		return fmt.Errorf("skill name and body required")
	}
	return nil
}

func (a *learnAdapter) Publish(_ context.Context, skill Skill) error {
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

func (a *learnAdapter) Stats(_ context.Context) LearnStats {
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
