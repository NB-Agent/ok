package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewSlackAdapter(t *testing.T) {
	a := newSlackAdapter("xoxb-test")
	if a.token != "xoxb-test" {
		t.Errorf("token = %q", a.token)
	}
	if a.baseURL != "https://slack.com/api" {
		t.Errorf("baseURL = %q", a.baseURL)
	}
	if a.client.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", a.client.Timeout)
	}
}

func TestSlack_PlatformName(t *testing.T) {
	a := newSlackAdapter("tok")
	if got := a.PlatformName(); got != "slack" {
		t.Errorf("PlatformName = %q, want %q", got, "slack")
	}
}

func TestVerifySlackSignature_Empty(t *testing.T) {
	if verifySlackSignature("secret", []byte("body"), "", "") {
		t.Error("expected false for empty sig/timestamp")
	}
}

func TestVerifySlackSignature_Valid(t *testing.T) {
	secret := "mysecret"
	body := []byte("hello")
	ts := fmt.Sprintf("%d", time.Now().Unix())

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySlackSignature(secret, body, sig, ts) {
		t.Error("expected valid signature to pass")
	}
}

func TestVerifySlackSignature_WrongSecret(t *testing.T) {
	secret := "mysecret"
	body := []byte("hello")
	ts := fmt.Sprintf("%d", time.Now().Unix())

	mac := hmac.New(sha256.New, []byte("wrong-secret"))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	if verifySlackSignature(secret, body, sig, ts) {
		t.Error("expected wrong secret to fail")
	}
}

func TestVerifySlackSignature_OldTimestamp(t *testing.T) {
	secret := "mysecret"
	body := []byte("hello")
	ts := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	if verifySlackSignature(secret, body, sig, ts) {
		t.Error("expected old timestamp to fail")
	}
}

func TestSlack_URLVerification(t *testing.T) {
	b := newBot("token", "secret", "ok", "", ".")
	body := map[string]string{
		"type":      "url_verification",
		"challenge": "abc123",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(jsonBody)))
	req.Header.Set("X-Slack-Signature", computeSignature("secret", jsonBody))
	req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "abc123" {
		t.Errorf("body = %q, want %q", body, "abc123")
	}
}

func TestSlack_Healthz(t *testing.T) {
	b := newBot("tok", "secret", "ok", "", ".")
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func computeSignature(secret string, body []byte) string {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}
