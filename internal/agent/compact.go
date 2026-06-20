package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/provider"
)

// Compaction defaults. Compaction is a low-frequency cache-reset point: prompts
// grow prepend-only (high cache hits) until a turn's prompt nears the model's
// context window, then we compact once — summarizing the older history and
// archiving the originals — so a long task can keep going.
const (
	defaultCompactRatio = 0.8 // compact when prompt_tokens reach this fraction of the window
	defaultRecentKeep   = 8   // recent messages kept verbatim, never summarized
	minCompactMessages  = 8   // skip compaction below this many compactable messages; fewer and the LLM summary costs more tokens than it saves
)

// summarySystemPrompt steers the executor to distill older history into a
// briefing it can keep relying on after the originals are dropped.
const summarySystemPrompt = `Compact this conversation into a terse briefing. Preserve: user's goal and constraints, decisions with rationale, files touched, command outcomes, pending items. Use bullets. Invent nothing.`

// maybeCompact compacts the session when the last turn's prompt has grown to the
// configured fraction of the context window. A hysteresis guard (compactedLast)
// prevents consecutive compactions — after a compaction succeeds, the next turn
// is skipped so the new prefix can stabilize in the API cache.
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	a.compactedLastMu.Lock()
	if a.compactedLast {
		a.compactedLast = false
		a.compactedLastMu.Unlock()
		return
	}
	a.compactedLastMu.Unlock()
	if u.PromptTokens < int(float64(a.contextWindow)*a.compactRatio) {
		return
	}
	// Estimate whether compaction saves enough tokens to be worth the
	// summarizer API call. A summary costs ~maxSum tokens and the overhead
	// of the archive write; if the region is too small, skipping is cheaper.
	if msgs := a.session.Snapshot(); len(msgs) > 0 {
		head, start, ok := compactBounds(msgs, a.recentKeep, minCompactMessages)
		if ok {
			// Estimate region bytes from raw content + overhead instead of
			// building the full transcript (which renderTranscript does on
			// the next call in summarize — avoid doing it twice).
			regionBytes := 0
			for _, m := range msgs[head:start] {
				regionBytes += len(m.Content) + len(m.ReasoningContent) + 80 // 80 = formatting overhead per msg
			}
			regionTokens := regionBytes / 4 // rough estimate, good enough for ROI
			maxSum := 2000
			if a.contextWindow > 0 {
				if h := int(float64(a.contextWindow)*(1-a.compactRatio)) / 2; h < maxSum {
					maxSum = h
				}
			}
			// When the context window is too small, the ROI calculation
			// becomes unreliable (headroom approaches zero).  Bypass the
			// cost/benefit check and compact anyway — running out of room
			// in a tiny window is worse than a wasted summarizer call.
			minSavings := 200
			if maxSum < 200 {
				minSavings = maxSum / 2
				if minSavings < 0 {
					minSavings = 0
				}
			}
			if regionTokens-maxSum < minSavings {
				return
			}
		}
	}
	if err := a.compact(ctx); err != nil {
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("compaction skipped: %v", err)})
		return
	}
	if a.msgbus != nil {
		a.msgbus.Pub("turn:compacting", fmt.Sprintf("tokens above threshold at %.0f%% window", a.compactRatio*100))
	}
	a.compactedLastMu.Lock()
	a.compactedLast = true
	a.compactedLastMu.Unlock()
	metrics.Compaction()
}

// compact summarizes the older middle of the session and replaces it in place:
// the session becomes system + summary + recent tail. The dropped originals are
// archived first, so the full history stays traceable.
func (a *Agent) compact(ctx context.Context) error {
	msgs := a.session.Snapshot()
	snapGen := a.session.Gen()
	head, start, ok := compactBounds(msgs, a.recentKeep, minCompactMessages)
	if !ok {
		return nil // recent tail already covers everything worth keeping
	}
	region := msgs[head:start]

	archived := ""
	if a.archiveDir != "" {
		path, err := archiveMessages(a.archiveDir, region)
		if err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		archived = path
	}

	summary, err := a.summarize(ctx, region)
	if err != nil {
		if archived != "" {
			os.Remove(archived)
		}
		return err
	}

	compacted := make([]provider.Message, 0, head+1+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of earlier conversation (older messages were compacted to save context):\n" + summary,
	})
	compacted = append(compacted, msgs[start:]...)
	if !a.session.ReplaceIfUnchanged(compacted, snapGen) {
		return fmt.Errorf("session changed during compaction — retry")
	}

	note := fmt.Sprintf("compacted %d messages → summary", len(region))
	if archived != "" {
		note += " (archived " + archived + ")"
	}
	a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: note})
	return nil
}

// compactBounds locates the region to summarize. head is the count of leading
// messages preserved verbatim (the system prompt, if any); start is where the
// preserved recent tail begins, so msgs[head:start] is compacted. The boundary
// is aligned backward off any tool result so the recent tail never begins with
// an orphan tool message whose assistant tool_calls were summarized away. ok is
// false when there is too little to compact.
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

// summarize asks the executor's own provider (no tools) to distill the region
// into a briefing, returning the collected text.
func (a *Agent) summarize(ctx context.Context, region []provider.Message) (string, error) {
	// Bound the summary size: at most 2000 tokens, or half the compaction
	// headroom, whichever is smaller. An unbounded summary can cost more
	// tokens than it saves.
	maxSum := 2000
	if a.contextWindow > 0 {
		headroom := int(float64(a.contextWindow) * (1 - a.compactRatio))
		if headroom/2 < maxSum {
			maxSum = headroom / 2
		}
	}
	if maxSum < 500 {
		maxSum = 500 // floor: too small a summary is useless
	}
	// Apply a short timeout to the summarizer call — it produces at most
	// maxSum tokens (typically ≤2000), so 30 s is generous. Without this a
	// hung provider stream blocks compaction (and thus the turn) indefinitely.
	sumCtx, sumCancel := context.WithTimeout(ctx, 30*time.Second)
	defer sumCancel()
	ch, err := a.prov.Stream(sumCtx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: summarySystemPrompt},
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
				goto done
			}
			switch chunk.Type {
			case provider.ChunkText:
				b.WriteString(chunk.Text)
			case provider.ChunkError:
				return "", chunk.Err
			default: // unknown chunk type — ignore
			}
		}
	}
done:
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "", fmt.Errorf("summarizer returned empty output")
	}
	return s, nil
}

// renderTranscript flattens messages into a readable transcript for summarization.
// Tool results longer than 1200 chars are head+tail truncated so the summarizer
// sees the gist without paying input-token cost for full grep/bash output dumps.
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
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, tc.Arguments)
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
		default: // system/tool roles — skip
		}
	}
	return b.String()
}

// truncateToolResult preserves the head and tail of a long tool result for the
// summarizer. The summarizer only needs to know what was searched and whether it
// found anything — not the full output. Head 700 + tail 400 chars keeps the
// command/pattern and any errors/failures visible while dropping the bulk.
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

// archiveMessages writes the dropped originals to a timestamped .jsonl (one
// message per line) under dir, returning the file path.
func archiveMessages(dir string, msgs []provider.Message) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// 4-byte random suffix prevents same-millisecond collisions.
	rnd := make([]byte, 4)
	if _, err := rand.Read(rnd); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405.000")+"-"+hex.EncodeToString(rnd)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	// Defer clean-up for early-return (error) paths only. On the happy path we
	// close f explicitly before ok = true so that a Close failure becomes a
	// returned error, not a deleted file the caller already got a path to.
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

	// Close before declaring success so a flush/close failure is surfaced.
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close archive: %w", err)
	}
	needsCleanup = false

	// Rotate: keep only the keepMost most recent archive files.
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

// preTurnCheck estimates whether the current context is close to overflowing
// and emits a proactive warning. This runs before the turn starts, so the
// agent can decide to /compact manually or accept the risk.
func (a *Agent) preTurnCheck() {
	if a.contextWindow <= 0 {
		return
	}
	// Estimate current token count from session messages.
	msgs := a.session.Snapshot()
	estTokens := 0
	for _, m := range msgs {
		estTokens += len(m.Content)/4 + len(m.ReasoningContent)/4 + 20 // rough estimate
	}

	usagePct := float64(estTokens) / float64(a.contextWindow) * 100
	switch {
	case usagePct >= 95:
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("⚠️  context near limit (~%.0f%% of %d tokens). The model may truncate responses. Consider /compact or /new.",
				usagePct, a.contextWindow)})
	case usagePct >= 85:
		a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("context high (~%.0f%% used). Compaction will trigger after this turn if needed.",
				usagePct)})
	case usagePct >= 70:
		// Silent — normal operating range, compaction will handle it.
	}
}
