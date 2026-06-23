// Package ollama registers a local-model provider that speaks the Ollama
// OpenAI-compatible API (http://localhost:11434/v1). Auto-detects a running
// Ollama instance and lists available models on first use.
package ollama

import (
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/provider/openai"
)

const DefaultBaseURL = "http://localhost:11434"

func init() {
	provider.Register("ollama", openai.New)
}
