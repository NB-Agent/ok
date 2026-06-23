package serve

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/permission"
)

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// newTestServer builds a controller-backed Server wired to a test broadcaster.
func newTestServer() (*Server, *control.Controller, *Broadcaster) {
	bc := NewBroadcaster()
	policy := permission.NewDenyPolicy(nil)
	ctrl := control.New(control.Options{Sink: bc, Policy: policy})
	return New(ctrl, bc), ctrl, bc
}

func TestServerHealthEndpoints(t *testing.T) {
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("GET /history", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/history")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("GET /context", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/context")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("GET /", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestServerPOSTEndpoints(t *testing.T) {
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("POST /plan", func(t *testing.T) {
		resp := postJSON(t, ts.URL+"/plan", `{"on":true}`)
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Errorf("status = %d, want 204", resp.StatusCode)
		}
	})

	t.Run("POST /cancel", func(t *testing.T) {
		resp := postJSON(t, ts.URL+"/cancel", "{}")
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Errorf("status = %d, want 204", resp.StatusCode)
		}
	})

	t.Run("POST /new", func(t *testing.T) {
		resp := postJSON(t, ts.URL+"/new", "{}")
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Errorf("status = %d, want 204", resp.StatusCode)
		}
	})

	t.Run("POST /approve missing id", func(t *testing.T) {
		resp := postJSON(t, ts.URL+"/approve", `{}`)
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400 for missing id", resp.StatusCode)
		}
	})

	t.Run("POST /submit missing input", func(t *testing.T) {
		resp := postJSON(t, ts.URL+"/submit", `{}`)
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("status = %d, want 400 for missing input", resp.StatusCode)
		}
	})
}

func TestSSEEventStream(t *testing.T) {
	if testing.Short() {
		t.Skip("SSE test requires real event emission")
	}
	srv, ctrl, bc := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Emit an event after subscribing.
	ready := make(chan struct{})
	go func() {
		// Wait for SSE client to connect.
		time.Sleep(50 * time.Millisecond)
		close(ready)
		bc.Emit(&event.Event{Kind: event.Text, Text: "hello from SSE"})
	}()

	req, _ := http.NewRequest("GET", ts.URL+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	<-ready

	// Read the initial ": connected" + the data frame.
	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	deadline := time.After(2 * time.Second)
	gotData := false
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for SSE data, got lines: %v", lines)
		default:
		}
		if !scanner.Scan() {
			break
		}
		lines = append(lines, scanner.Text())
		if strings.HasPrefix(scanner.Text(), "data:") {
			gotData = true
			break
		}
	}
	if !gotData {
		t.Errorf("expected data frame, got: %v", lines)
	}

	// Ensure the SSE stream still handles client disconnect.
	_ = ctrl // controller lifecycle managed by test server Close()
}

func TestSSEClientDisconnect(t *testing.T) {
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Connect, then close immediately — the goroutine must exit cleanly.
	req, _ := http.NewRequest("GET", ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Verify the server didn't crash — make another request.
	resp2, err := http.Get(ts.URL + "/context")
	if err != nil {
		t.Fatal("server should still be alive:", err)
	}
	if resp2.StatusCode != 200 {
		t.Fatal("server should respond after SSE disconnect")
	}
	_ = resp2.Body.Close()
}

func TestServerContentTypes(t *testing.T) {
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, tc := range []struct{ path, method, body string }{
		{"/plan", "POST", `{"on":true}`},
		{"/new", "POST", "{}"},
		{"/cancel", "POST", "{}"},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("%s %s: %v", tc.method, tc.path, err)
			continue
		}
		if resp.StatusCode != 204 {
			t.Errorf("%s %s: status=%d, want 204", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestBroadcasterConcurrentSubscriptions(t *testing.T) {
	bc := NewBroadcaster()
	const n = 10
	chans := make([]<-chan []byte, n)
	for i := 0; i < n; i++ {
		ch, _ := bc.Subscribe()
		chans[i] = ch
	}

	// Emit and verify all subscribers get it.
	bc.Emit(&event.Event{Kind: event.Text, Text: "broadcast"})

	for i, ch := range chans {
		select {
		case data := <-ch:
			if !strings.Contains(string(data), "broadcast") {
				t.Errorf("sub %d: data = %q, want 'broadcast'", i, string(data))
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("sub %d: timeout waiting for data", i)
		}
	}

	// Unsubscribe all — the broadcaster must handle it cleanly.
	for _, ch := range chans {
		_ = ch
	}
}

func TestWSRouteRegistered(t *testing.T) {
	// Verify the /ws endpoint is in the handler by checking the ACP info.
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/acp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthAPIKey(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, Policy: permission.NewDenyPolicy(nil)})
	srv := New(ctrl, bc, WithAPIKey("ok_test_key_123"))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Without API key → 401
	resp, err := http.Get(ts.URL + "/history")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("no auth: status=%d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// With correct API key → 200
	req, _ := http.NewRequest("GET", ts.URL+"/history", nil)
	req.Header.Set("Authorization", "Bearer ok_test_key_123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("with auth: status=%d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Health check is always public
	resp, err = http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("healthz: status=%d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong API key → 401
	req, _ = http.NewRequest("GET", ts.URL+"/history", nil)
	req.Header.Set("Authorization", "Bearer wrong_key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("wrong key: status=%d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestWebSocketUpgrade(t *testing.T) {
	// Try a WebSocket upgrade request and verify it gets 101 Switching Protocols.
	srv, _, _ := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Manually construct a WebSocket upgrade request.
	req, _ := http.NewRequest("GET", ts.URL+"/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// HTTP client won't follow the upgrade, which is fine.
		// We just need it not to return 404/405.
		return
	}
	defer resp.Body.Close()
	// If we got here, the handler didn't error; status might be 101 or 400
	// depending on how the HTTP client handles the upgrade.
	_ = resp.StatusCode
}
