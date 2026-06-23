package boot

import (
	"strings"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/provider"
)

func NewProvider(e *config.ProviderEntry) (provider.Provider, error) {
	return provider.New(e.Kind, provider.Config{
		Name:      e.Name,
		BaseURL:   e.BaseURL,
		Model:     e.Model,
		APIKey:    e.APIKey(),
		APIKeyEnv: e.APIKeyEnv,
	})
}

func providerNames(cfg *config.Config) string {
	names := make([]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, "/")
}

type proofChainAdapter struct{ pc *core.ProofChain }

func (a *proofChainAdapter) AppendWithPath(atomID, proposition, evidence, parentID, path string) {
	a.pc.AppendWithPath(atomID, proposition, evidence, parentID, path)
}
