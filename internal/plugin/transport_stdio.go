package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/winhide"
)

// stdioTransport speaks newline-delimited JSON-RPC 2.0 over a subprocess's
// stdin/stdout — the MCP stdio convention (one JSON message per line, no
// embedded newlines).
//
// A write-mutex serializes request writes to stdin so concurrent tool calls
// don't interleave their JSON-RPC messages. A single background reader
// goroutine reads all lines from stdout and dispatches responses by ID to
// the pending callers via channels. This split means close() can kill the
// subprocess and unblock the reader without waiting for a hung call().
type stdioTransport struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	writeMu  sync.Mutex // serializes writes to stdin
	nextID   int
	pending  map[int]chan pendingResult // id → response channel
	pendMu   sync.Mutex                 // guards pending
	readerWG sync.WaitGroup             // tracks the background reader

	onNotify func(method string, params json.RawMessage) // called on server-initiated notifications

	closeOnce sync.Once
}

type pendingResult struct {
	result json.RawMessage
	err    error
}

func newStdioTransport(ctx context.Context, s Spec) (*stdioTransport, error) {
	if s.Command == "" {
		return nil, fmt.Errorf("stdio plugin %q: command is required", s.Name)
	}
	cmd := winhide.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = append(os.Environ(), envSlice(s.Env)...)
	if s.Dir != "" {
		cmd.Dir = s.Dir // pin cwd-aware servers (e.g. CodeGraph) to the project root
	}
	cmd.Stderr = os.Stderr // surface plugin logs to the terminal

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t := &stdioTransport{
		name:    s.Name,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		pending: make(map[int]chan pendingResult),
	}
	t.readerWG.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
			}
		}()
		t.readLoop()
	}()
	return t, nil
}

// readLoop is the single goroutine reading all JSON-RPC lines from stdout.
// It dispatches each response to the pending caller by ID. Notifications
// (messages with no id) are dropped. On EOF or read error it signals all
// remaining callers and exits.
func (t *stdioTransport) readLoop() {
	defer t.readerWG.Done()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "plugin %q: readLoop panic: %v\n", t.name, r)
			t.pendMu.Lock()
			for id, ch := range t.pending {
				ch <- pendingResult{err: fmt.Errorf("plugin %q: readLoop panic: %v", t.name, r)}
				close(ch)
				delete(t.pending, id)
			}
			t.pendMu.Unlock()
		}
	}()
	for {
		line, err := t.stdout.ReadBytes('\n')
		if err != nil {
			// Pipe closed (subprocess exited, or we killed it in close()).
			// Drain all pending callers with an error.
			t.pendMu.Lock()
			for id, ch := range t.pending {
				ch <- pendingResult{err: fmt.Errorf("plugin %q: read: %w", t.name, err)}
				close(ch)
				delete(t.pending, id)
			}
			t.pendMu.Unlock()
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Route server-initiated notifications to the handler.
		var probe struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &probe); err != nil || probe.Method != "" {
			if probe.Method != "" && t.onNotify != nil {
				t.onNotify(probe.Method, probe.Params)
			}
			continue
		}
		if probe.ID == nil {
			continue
		}

		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed response — deliver as error to the caller with
			// the matching id, or drop if no caller is waiting.
			t.pendMu.Lock()
			if ch, ok := t.pending[resp.ID]; ok {
				ch <- pendingResult{err: fmt.Errorf("plugin %q: decode response: %w", t.name, err)}
				close(ch)
				delete(t.pending, resp.ID)
			}
			t.pendMu.Unlock()
			continue
		}

		t.pendMu.Lock()
		ch, ok := t.pending[resp.ID]
		if ok {
			delete(t.pending, resp.ID)
		}
		t.pendMu.Unlock()

		if !ok {
			continue // stale response for a canceled/timed-out call
		}
		if resp.Error != nil {
			ch <- pendingResult{err: fmt.Errorf("plugin %q: %w", t.name, resp.Error)}
		} else {
			ch <- pendingResult{result: resp.Result}
		}
		close(ch)
	}
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Write the request under the write lock so concurrent call()s don't
	// interleave on stdin.
	t.writeMu.Lock()
	t.nextID++
	id := t.nextID
	if err := t.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		t.writeMu.Unlock()
		return nil, fmt.Errorf("plugin %q: write %s: %w", t.name, method, err)
	}

	// Register the response channel BEFORE releasing the write lock so the
	// reader goroutine can't see a response for this id before we're ready
	// to receive it.
	ch := make(chan pendingResult, 1)
	t.pendMu.Lock()
	t.pending[id] = ch
	t.pendMu.Unlock()
	t.writeMu.Unlock()

	// Wait for the response or context cancellation. Context cancellation
	// is responsive here — we're NOT holding the write lock.
	select {
	case pr := <-ch:
		return pr.result, pr.err
	case <-ctx.Done():
		// Clean up the pending entry so the reader goroutine doesn't
		// leak a channel write.
		t.pendMu.Lock()
		delete(t.pending, id)
		t.pendMu.Unlock()
		return nil, ctx.Err()
	}
}

func (t *stdioTransport) notify(_ context.Context, method string, params any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdioTransport) setNotifyHandler(fn func(method string, params json.RawMessage)) {
	t.onNotify = fn
}

// close kills the subprocess (which closes its stdout pipe and unblocks
// the reader goroutine), then waits for the reader to drain pending callers
// and exit. Unlike the old single-mutex design, close() never needs the
// write lock — it kills the process directly, so a hung call() no longer
// deadlocks the teardown path.
func (t *stdioTransport) close() {
	t.closeOnce.Do(func() {
		// Kill the subprocess first — this closes stdout, unblocking the
		// reader goroutine. Any in-flight call() is already past the write
		// lock (writeMu was released after registering the channel), so it
		// blocks on the channel read in the select{} — the reader goroutine
		// will deliver an error through the channel as soon as it sees EOF.
		if t.cmd != nil && t.cmd.Process != nil {
			if err := t.cmd.Process.Kill(); err != nil {
				fmt.Fprintf(os.Stderr, "plugin stdio: kill: %v\n", err)
			}
			if err := t.cmd.Wait(); err != nil {
				fmt.Fprintf(os.Stderr, "plugin stdio: wait: %v\n", err)
			}
		}
		if t.stdin != nil {
			if err := t.stdin.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "plugin stdio: close stdin: %v\n", err)
			}
		}
		// Wait for the reader goroutine to drain pending callers.
		t.readerWG.Wait()
	})
}
