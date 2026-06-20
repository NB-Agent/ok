package voice

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNewEngine(t *testing.T) {
	e := NewEngine("en")
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.lang != "en" {
		t.Errorf("expected lang=en, got %s", e.lang)
	}
}

func TestSetLanguage(t *testing.T) {
	e := NewEngine("en")
	e.SetLanguage("zh")
	if e.lang != "zh" {
		t.Errorf("expected lang=zh, got %s", e.lang)
	}
}

func TestModelDir(t *testing.T) {
	dir := ModelDir()
	if dir == "" {
		t.Error("ModelDir should not be empty")
	}
}

func TestWriteWAV(t *testing.T) {
	samples := []int16{100, 200, 300}
	var buf bytes.Buffer
	if err := WriteWAV(&buf, samples, 22050); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("WAV output should not be empty")
	}
	// Check RIFF header
	header := buf.Bytes()[:44]
	if string(header[0:4]) != "RIFF" {
		t.Errorf("missing RIFF header: %q", header[0:4])
	}
	if string(header[8:12]) != "WAVE" {
		t.Errorf("missing WAVE: %q", header[8:12])
	}
}

func TestDetectOS(t *testing.T) {
	os := detectOS()
	if os == "" {
		t.Error("detectOS should not return empty")
	}
}

func TestVoiceToolInterface(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	if v.Name() != "voice" {
		t.Errorf("got %q, want voice", v.Name())
	}
	if v.ReadOnly() {
		t.Error("voice tool should not be read-only")
	}
	if v.Description() == "" {
		t.Error("voice tool should have a description")
	}
	if len(v.Schema()) == 0 {
		t.Error("voice tool should have a schema")
	}
}

func TestWriteWAVBinary(t *testing.T) {
	samples := []int16{0, 1, 0, -1}
	var buf bytes.Buffer
	if err := WriteWAV(&buf, samples, 44100); err != nil {
		t.Fatal(err)
	}
	// WriteWAV should produce a valid WAV with header + PCM data
	expectedSize := 44 + len(samples)*2
	if buf.Len() != expectedSize {
		t.Errorf("expected %d bytes, got %d", expectedSize, buf.Len())
	}
}

func TestWriteWAVEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAV(&buf, nil, 8000); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 44 {
		t.Error("empty WAV should still have a header")
	}
}

// Verify binary.Write integration
func TestBinaryWrite(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(12345))
	if buf.Len() != 4 {
		t.Errorf("expected 4 bytes, got %d", buf.Len())
	}
}

// ─── Tool.Execute tests ───────────────────────────────────────────────────

func TestToolExecute_NilTool(t *testing.T) {
	var v *Tool
	_, err := v.Execute(nil, nil)
	if err == nil || err.Error() != "voice engine not initialized" {
		t.Errorf("expected engine-not-initialized error, got %v", err)
	}
}

func TestToolExecute_NilEngine(t *testing.T) {
	v := &Tool{}
	_, err := v.Execute(nil, nil)
	if err == nil || err.Error() != "voice engine not initialized" {
		t.Errorf("expected engine-not-initialized error, got %v", err)
	}
}

func TestToolExecute_InvalidJSON(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	_, err := v.Execute(nil, []byte("{bad json}"))
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
	if err.Error() != "invalid args: invalid character 'b' looking for beginning of object key string" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolExecute_UnknownAction(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	_, err := v.Execute(nil, []byte(`{"action":"fly"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if err.Error() != "unknown action: fly" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolExecute_EmptyJSON(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	_, err := v.Execute(nil, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for empty JSON")
	}
	if err.Error() != "unknown action: " {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolExecute_SpeakEmptyText(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	_, err := v.Execute(nil, []byte(`{"action":"speak","text":""}`))
	if err == nil {
		t.Fatal("expected error for empty text")
	}
	if err.Error() != "text is required for speak action" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolExecute_SpeakMissingText(t *testing.T) {
	v := &Tool{Engine: NewEngine("en")}
	_, err := v.Execute(nil, []byte(`{"action":"speak"}`))
	if err == nil {
		t.Fatal("expected error for missing text")
	}
	if err.Error() != "text is required for speak action" {
		t.Errorf("unexpected error: %v", err)
	}
}
