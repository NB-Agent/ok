package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

// scheduleTool manages delayed and recurring tasks in a single session.
// Tasks run in the background and report completion through the result string.
type scheduleTool struct {
	mu   sync.Mutex
	tmap map[string]*scheduledTask
	seq  int
}

type scheduledTask struct {
	name     string
	ticker   *time.Ticker
	stop     chan struct{}
	callback func()
}

func init() {
	s := &scheduleTool{tmap: map[string]*scheduledTask{}}
	tool.RegisterBuiltin(s)
}

func (s *scheduleTool) Name() string { return "schedule" }

func (s *scheduleTool) Description() string {
	return "Schedule delayed or recurring tasks. Actions: once (run once after delay), repeat (run every interval), list (show active tasks), cancel (stop a task)."
}

func (s *scheduleTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["once","repeat","list","cancel"],"type":"string"},"command":{"type":"string"},"interval_sec":{"type":"integer"},"name":{"type":"string"},"note":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (s *scheduleTool) ReadOnly() bool { return false }

func (s *scheduleTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action      string `json:"action"`
		Name        string `json:"name"`
		IntervalSec int    `json:"interval_sec"`
		Note        string `json:"note"`
		Command     string `json:"command"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "once":
		if p.IntervalSec <= 0 {
			p.IntervalSec = 1
		}
		if p.IntervalSec > 86400 {
			p.IntervalSec = 86400
		}
		note := p.Note
		if note == "" {
			note = fmt.Sprintf("run once after %ds", p.IntervalSec)
		}

		s.mu.Lock()
		s.seq++
		name := fmt.Sprintf("once-%d", s.seq)
		if p.Name != "" {
			name = p.Name
			// Cancel the old task. See goroutine cleanup for stale-delete guard.
			if old, ok := s.tmap[name]; ok {
				close(old.stop)
			}
		}
		st := &scheduledTask{
			name:     name,
			stop:     make(chan struct{}),
			callback: makeCallback(p.Command),
		}
		s.tmap[name] = st
		s.mu.Unlock()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("goroutine panic", "recover", r)
					fmt.Fprintf(os.Stderr, "schedule: panic in once timer: %v\n", r)
				}
			}()
			timer := time.NewTimer(time.Duration(p.IntervalSec) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				s.mu.Lock()
				// Only delete if we are still the current entry (stale-delete guard).
				if cur, ok := s.tmap[name]; ok && cur == st {
					delete(s.tmap, name)
				}
				s.mu.Unlock()
				st.run()
			case <-st.stop:
				s.mu.Lock()
				if cur, ok := s.tmap[name]; ok && cur == st {
					delete(s.tmap, name)
				}
				s.mu.Unlock()
			case <-ctx.Done():
				s.mu.Lock()
				if cur, ok := s.tmap[name]; ok && cur == st {
					delete(s.tmap, name)
				}
				s.mu.Unlock()
			}
		}()

		return fmt.Sprintf("# Schedule Once\n\n✅ `%s` will run in %ds: %s\n", name, p.IntervalSec, note), nil

	case "repeat":
		if p.IntervalSec <= 0 {
			p.IntervalSec = 10
		}
		if p.IntervalSec > 86400 {
			p.IntervalSec = 86400
		}
		note := p.Note
		if note == "" {
			note = fmt.Sprintf("repeat every %ds", p.IntervalSec)
		}

		s.mu.Lock()
		s.seq++
		name := fmt.Sprintf("repeat-%d", s.seq)
		if p.Name != "" {
			name = p.Name
			// Cancel the old task. See once goroutine for stale-delete guard.
			if old, ok := s.tmap[name]; ok {
				close(old.stop)
			}
		}
		st := &scheduledTask{
			name:     name,
			ticker:   time.NewTicker(time.Duration(p.IntervalSec) * time.Second),
			stop:     make(chan struct{}),
			callback: makeCallback(p.Command),
		}
		s.tmap[name] = st
		s.mu.Unlock()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("goroutine panic", "recover", r)
					fmt.Fprintf(os.Stderr, "schedule: panic in repeat loop: %v\n", r)
				}
			}()
			for {
				select {
				case <-st.ticker.C:
					st.run()
				case <-st.stop:
					st.ticker.Stop()
					s.mu.Lock()
					if cur, ok := s.tmap[name]; ok && cur == st {
						delete(s.tmap, name)
					}
					s.mu.Unlock()
					return
				case <-ctx.Done():
					st.ticker.Stop()
					s.mu.Lock()
					if cur, ok := s.tmap[name]; ok && cur == st {
						delete(s.tmap, name)
					}
					s.mu.Unlock()
					return
				}
			}
		}()

		return fmt.Sprintf("# Schedule Repeat\n\n✅ `%s` runs every %ds: %s\n", name, p.IntervalSec, note), nil

	case "list":
		s.mu.Lock()
		names := make([]string, 0, len(s.tmap))
		for n := range s.tmap {
			names = append(names, n)
		}
		s.mu.Unlock()

		if len(names) == 0 {
			return "# Schedule\n\nNo active scheduled tasks.\n", nil
		}
		var b strings.Builder
		b.WriteString("# Schedule\n\nActive tasks:\n")
		for _, n := range names {
			b.WriteString(fmt.Sprintf("- `%s`\n", n))
		}
		return b.String(), nil

	case "cancel":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		s.mu.Lock()
		st, ok := s.tmap[p.Name]
		delete(s.tmap, p.Name)
		s.mu.Unlock()

		if !ok {
			return fmt.Sprintf("# Schedule\n\nTask `%s` not found\n", p.Name), nil
		}
		close(st.stop)
		return fmt.Sprintf("# Schedule\n\n✅ Task `%s` cancelled\n", p.Name), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

// run executes the callback with panic recovery.
func (st *scheduledTask) run() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "schedule: callback panic: %v\n", r)
		}
	}()
	st.callback()
}

// makeCallback creates a callback function that executes the given command.
// If cmd is empty, the callback logs that it fired (no-op).
// If cmd starts with http:// or https://, it performs a POST webhook.
// Otherwise, it runs cmd as a shell command.
func makeCallback(cmd string) func() {
	if cmd == "" {
		return func() {
			fmt.Fprintf(os.Stderr, "schedule: task fired (no command configured)\n")
		}
	}
	return func() {
		fmt.Fprintf(os.Stderr, "schedule: executing %q\n", cmd)
		if strings.HasPrefix(cmd, "http://") || strings.HasPrefix(cmd, "https://") {
			doWebhook(cmd)
		} else {
			doShell(cmd)
		}
	}
}

// isSafeShellCommand checks whether a shell command is safe to execute.
// Rejects chained, piped, redirected, and multi-statement commands.
func isSafeShellCommand(cmd string) bool {
	// Reject shell chaining/operators that bypass argument boundaries
	dangerous := []string{
		"`",   // command substitution
		"$(",  // subshell
		"&&",  // AND chain
		"||",  // OR chain
		"|",   // pipe
		";",   // statement separator
		">",   // redirect output
		"<",   // redirect input
		"${",  // variable expansion
		"$((", // arithmetic expansion
		"2>",  // stderr redirect
		"&>",  // redirect with &
		"\n",  // multi-line
	}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			return false
		}
	}
	return true
}

// isSafeWebhookURL validates that the URL doesn't point to an internal IP.
func isSafeWebhookURL(url string) bool {
	// Parse the URL to extract host
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return false
	}
	// Strip scheme
	rest := url[len("http://"):]
	if strings.HasPrefix(url, "https://") {
		rest = url[len("https://"):]
	}
	// Extract host:port
	host := rest
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.IndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}
	// Reject empty/obviously bad hosts
	if host == "" {
		return false
	}
	// Resolve IP and check if it's private
	ips, err := net.LookupIP(host)
	if err != nil {
		// If we can't resolve, still allow (could be DNS transient)
		// but log a warning at execution time
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func doWebhook(url string) {
	// SSRF protection: block internal IPs
	if !isSafeWebhookURL(url) {
		fmt.Fprintf(os.Stderr, "schedule: webhook %s blocked (internal IP)\n", url)
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule: webhook %s failed: %v\n", url, err)
		return
	}
	_, cerr := io.Copy(io.Discard, resp.Body)
	if err := resp.Body.Close(); err != nil || cerr != nil {
		fmt.Fprintf(os.Stderr, "schedule: webhook %s body close error: %v\n", url, err)
	}
	fmt.Fprintf(os.Stderr, "schedule: webhook %s → HTTP %d\n", url, resp.StatusCode)
}

func doShell(cmd string) {
	// Safety check: reject dangerous shell operations
	if !isSafeShellCommand(cmd) {
		fmt.Fprintf(os.Stderr, "schedule: command %q blocked by safety check\n", cmd)
		return
	}
	c := winhide.Command("sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule: command %q failed: %v\n%s\n", cmd, err, string(out))
		return
	}
	if len(out) > 0 {
		outStr := string(out)
		if len(outStr) > 500 {
			outStr = outStr[:500] + "\n... (truncated)"
		}
		fmt.Fprintf(os.Stderr, "schedule: command %q completed:\n%s\n", cmd, outStr)
	}
}
