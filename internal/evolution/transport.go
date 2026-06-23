// Package evolution — ECP/1.1: Federation transport layer.
//
// ECP/1.0 defined types and serialization. ECP/1.1 adds the ability for
// agent instances to discover each other, exchange knowledge updates, and
// merge learned skills — turning single-device evolution into a federated
// learning network.
//
// This file implements:
//
//   - ECPTransport: transport-agnostic interface for push/pull
//   - HTTPPeer: HTTP-based peer client (the reference implementation)
//   - Federator: orchestrates peer sync on a schedule
package evolution

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// ─── Transport interface ─────────────────────────────────────────────────

// ECPTransport abstracts how knowledge updates move between agent instances.
// Implementations can use HTTP, gRPC, WebSocket, or even file-based sync.
type ECPTransport interface {
	// Push sends a knowledge update to a peer. The peer may accept, reject,
	// or merge the update at its discretion. Returns the peer's response
	// describing what was done.
	Push(ctx context.Context, peerURL string, update ECPKnowledgeUpdate) (ECPMergeResult, error)

	// Pull fetches a peer's knowledge manifest and (optionally) the full
	// update. When manifestOnly is true, only the lightweight manifest is
	// returned — useful for discovery. Otherwise, the full update is fetched.
	Pull(ctx context.Context, peerURL string) (ECPKnowledgeUpdate, error)

	// PullManifest fetches only the lightweight manifest from a peer.
	// Used during discovery to decide whether a full pull is worthwhile.
	PullManifest(ctx context.Context, peerURL string) (ECPManifest, error)
}

// ─── HTTP transport ──────────────────────────────────────────────────────

// HTTPPeer implements ECPTransport over plain HTTP. It is the reference
// transport for ECP/1.1.
//
// Endpoints (on the peer server):
//
//	GET  /ecp/manifest           → ECPManifest
//	GET  /ecp/skills             → ECPKnowledgeUpdate
//	POST /ecp/skills             → accept ECPKnowledgeUpdate, return ECPMergeResult
//
// When SharedSecret is non-empty, requests include an X-ECP-HMAC header
// computed as HMAC-SHA256 of the request body (or URL path for GET requests)
// so the peer can authenticate the caller.
type HTTPPeer struct {
	client       *http.Client
	timeout      time.Duration
	SharedSecret string
}

// NewHTTPPeer creates an HTTP transport with the given timeout.
func NewHTTPPeer(timeout time.Duration) *HTTPPeer {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPPeer{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// sign computes HMAC-SHA256 of the payload and returns a hex string.
func (h *HTTPPeer) sign(payload []byte) string {
	if h.SharedSecret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(h.SharedSecret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// setHMACHeader sets the X-ECP-HMAC header on the request if a secret is configured.
func (h *HTTPPeer) setHMACHeader(req *http.Request, payload []byte) {
	if sig := h.sign(payload); sig != "" {
		req.Header.Set("X-ECP-HMAC", sig)
	}
}

func (h *HTTPPeer) Push(ctx context.Context, peerURL string, update ECPKnowledgeUpdate) (ECPMergeResult, error) {
	body, err := update.Marshal()
	if err != nil {
		return ECPMergeResult{}, fmt.Errorf("ecp push: marshal: %w", err)
	}

	url := peerURL + "/ecp/skills"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ECPMergeResult{}, fmt.Errorf("ecp push: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	h.setHMACHeader(req, body)

	resp, err := h.client.Do(req)
	if err != nil {
		return ECPMergeResult{}, fmt.Errorf("ecp push: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ECPMergeResult{}, fmt.Errorf("ecp push: %s: %s", resp.Status, string(msg))
	}

	var result ECPMergeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ECPMergeResult{}, fmt.Errorf("ecp push: decode: %w", err)
	}
	return result, nil
}

func (h *HTTPPeer) Pull(ctx context.Context, peerURL string) (ECPKnowledgeUpdate, error) {
	url := peerURL + "/ecp/skills"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ECPKnowledgeUpdate{}, fmt.Errorf("ecp pull: request: %w", err)
	}
	h.setHMACHeader(req, []byte(url))

	resp, err := h.client.Do(req)
	if err != nil {
		return ECPKnowledgeUpdate{}, fmt.Errorf("ecp pull: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ECPKnowledgeUpdate{}, fmt.Errorf("ecp pull: %s: %s", resp.Status, string(msg))
	}

	var update ECPKnowledgeUpdate
	if err := json.NewDecoder(resp.Body).Decode(&update); err != nil {
		return ECPKnowledgeUpdate{}, fmt.Errorf("ecp pull: decode: %w", err)
	}
	return update, nil
}

func (h *HTTPPeer) PullManifest(ctx context.Context, peerURL string) (ECPManifest, error) {
	url := peerURL + "/ecp/manifest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ECPManifest{}, fmt.Errorf("ecp manifest: request: %w", err)
	}
	h.setHMACHeader(req, []byte(url))

	resp, err := h.client.Do(req)
	if err != nil {
		return ECPManifest{}, fmt.Errorf("ecp manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ECPManifest{}, fmt.Errorf("ecp manifest: %s: %s", resp.Status, string(msg))
	}

	var m ECPManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return ECPManifest{}, fmt.Errorf("ecp manifest: decode: %w", err)
	}
	return m, nil
}

// ─── Federation ──────────────────────────────────────────────────────────

// Federator orchestrates knowledge exchange with a set of peer instances.
// It periodically pulls from configured peers, merges their knowledge,
// and makes the local skill set available for others to pull.
//
// The zero value is safe — Start is a no-op when no peers are configured.
type Federator struct {
	transport      ECPTransport
	peers          []string        // peer URLs to sync with
	interval       time.Duration   // how often to sync
	instanceID     string          // this instance's identifier
	skillStore     SkillInstaller  // where to install merged skills
	existingSkills func() []string // returns names of locally installed skills

	mu      sync.Mutex
	running bool
	stop    chan struct{}
}

// SkillInstaller abstracts skill installation so the federator doesn't
// depend on the concrete skill.Store.
type SkillInstaller interface {
	Install(name, scope, body string) error
	List() []SkillInfo
}

// SkillInfo is a minimal skill descriptor for federation.
type SkillInfo struct {
	Name        string
	Description string
}

// FederatorConfig configures the federation loop.
type FederatorConfig struct {
	Transport      ECPTransport
	Peers          []string
	Interval       time.Duration // sync interval; default 1h
	InstanceID     string
	SkillStore     SkillInstaller
	ExistingSkills func() []string
}

// NewFederator creates a federator. Pass nil transport to use the default
// HTTP transport with 30s timeout.
func NewFederator(cfg FederatorConfig) *Federator {
	if cfg.Interval <= 0 {
		cfg.Interval = 1 * time.Hour
	}
	transport := cfg.Transport
	if transport == nil {
		transport = NewHTTPPeer(30 * time.Second)
	}
	return &Federator{
		transport:      transport,
		peers:          cfg.Peers,
		interval:       cfg.Interval,
		instanceID:     cfg.InstanceID,
		skillStore:     cfg.SkillStore,
		existingSkills: cfg.ExistingSkills,
		stop:           make(chan struct{}),
	}
}

// Start begins periodic federation. Runs the first sync immediately, then
// every interval. Call Stop to shut down.
func (f *Federator) Start() {
	if len(f.peers) == 0 || f.skillStore == nil {
		return
	}

	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return
	}
	f.running = true
	f.mu.Unlock()

	go f.loop()
}

// Stop shuts down the federation loop. Safe to call multiple times.
func (f *Federator) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		return
	}
	f.running = false
	close(f.stop)
}

// SyncNow triggers an immediate sync with all configured peers.
// Returns the aggregate merge result.
func (f *Federator) SyncNow(ctx context.Context) ECPMergeResult {
	var total ECPMergeResult
	for _, peer := range f.peers {
		result := f.syncPeer(ctx, peer)
		total.NewSkills += result.NewSkills
		total.UpdatedSkills += result.UpdatedSkills
		total.RejectedSkills += result.RejectedSkills
		total.Conflicts = append(total.Conflicts, result.Conflicts...)
	}
	return total
}

func (f *Federator) loop() {
	// Run first sync immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	result := f.SyncNow(ctx)
	if result.NewSkills > 0 || result.UpdatedSkills > 0 {
		log.Printf("ecp: initial sync — %d new, %d updated, %d rejected",
			result.NewSkills, result.UpdatedSkills, result.RejectedSkills)
	}
	cancel()

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			result := f.SyncNow(ctx)
			if result.NewSkills > 0 || result.UpdatedSkills > 0 {
				log.Printf("ecp: periodic sync — %d new, %d updated, %d rejected",
					result.NewSkills, result.UpdatedSkills, result.RejectedSkills)
			}
			cancel()
		}
	}
}

func (f *Federator) syncPeer(ctx context.Context, peerURL string) ECPMergeResult {
	// 1. Fetch manifest first to check if pull is worthwhile.
	manifest, err := f.transport.PullManifest(ctx, peerURL)
	if err != nil {
		log.Printf("ecp: manifest fetch from %s: %v", peerURL, err)
		return ECPMergeResult{}
	}

	// 2. Skip if the peer has nothing new since last sync.
	if f.hasAllSkills(manifest.SkillNames) {
		return ECPMergeResult{}
	}

	// 3. Pull full update.
	update, err := f.transport.Pull(ctx, peerURL)
	if err != nil {
		log.Printf("ecp: pull from %s: %v", peerURL, err)
		return ECPMergeResult{}
	}

	// 4. Merge with local policy.
	existing := make(map[string]bool)
	for _, name := range f.existingSkills() {
		existing[name] = true
	}

	installFn := func(p ECPSkillPacket) error {
		if f.skillStore == nil {
			return fmt.Errorf("no skill store configured")
		}
		return f.skillStore.Install(p.SkillName, "project", p.SkillBody)
	}

	return MergeKnowledge(update, existing, DefaultAcceptPolicy, installFn)
}

func (f *Federator) hasAllSkills(names []string) bool {
	if f.existingSkills == nil || len(names) == 0 {
		return false
	}
	local := make(map[string]bool)
	for _, name := range f.existingSkills() {
		local[name] = true
	}
	for _, name := range names {
		if !local[name] {
			return false
		}
	}
	return true
}

// ─── Server-side handlers ────────────────────────────────────────────────

// ServeECP registers ECP endpoints on an HTTP mux. The evolution engine
// provides the knowledge to serve; the skill store receives pushed skills.
//
// Usage:
//
//	mux := http.NewServeMux()
//	ecp.ServeECP(mux, engine, skillStore)
//
// Deprecated: use ServeECPWithSecret to enable peer authentication.
func ServeECP(mux *http.ServeMux, eng *Engine) {
	serveECP(mux, eng, "")
}

// ServeECPWithSecret registers ECP endpoints on an HTTP mux with peer
// authentication via HMAC shared secret. When sharedSecret is empty, HMAC
// verification is disabled (open federation).
func ServeECPWithSecret(mux *http.ServeMux, eng *Engine, sharedSecret string) {
	serveECP(mux, eng, sharedSecret)
}

func serveECP(mux *http.ServeMux, eng *Engine, sharedSecret string) {
	if eng == nil {
		return
	}
	h := &ecpHandler{eng: eng, sharedSecret: sharedSecret}
	mux.HandleFunc("GET /ecp/manifest", h.serveManifest)
	mux.HandleFunc("GET /ecp/skills", h.serveSkills)
	mux.HandleFunc("POST /ecp/skills", h.acceptSkills)
}

type ecpHandler struct {
	eng          *Engine
	sharedSecret string
}

// verifyHMAC checks the X-ECP-HMAC header against the request body (POST) or URL (GET).
// Returns true if the secret is empty (auth disabled) or the HMAC matches.
func (h *ecpHandler) verifyHMAC(r *http.Request, payload []byte) bool {
	if h.sharedSecret == "" {
		return true // auth disabled
	}
	got := r.Header.Get("X-ECP-HMAC")
	if got == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.sharedSecret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(expected))
}

// serveManifest returns a lightweight manifest of available skills.
func (h *ecpHandler) serveManifest(w http.ResponseWriter, r *http.Request) {
	if !h.verifyHMAC(r, []byte(r.URL.Path)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid HMAC"})
		return
	}
	if h.eng == nil || h.eng.skillStor == nil {
		writeJSON(w, http.StatusOK, ECPManifest{SkillCount: 0})
		return
	}

	skills := h.eng.skillStor.List()
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}

	manifest := ECPManifest{
		Instance:    "ok-agent",
		Version:     "1.0",
		GeneratedAt: time.Now().UTC(),
		SkillCount:  len(skills),
		SkillNames:  names,
		LastUpdate:  time.Now().UTC(),
	}
	writeJSON(w, http.StatusOK, manifest)
}

// serveSkills returns the full knowledge update — all locally installed
// skills packaged as ECP packets.
func (h *ecpHandler) serveSkills(w http.ResponseWriter, r *http.Request) {
	if !h.verifyHMAC(r, []byte(r.URL.Path)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid HMAC"})
		return
	}
	if h.eng == nil || h.eng.skillStor == nil {
		writeJSON(w, http.StatusOK, ECPKnowledgeUpdate{Skills: nil})
		return
	}

	skills := h.eng.skillStor.List()
	packets := make([]ECPSkillPacket, 0, len(skills))
	for _, s := range skills {
		p := NewECPSkillPacket(
			"ok-agent", "", "unknown", "5.0.0",
			s.Name, s.Description, s.Body,
			nil, 0.8,
		)
		packets = append(packets, p)
	}

	update := ECPKnowledgeUpdate{
		ID:           fmt.Sprintf("ok-%d", time.Now().Unix()),
		PeerInstance: "ok-agent",
		GeneratedAt:  time.Now().UTC(),
		Skills:       packets,
		Stats: ECPPeerStats{
			TotalSkills: len(skills),
		},
	}
	writeJSON(w, http.StatusOK, update)
}

// acceptSkills receives a knowledge update from a peer and merges it.
func (h *ecpHandler) acceptSkills(w http.ResponseWriter, r *http.Request) {
	// Read body for both HMAC verification and JSON parsing.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot read body"})
		return
	}
	if !h.verifyHMAC(r, bodyBytes) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid HMAC"})
		return
	}
	if h.eng == nil || h.eng.skillStor == nil {
		writeJSON(w, http.StatusOK, ECPMergeResult{})
		return
	}

	var update ECPKnowledgeUpdate
	if err := json.Unmarshal(bodyBytes, &update); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	existing := make(map[string]bool)
	for _, s := range h.eng.skillStor.List() {
		existing[s.Name] = true
	}

	installFn := func(p ECPSkillPacket) error {
		_, err := h.eng.skillStor.CreateWithContent(p.SkillName, "project", p.SkillBody)
		return err
	}

	result := MergeKnowledge(update, existing, DefaultAcceptPolicy, installFn)
	writeJSON(w, http.StatusOK, result)

	if result.NewSkills > 0 {
		log.Printf("ecp: accepted %d new skills from peer push", result.NewSkills)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
