package semantic

import (
	"testing"
)

func TestNewEngine_NonNil(t *testing.T) {
	e := NewEngine(".")
	if e == nil {
		t.Fatal("NewEngine() returned nil")
	}
	if e.embedder == nil {
		t.Error("Engine.embedder is nil")
	}
	if e.chunker == nil {
		t.Error("Engine.chunker is nil")
	}
	if e.index == nil {
		t.Error("Engine.index is nil")
	}
}

func TestNewEmbedder_NonNil(t *testing.T) {
	e := NewEmbedder("http://localhost:11434", "nomic-embed-text")
	if e == nil {
		t.Fatal("NewEmbedder() returned nil")
	}
}

func TestNewChunker_NonNil(t *testing.T) {
	c := NewChunker()
	if c == nil {
		t.Fatal("NewChunker() returned nil")
	}
}

func TestNewIndex_NonNil(t *testing.T) {
	idx := NewIndex(t.TempDir() + "/test-index.json")
	if idx == nil {
		t.Fatal("NewIndex() returned nil")
	}
}

func TestEngine_IndexPath(t *testing.T) {
	e := NewEngine("/tmp/test-project")
	if e.projectRoot != "/tmp/test-project" {
		t.Errorf("projectRoot = %q, want /tmp/test-project", e.projectRoot)
	}
}
