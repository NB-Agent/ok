package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/provider"
)

// Compaction defaults.
const (
	defaultCompactRatio   = 0.80
	defaultRecentKeep     = 8
	minCompactMessagesOld = 8 // legacy value referenced by compact_test.go

	defaultSoftCompactRatio   = 0.50
	defaultCompactForceRatio  = 0.90
	defaultCompactTarget      = 0.50
	defaultTailTokens         = 16384
	minRecentKeep             = 2
	minCompactMessages        = 2
	fallbackTokPerChar        = 0.25
	maxPinnedFirstUserTokens  = 1500
	pinnedFirstUserWindowFrac = 0.15
)

const (
	summaryTagOpen  = "<compaction-summary>"
	summaryTagClose = "</compaction-summary>"
	summaryTimeout  = 90 * time.Second
)

const summarySystemPrompt = `Compact this conversation into a terse briefing. Preserve: user's goal and constraints, decisions with rationale, files touched, command outcomes, pending items. Use bullets. Invent nothing.`

// ─── soft/force ratio helpers ─────────────────────────────────────────────

func (a *Agent) softRatio() float64 {
	if a.softCompactRatio > 0 {
		return a.softCompactRatio
	}
	return defaultSoftCompactRatio
}

func (a *Agent) forceRatio() float64 {
	if a.compactForceRatio > 0 {
		return a.compactForceRatio
	}
	return defaultCompactForceRatio
}

// ─── maybeCompact ──────────────────────────────────────────────────────────

func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	high := int(float64(a.contextWindow) * a.compactRatio)
	soft := int(float64(a.contextWindow) * a.softRatio())

	// Phase 5: soft band — report without rewriting prefix.
	if u.PromptTokens >= soft && u.PromptTokens < high && !a.softCompactNoticed {
		a.softCompactNoticed = true
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: fmt.Sprintf("context reached %.0f%% of window; keeping cache-first prefix until compact threshold %.0f%%",
				a.softRatio()*100, a.compactRatio*100)})
		return
	}
	if u.PromptTokens < high {
		a.compactedLastMu.Lock()
		a.consecutiveCompacts = 0
		a.compactStuck = false
		a.compactedLastMu.Unlock()
		return
	}

	a.compactedLastMu.Lock()
	if a.compactStuck {
		a.compactedLastMu.Unlock()
		return
	}
	force := u.PromptTokens >= int(float64(a.contextWindow)*a.forceRatio())
	a.compactedLastMu.Unlock()

	// Phase 4: prune before folding.
	ratio := a.tokPerChar()
	if st, err := a.PruneStaleToolResults(); err == nil && st.Results > 0 {
		saved := int(float64(st.SavedChars) * ratio)
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
			"pruned %d stale tool results (~%d tokens est.) before compaction", st.Results, saved)})
		if !force && u.PromptTokens-saved < high {
			return
		}
	}

	if err := a.compact(ctx); err != nil {
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: fmt.Sprintf("compaction skipped: %v", err)})
		return
	}

	// Phase 3: stuck detection.
	a.compactedLastMu.Lock()
	a.consecutiveCompacts++
	if a.consecutiveCompacts >= 2 {
		a.compactStuck = true
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: fmt.Sprintf(
			"context_window=%d is too small for compaction to help; auto-compaction paused.",
			a.contextWindow)})
	}
	a.compactedLastMu.Unlock()
	metrics.Compaction()
}

// ─── compact / compactWith ─────────────────────────────────────────────────

func (a *Agent) compact(ctx context.Context) error {
	return a.compactWith(ctx, "auto", "", false)
}

func (a *Agent) compactWith(ctx context.Context, trigger, instructions string, force bool) error {
	msgs := a.session.Snapshot()
	snapGen := a.session.Gen()
	head, start, ok := a.planCompaction(msgs, minCompactMessages)
	if !ok {
		head, start, ok = a.planCompaction(msgs, 1)
	}
	if !ok {
		return nil
	}
	region := msgs[head:start]

	kept, fold := a.partitionFold(region)
	if len(fold) == 0 {
		return nil
	}

	if !force && !foldEconomics(fold) {
		return nil
	}

	archived := ""
	if a.archiveDir != "" {
		path, err := archiveMessages(a.archiveDir, fold)
		if err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		archived = path
	}

	summary, err := a.summarizeWithRetry(ctx, fold, instructions)
	if err != nil {
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "compaction summary unavailable (" + err.Error() + "); folded mechanically"})
		summary = mechanicalFoldDigest(len(fold), archived)
	}

	compacted := make([]provider.Message, 0, head+len(kept)+1+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, kept...)
	compacted = append(compacted, provider.Message{
		Role: provider.RoleUser,
		Content: summaryTagOpen + "\n" +
			"Summary of earlier conversation (older messages were compacted to save context):\n" +
			summary + "\n" +
			summaryTagClose,
	})
	compacted = append(compacted, msgs[start:]...)
	if !a.session.ReplaceIfUnchanged(compacted, snapGen) {
		return fmt.Errorf("session changed during compaction — retry")
	}

	note := fmt.Sprintf("compacted %d messages → summary", len(fold))
	if archived != "" {
		note += " (archived " + archived + ")"
	}
	a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: note})
	return nil
}

// ─── Legacy compactBounds (backward compat for tests) ──────────────────────

func compactBounds(msgs []provider.Message, recentKeep, minCompact int) (head, start int, ok bool) {
	if len(msgs) == 0 || recentKeep <= 0 {
		return 0, 0, false
	}
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		head = 1
	}
	start = len(msgs) - recentKeep
	if start < 0 {
		start = 0
	}
	if start <= head {
		return head, start, false
	}
	if start >= len(msgs) {
		start = len(msgs) - 1
	}
	for start > head && msgs[start].Role == provider.RoleTool {
		start--
	}
	if start-head < minCompact {
		return head, start, false
	}
	return head, start, true
}

// ─── planCompaction ───────────────────────────────────────────────────────

func (a *Agent) planCompaction(msgs []provider.Message, min int) (head, start int, ok bool) {
	head = a.pinnedPrefixLen(msgs)
	if a.contextWindow > 0 {
		budget := defaultTailTokens
		if maxByWin := int(float64(a.contextWindow) * defaultCompactTarget); maxByWin < budget {
			budget = maxByWin
		}
		start = tailStart(msgs, head, budget, a.tokPerChar(), a.tailFloor())
	} else {
		start = len(msgs) - a.tailFloor()
		for start > head && start < len(msgs) && msgs[start].Role == provider.RoleTool {
			start--
		}
	}
	if start < head {
		start = head
	}
	if start-head < min {
		return head, start, false
	}
	return head, start, true
}

func (a *Agent) tailFloor() int {
	if a.recentKeep > minRecentKeep {
		return a.recentKeep
	}
	return minRecentKeep
}

func tailStart(msgs []provider.Message, head, budgetTokens int, tokPerChar float64, minKeep int) int {
	start := len(msgs)
	acc := 0
	for i := len(msgs) - 1; i > head; i-- {
		c := int(float64(msgChars(msgs[i])) * tokPerChar)
		if len(msgs)-i > minKeep && acc+c > budgetTokens {
			break
		}
		acc += c
		start = i
	}
	for start > head && start < len(msgs) && msgs[start].Role == provider.RoleTool {
		start--
	}
	return start
}

// ─── pinned prefix ────────────────────────────────────────────────────────

func (a *Agent) pinnedPrefixLen(msgs []provider.Message) int {
	i := 0
	if i < len(msgs) && msgs[i].Role == provider.RoleSystem {
		i++
	}
	if i < len(msgs) && msgs[i].Role == provider.RoleUser && !isCompactionSummary(msgs[i]) && a.pinnableUserTurn(msgs[i]) {
		i++
	}
	for i < len(msgs) && isCompactionSummary(msgs[i]) {
		i++
	}
	return i
}

func (a *Agent) pinnableUserTurn(m provider.Message) bool {
	budget := maxPinnedFirstUserTokens
	if a.contextWindow > 0 {
		if f := int(float64(a.contextWindow) * pinnedFirstUserWindowFrac); f < budget {
			budget = f
		}
	}
	return int(float64(msgChars(m))*a.tokPerChar()) <= budget
}

func isCompactionSummary(m provider.Message) bool {
	return m.Role == provider.RoleUser &&
		strings.HasPrefix(strings.TrimLeft(m.Content, "\n "), summaryTagOpen)
}

// ─── partitionFold ────────────────────────────────────────────────────────

func (a *Agent) partitionFold(region []provider.Message) (kept, fold []provider.Message) {
	policyKeep := keepIndexes(region, a.keepPolicy)
	for i, m := range region {
		if policyKeep[i] || isCompactionSummary(m) {
			kept = append(kept, m)
		} else {
			fold = append(fold, m)
		}
	}
	return kept, fold
}

func keepIndexes(region []provider.Message, policy KeepPolicy) []bool {
	keep := make([]bool, len(region))
	policyStart := 0
	for i, m := range region {
		if isCompactionSummary(m) {
			policyStart = i + 1
		}
	}
	for i, m := range region {
		if i >= policyStart && shouldKeepMessage(m, policy) {
			keep[i] = true
		}
	}
	for i, m := range region {
		if !keep[i] {
			continue
		}
		switch m.Role {
		case provider.RoleTool:
			if j := findToolCaller(region, i, m.ToolCallID); j >= 0 {
				keepToolCallGroup(region, keep, j)
			}
		case provider.RoleAssistant:
			keepToolCallGroup(region, keep, i)
		}
	}
	return keep
}

func keepToolCallGroup(region []provider.Message, keep []bool, assistantIndex int) {
	if assistantIndex < 0 || assistantIndex >= len(region) {
		return
	}
	m := region[assistantIndex]
	if m.Role != provider.RoleAssistant || len(m.ToolCalls) == 0 {
		return
	}
	keep[assistantIndex] = true
	ids := toolCallIDs(m)
	for j := assistantIndex + 1; j < len(region) && region[j].Role == provider.RoleTool; j++ {
		if ids[region[j].ToolCallID] {
			keep[j] = true
		}
	}
}

func shouldKeepMessage(m provider.Message, policy KeepPolicy) bool {
	if policy&KeepErrors != 0 && isErrorMessage(m) {
		return true
	}
	if policy&KeepUserMarked != 0 && isUserMarked(m) {
		return true
	}
	return false
}

func isErrorMessage(m provider.Message) bool {
	if m.Role != provider.RoleTool {
		return false
	}
	s := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(s, "error:") || strings.HasPrefix(s, "blocked:")
}

func isUserMarked(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	content := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(content, "[[keep]]") ||
		strings.HasPrefix(content, "[keep]") ||
		strings.HasPrefix(content, "<keep>") ||
		strings.HasPrefix(content, "<!-- keep -->")
}

func findToolCaller(region []provider.Message, toolIndex int, id string) int {
	for i := toolIndex - 1; i >= 0; i-- {
		if region[i].Role != provider.RoleAssistant {
			continue
		}
		for _, tc := range region[i].ToolCalls {
			if tc.ID == id {
				return i
			}
		}
	}
	return -1
}

func toolCallIDs(m provider.Message) map[string]bool {
	ids := make(map[string]bool, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		ids[tc.ID] = true
	}
	return ids
}

// ─── foldEconomics ────────────────────────────────────────────────────────

func foldEconomics(region []provider.Message) bool {
	const minFoldTokens = 400
	return estimateMessagesTokens(region) >= minFoldTokens
}

func estimateMessagesTokens(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += 4
		total += estimateTextTokens(m.Content)
		total += estimateTextTokens(m.ReasoningContent)
		total += estimateTextTokens(m.Name)
		total += estimateTextTokens(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			total += 8
			total += estimateTextTokens(tc.ID)
			total += estimateTextTokens(tc.Name)
			total += estimateTextTokens(tc.Arguments)
		}
	}
	return total
}

func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	bytes := len(s)
	runes := utf8.RuneCountInString(s)
	byBytes := (bytes + 3) / 4
	if runes > byBytes {
		return runes
	}
	return byBytes
}

// ─── token estimation ─────────────────────────────────────────────────────

func (a *Agent) tokPerChar() float64 {
	if u := a.usage.LastUsage(); u != nil && u.PromptTokens > 0 {
		if c := charsOfMessages(a.session.Snapshot()); c > 0 {
			if r := float64(u.PromptTokens) / float64(c); r > 0.05 && r < 2 {
				return r
			}
		}
	}
	return fallbackTokPerChar
}

func msgChars(m provider.Message) int {
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Name) + len(tc.Arguments)
	}
	return n
}

func charsOfMessages(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += msgChars(m)
	}
	return n
}

// ─── summarize ─────────────────────────────────────────────────────────────

func (a *Agent) summarizeWith(ctx context.Context, region []provider.Message, instructions string) (string, error) {
	maxSum := 2000
	if a.contextWindow > 0 {
		headroom := int(float64(a.contextWindow) * (1 - a.compactRatio))
		if headroom/2 < maxSum {
			maxSum = headroom / 2
		}
	}
	if maxSum < 500 {
		maxSum = 500
	}
	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	sys := summarySystemPrompt
	if strings.TrimSpace(instructions) != "" {
		sys += "\n\nAdditional focus for this compaction:\n" + strings.TrimSpace(instructions)
	}
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: sys},
			{Role: provider.RoleUser, Content: renderTranscript(region)},
		},
		Temperature: a.temperature,
		MaxTokens:   maxSum,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				s := strings.TrimSpace(b.String())
				if s == "" {
					return "", fmt.Errorf("summarizer returned empty output")
				}
				return s, nil
			}
			switch chunk.Type {
			case provider.ChunkText:
				b.WriteString(chunk.Text)
			case provider.ChunkError:
				return "", chunk.Err
			}
		}
	}
}

func (a *Agent) summarizeWithRetry(ctx context.Context, fold []provider.Message, instructions string) (string, error) {
	summary, err := a.summarizeWith(ctx, fold, instructions)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return summary, err
	}
	return a.summarizeWith(ctx, fold, instructions)
}

func mechanicalFoldDigest(n int, archive string) string {
	where := "."
	if archive != "" {
		where = " (archived to " + archive + ")."
	}
	return fmt.Sprintf("%d earlier message(s) were folded to free context, but the automatic summary was unavailable%s Ask the user if you need details from before this point.", n, where)
}

// ─── renderTranscript ─────────────────────────────────────────────────────

func renderTranscript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "[user]\n%s\n\n", m.Content)
		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "[assistant]\n%s\n", m.Content)
			}
			if m.ReasoningContent != "" {
				reasoningLen := len(m.ReasoningContent)
				if reasoningLen > 500 {
					fmt.Fprintf(&b, "[assistant reasoning] …%d chars… [/assistant reasoning]\n", reasoningLen)
				} else {
					fmt.Fprintf(&b, "[assistant reasoning]\n%s\n[/assistant reasoning]\n", m.ReasoningContent)
				}
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, summarizeToolArgs(tc.Arguments))
			}
			b.WriteString("\n")
		case provider.RoleTool:
			result := m.Content
			if len(result) > 1200 {
				result = truncateToolResult(result)
			}
			fmt.Fprintf(&b, "[tool %s result]\n%s\n\n", m.Name, result)
		case provider.RoleSystem:
			fmt.Fprintf(&b, "[system]\n%s\n\n", m.Content)
		}
	}
	return b.String()
}

func summarizeToolArgs(args string) string {
	if args == "" {
		return "(no arguments)"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return fmt.Sprintf("(%d bytes)", len(args))
	}
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("{%s} (%d keys)", strings.Join(keys, ", "), len(parsed))
}

// ─── truncation ───────────────────────────────────────────────────────────

func truncateToolResult(s string) string {
	const headChars = 700
	const tailChars = 400
	r := []rune(s)
	if len(r) <= headChars+tailChars {
		return s
	}
	head := string(r[:headChars])
	tail := string(r[len(r)-tailChars:])
	return head + fmt.Sprintf("\n\n… (%d chars truncated — full output in archives)\n\n", len(r)-headChars-tailChars) + tail
}

// ─── archive ──────────────────────────────────────────────────────────────

func archiveMessages(dir string, msgs []provider.Message) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	rnd := make([]byte, 4)
	if _, err := rand.Read(rnd); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405.000")+"-"+hex.EncodeToString(rnd)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	needsCleanup := true
	defer func() {
		if needsCleanup {
			f.Close()
			os.Remove(path)
		}
	}()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return "", err
		}
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close archive: %w", err)
	}
	needsCleanup = false

	pruneOldArchives(dir, 20)
	return path, nil
}

func pruneOldArchives(dir string, keepMost int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keepMost {
		return
	}
	type named struct {
		name string
		mod  int64
	}
	var files []named
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, named{name: e.Name(), mod: info.ModTime().UnixNano()})
		}
	}
	if len(files) <= keepMost {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	for _, f := range files[keepMost:] {
		os.Remove(filepath.Join(dir, f.name))
	}
}

// ─── PruneStaleToolResults ────────────────────────────────────────────────

const prunedMarker = "[elided tool result — "

const minPruneBytes = 1024

type PruneStats struct {
	Results    int
	SavedChars int
	Archive    string
}

func (a *Agent) PruneStaleToolResults() (PruneStats, error) {
	var st PruneStats
	if a.contextWindow <= 0 {
		return st, nil
	}
	msgs := a.session.Messages
	head, start, ok := a.planCompaction(msgs, 1)
	if !ok {
		return st, nil
	}
	var idx []int
	for i := head; i < start; i++ {
		m := msgs[i]
		if m.Role != provider.RoleTool || len(m.Content) < minPruneBytes || strings.HasPrefix(m.Content, prunedMarker) {
			continue
		}
		if a.keepPolicy&KeepErrors != 0 && isErrorMessage(m) {
			continue
		}
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return st, nil
	}
	if a.archiveDir != "" {
		originals := make([]provider.Message, 0, len(idx))
		for _, i := range idx {
			originals = append(originals, msgs[i])
		}
		path, err := archiveMessages(a.archiveDir, originals)
		if err != nil {
			return st, fmt.Errorf("archive: %w", err)
		}
		st.Archive = path
	}
	next := append([]provider.Message(nil), msgs...)
	for _, i := range idx {
		m := next[i]
		placeholder := fmt.Sprintf("%s%s, %d bytes dropped to save context; re-run the tool if the data is needed again]", prunedMarker, m.Name, len(m.Content))
		st.SavedChars += len(m.Content) - len(placeholder)
		m.Content = placeholder
		next[i] = m
		st.Results++
	}
	a.session.Replace(next)
	return st, nil
}

// ─── preTurnCheck ─────────────────────────────────────────────────────────

func (a *Agent) preTurnCheck() {
	if a.contextWindow <= 0 {
		return
	}
	msgs := a.session.Snapshot()
	estTokens := 0
	for _, m := range msgs {
		estTokens += len(m.Content)/4 + len(m.ReasoningContent)/4 + 20
	}

	usagePct := float64(estTokens) / float64(a.contextWindow) * 100
	switch {
	case usagePct >= 95:
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("⚠️  context near limit (~%.0f%% of %d tokens); the model may truncate responses. Consider /compact or /new.",
				usagePct, a.contextWindow)})
	case usagePct >= 85:
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("context high (~%.0f%% used). Compaction will trigger after this turn if needed.",
				usagePct)})
	}
}
