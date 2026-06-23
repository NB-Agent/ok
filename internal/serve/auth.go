package serve

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// ─── API Key Authentication ───────────────────────────────────────────────
//
// Three authentication modes:
//
//  1. No API key set → localhost-only (default, backward-compatible)
//  2. API key set with --api-key → Bearer token in Authorization header
//     or ?token= query param (for WebSocket upgrade)
//  3. SSO configured → OIDC validation (via WithSSO, existing code)

// apiKeyAuthenticator validates API keys for HTTP and WebSocket requests.
type apiKeyAuthenticator struct {
	enabled  bool
	expected string
}

func newAPIKeyAuthenticator(key string) *apiKeyAuthenticator {
	return &apiKeyAuthenticator{
		enabled:  key != "",
		expected: key,
	}
}

// isLocalhost checks if the request originates from a loopback address.
func isLocalhost(r *http.Request) bool {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// extractBearer extracts a Bearer token from the Authorization header.
func extractBearer(authHeader string) string {
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}

// extractTokenFromRequest looks for a token in:
// 1. Authorization: Bearer <token> header
// 2. ?token=<token> query parameter
func extractTokenFromRequest(r *http.Request) string {
	if t := extractBearer(r.Header.Get("Authorization")); t != "" {
		return t
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}

// validate performs constant-time comparison of the given token against the
// expected key. Returns true if valid or if auth is disabled (localhost-only).
func (a *apiKeyAuthenticator) validate(r *http.Request) bool {
	if !a.enabled {
		return isLocalhost(r)
	}
	token := extractTokenFromRequest(r)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.expected)) == 1
}

// canUpgradeWS checks whether the WebSocket upgrade should proceed.
func (a *apiKeyAuthenticator) canUpgradeWS(r *http.Request) bool {
	return a.validate(r)
}

// middleware wraps a handler with authentication check.
func (a *apiKeyAuthenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check and ACP info are always public.
		if r.URL.Path == "/healthz" || r.URL.Path == "/acp" {
			next.ServeHTTP(w, r)
			return
		}
		if a.validate(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// ─── API Key Generation ───────────────────────────────────────────────────

// GenerateAPIKey creates a random API key with the "ok_" prefix.
// 32 hex characters = 128 bits of entropy.
func GenerateAPIKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		fmt.Fprintf(os.Stderr, "auth: failed to generate api key: %v\n", err)
		os.Exit(1)
	}
	return "ok_" + hex.EncodeToString(buf)
}
