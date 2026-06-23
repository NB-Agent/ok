package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPTransportUnreachable proves a connection error during initialize
// surfaces as a StartAll error, not a crash.
func TestHTTPTransportUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// An address nothing is listening on.
	_, _, err := StartAll(ctx, []Spec{{Name: "down", Type: "http", URL: "http://127.0.0.1:1"}})
	if err == nil {
		t.Fatal("unreachable server should error")
	}
	if !strings.Contains(err.Error(), "down") {
		t.Errorf("error should name the server, got %v", err)
	}
}

// TestHTTPTransportMalformedJSON proves a server returning non-JSON doesn't
// crash the client — the error surfaces through the initialize call.
func TestHTTPTransportMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not json at all {{{")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := StartAll(ctx, []Spec{{Name: "bad", Type: "http", URL: srv.URL}})
	if err == nil {
		t.Fatal("malformed JSON should cause initialize to fail")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should mention the server name: %v", err)
	}
}

// TestHTTPTransport500 proves an HTTP 500 response surfaces cleanly.
func TestHTTPTransport500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := StartAll(ctx, []Spec{{Name: "flaky", Type: "http", URL: srv.URL}})
	if err == nil {
		t.Fatal("HTTP 500 should cause initialize to fail")
	}
}

// TestHostReadResourceNonexistent proves ReadResource always errors for an
// unknown server name.
func TestHostReadResourceNonexistent(t *testing.T) {
	h := NewHost()
	_, err := h.ReadResource(context.Background(), "no-such-server", "any-uri")
	if err == nil {
		t.Fatal("ReadResource for unknown server should error")
	}
}

// TestHostAddDuplicate proves that adding two servers with the same name is
// rejected even when the first add succeeded.
func TestHostAddDuplicate(t *testing.T) {
	srv := mcpHTTPServer(t, false)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewHost()
	defer h.Close()

	spec := Spec{Name: "dup", Type: "http", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}}

	_, err := h.Add(ctx, spec)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}

	_, err = h.Add(ctx, spec)
	if err == nil {
		t.Fatal("duplicate Add should error")
	}
	if !strings.Contains(err.Error(), "already connected") {
		t.Errorf("error should say 'already connected', got %v", err)
	}
}

// TestHostRemoveNonexistent proves Remove returns false for a server not
// currently connected.
func TestHostRemoveNonexistent(t *testing.T) {
	h := NewHost()
	if _, found := h.Remove("no-one"); found {
		t.Error("Remove of nonexistent server should return found=false")
	}
}

// TestServerStatusEmpty proves Servers on an empty host returns nil/empty.
func TestServerStatusEmpty(t *testing.T) {
	h := NewHost()
	servers := h.Servers()
	// Servers on an empty host should return empty — may be nil or empty slice.
	if len(servers) != 0 {
		t.Errorf("empty host should return no servers, got %v", servers)
	}
}

// TestNewHostEmpty proves NewHost creates a Host with no servers and no
// prompts/resources.
func TestNewHostEmpty(t *testing.T) {
	h := NewHost()
	if len(h.Prompts()) != 0 {
		t.Error("new host should have no prompts")
	}
	if len(h.Resources()) != 0 {
		t.Error("new host should have no resources")
	}
	if len(h.ServerNames()) != 0 {
		t.Error("new host should have no server names")
	}
}

// TestInvalidTransportType proves an unrecognized transport type is rejected.
func TestInvalidTransportType(t *testing.T) {
	_, _, err := StartAll(context.Background(), []Spec{
		{Name: "bad", Type: "grpc", URL: "http://x"},
	})
	if err == nil {
		t.Fatal("unknown transport type should error")
	}
	if !strings.Contains(err.Error(), "unknown transport type") {
		t.Errorf("error should say 'unknown transport type', got %v", err)
	}
}

// TestStdioTransportNoCommand proves a stdio spec with empty Command is rejected.
func TestStdioTransportNoCommand(t *testing.T) {
	ctx := context.Background()
	_, _, err := StartAll(ctx, []Spec{{Name: "nocommand", Type: "stdio"}})
	if err == nil {
		t.Fatal("stdio spec with no command should error")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("error should say 'command is required', got %v", err)
	}
}
