// Package enterprise provides enterprise-grade features for OK:
// SSO, RBAC, audit exports, session persistence, air-gap mode, and metrics.
package enterprise

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// contextKey is a private type for context keys.
type contextKey string

const userContextKey contextKey = "user"

// ── SSO (OIDC) ──

// OIDCMiddleware validates Bearer tokens via OIDC JWKS.
func OIDCMiddleware(issuerURL, clientID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r.Header.Get("Authorization"))
			if token == "" {
				http.Error(w, "missing authorization header", http.StatusUnauthorized)
				return
			}
			claims, err := verifyToken(token, issuerURL, clientID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sso: token verification failed: %v\n", err)
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserClaims extracted from a verified OIDC token.
type UserClaims struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
	Roles  []string `json:"roles"`
}

func extractBearer(auth string) string {
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// ── JWT types ──

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtPayload struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Exp    float64  `json:"exp"`
	Iss    string   `json:"iss"` // issuer — must match issuerURL
	Aud    string   `json:"aud"` // audience — must include clientID
	Groups []string `json:"groups"`
	Roles  []string `json:"roles"`
}

// ── JWT verification ──

func verifyToken(token, issuerURL, clientID string) (*UserClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid JWT")
	}

	// Decode payload
	payloadJSON, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var payload jwtPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}

	// Check expiration
	if payload.Exp > 0 && time.Now().Unix() > int64(payload.Exp) {
		return nil, errors.New("token expired")
	}

	// Validate issuer
	if issuerURL != "" && payload.Iss != "" && payload.Iss != issuerURL {
		return nil, fmt.Errorf("invalid issuer: %q", payload.Iss)
	}

	// Validate audience (may be a single string or JSON array)
	if clientID != "" {
		if payload.Aud == "" {
			return nil, errors.New("token missing audience claim")
		}
		// aud can be a string or array; for now check string equality
		if payload.Aud != clientID {
			return nil, fmt.Errorf("invalid audience: %q", payload.Aud)
		}
	}

	// Verify signature
	if err := verifyJWTSignature(token, issuerURL); err != nil {
		return nil, err
	}

	return &UserClaims{
		Sub:    payload.Sub,
		Email:  payload.Email,
		Name:   payload.Name,
		Groups: payload.Groups,
		Roles:  payload.Roles,
	}, nil
}

func verifyJWTSignature(token, issuerURL string) error {
	parts := strings.Split(token, ".")

	// Decode header for algorithm
	headerJSON, err := decodeBase64URL(parts[0])
	if err != nil {
		return fmt.Errorf("decode header: %w", err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	if header.Alg == "" {
		return errors.New("missing algorithm")
	}
	if header.Alg == "none" {
		return errors.New("alg 'none' is not allowed — tokens must be signed")
	}
	// Only allow RSA asymmetric algorithms (RS256/RS384/RS512).
	// ES (ECDSA) and PS (RSASSA-PSS) are NOT supported because
	// jwkToPublicKey only handles RSA N/E fields. Reject symmetric
	// algorithms (HS256/HS384/HS512) to prevent algorithm confusion.
	alg := strings.ToUpper(header.Alg)
	if !strings.HasPrefix(alg, "RS") {
		return fmt.Errorf("unsupported algorithm %q — only RS* asymmetric algorithms are supported", header.Alg)
	}

	// Fetch JWKS
	jwks, err := fetchJWKS(issuerURL)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}

	// Find matching key and verify — when Kid is empty, we try every key
	// until one verifies the signature, rather than blindly matching the first.
	for _, k := range jwks.Keys {
		if header.Kid != "" && k.Kid != header.Kid {
			continue
		}
		pubKey, err := jwkToPublicKey(&k)
		if err != nil {
			continue // skip malformed keys
		}
		sig, err := decodeBase64URL(parts[2])
		if err != nil {
			continue
		}
		msg := []byte(parts[0] + "." + parts[1])
		hashed := sha256.Sum256(msg)
		if err := rsa.VerifyPKCS1v15(pubKey, cryptoHashSHA256, hashed[:], sig); err == nil {
			return nil // signature verified with this key
		}
	}
	return fmt.Errorf("signature verification failed: no matching JWK key or invalid signature")
}

const cryptoHashSHA256 = 4 // crypto.SHA256

func jwkToPublicKey(key *jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := decodeBase64URL(key.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := decodeBase64URL(key.E)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())
	return &rsa.PublicKey{N: n, E: e}, nil
}

// ── JWKS ──

type jwkSet struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Kid string `json:"kid"`
}

var (
	jwksCache     *jwkSet
	jwksCacheTime time.Time
	jwksMu        sync.Mutex
)

func fetchJWKS(issuerURL string) (*jwkSet, error) {
	jwksMu.Lock()
	defer jwksMu.Unlock()

	if jwksCache != nil && time.Since(jwksCacheTime) < time.Hour {
		return jwksCache, nil
	}

	jwksURL := strings.TrimRight(issuerURL, "/") + "/.well-known/jwks.json"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // cached JWKS fetch
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	if err != nil {
		if jwksCache != nil {
			fmt.Fprintf(os.Stderr, "sso: JWKS request creation failed, using stale cache: %v\n", err)
			return jwksCache, nil
		}
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if jwksCache != nil {
			fmt.Fprintf(os.Stderr, "sso: JWKS fetch failed, using stale cache: %v\n", err)
			return jwksCache, nil
		}
		return nil, err
	}
	defer log.Close("sso jwks response", resp.Body)

	var jwks jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		if jwksCache != nil {
			return jwksCache, nil
		}
		return nil, err
	}

	jwksCache = &jwks
	jwksCacheTime = time.Now()
	return &jwks, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// ── RBAC ──

// Role defines a set of permissions.
type Role struct {
	Name            string
	AllowAll        bool
	AllowReads      bool
	AllowWrites     bool
	RequireAudit    bool
	RequireApproval bool
}

// DefaultRoles maps role names to their definitions.
var DefaultRoles = map[string]Role{
	"admin":   {Name: "admin", AllowAll: true},
	"dev":     {Name: "dev", AllowReads: true, AllowWrites: true, RequireApproval: true, RequireAudit: true},
	"viewer":  {Name: "viewer", AllowReads: true},
	"auditor": {Name: "auditor", AllowReads: true, RequireAudit: true},
}

// Authorizer checks if a user has permission to perform an action.
type Authorizer struct {
	mu    sync.RWMutex
	roles map[string]Role
}

// NewAuthorizer creates an authorizer with the given role assignments.
func NewAuthorizer(assignments map[string]string) *Authorizer {
	a := &Authorizer{roles: make(map[string]Role)}
	for userID, roleName := range assignments {
		if role, ok := DefaultRoles[roleName]; ok {
			a.roles[userID] = role
		}
	}
	return a
}

// CanRead checks if a user can read.
func (a *Authorizer) CanRead(userID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	role, ok := a.roles[userID]
	return ok && (role.AllowAll || role.AllowReads)
}

// CanWrite checks if a user can write.
func (a *Authorizer) CanWrite(userID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	role, ok := a.roles[userID]
	return ok && (role.AllowAll || role.AllowWrites)
}

// ── PEM utilities ──

func ParseRSAPublicKey(pemData []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("no PEM data")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not an RSA public key")
	}
	return pub, nil
}

// ── Audit export ──

type AuditEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	UserID    string                 `json:"user_id"`
	Action    string                 `json:"action"`
	ToolName  string                 `json:"tool_name"`
	Subject   string                 `json:"subject"`
	Args      map[string]interface{} `json:"args,omitempty"`
	Result    string                 `json:"result"`
	ProofHash string                 `json:"proof_hash"`
}

type AuditExporter struct {
	entries []AuditEntry
	mu      sync.Mutex
}

func (e *AuditExporter) Add(entry AuditEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.entries = append(e.entries, entry)
}

func (e *AuditExporter) ExportJSON() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return json.MarshalIndent(e.entries, "", "  ")
}

func (e *AuditExporter) ExportSplunk() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	for _, entry := range e.entries {
		fmt.Fprintf(&b, "%s user=%s action=%s tool=%s subject=%s result=%s\n",
			entry.Timestamp.Format(time.RFC3339),
			entry.UserID, entry.Action, entry.ToolName,
			entry.Subject, entry.Result)
	}
	return b.String()
}
