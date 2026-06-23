package evolution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServeManifest_Empty(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	mux := http.NewServeMux()
	ServeECP(mux, e)

	req := httptest.NewRequest("GET", "/ecp/manifest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var m ECPManifest
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.SkillCount != 0 {
		t.Errorf("expected 0 skills, got %d", m.SkillCount)
	}
}

func TestServeSkills_Empty(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	mux := http.NewServeMux()
	ServeECP(mux, e)

	req := httptest.NewRequest("GET", "/ecp/skills", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var update ECPKnowledgeUpdate
	if err := json.NewDecoder(w.Body).Decode(&update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if len(update.Skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(update.Skills))
	}
}

func TestAcceptSkills_EmptyUpdate(t *testing.T) {
	e := New(nil, nil, t.TempDir())
	mux := http.NewServeMux()
	ServeECP(mux, e)

	body := `{"id":"test","peerInstance":"peer","skills":[]}`
	req := httptest.NewRequest("POST", "/ecp/skills", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result ECPMergeResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.NewSkills != 0 {
		t.Errorf("expected 0 new skills, got %d", result.NewSkills)
	}
}

func TestFederator_NoPeers(t *testing.T) {
	f := NewFederator(FederatorConfig{
		Peers:      nil,
		InstanceID: "test",
	})
	f.Start()
	f.SyncNow(t.Context())
}

func TestFederator_HasAllSkills(t *testing.T) {
	f := NewFederator(FederatorConfig{
		ExistingSkills: func() []string { return []string{"a", "b", "c"} },
	})
	if !f.hasAllSkills([]string{"a", "c"}) {
		t.Error("should have all skills")
	}
	if f.hasAllSkills([]string{"a", "d"}) {
		t.Error("should not have skill d")
	}
	f2 := NewFederator(FederatorConfig{})
	if f2.hasAllSkills([]string{"a"}) {
		t.Error("nil existingSkills should return false")
	}
}

func TestHTTPPeer_Interface(t *testing.T) {
	peer := NewHTTPPeer(5 * time.Second)
	if peer == nil {
		t.Fatal("expected non-nil peer")
	}
	_ = peer
}
