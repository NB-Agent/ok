// Package boot wires the i18n translation resolver chain into the application.
//
// The resolver chain is:
//  1. PrecompiledResolver — compiled-in Messages catalogs (30+ languages)
//  2. LiveResolver — runtime LLM translation for any other language
//  3. English fallback — always works
package boot

import (
	"context"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/provider"
)

// translateProvider wraps a provider.Provider to satisfy i18n.LiveProvider.
// It translates text by calling the LLM with a simple translation prompt.
type translateProvider struct {
	prov provider.Provider
}

// Translate calls the wrapped provider to translate English text into the
// target language. Returns the translated text or an error.
func (t *translateProvider) Translate(text, lang string) (string, error) {
	if text == "" || lang == "" {
		return "", fmt.Errorf("empty text or language")
	}
	if lang == "en" {
		return text, nil // identity for English
	}

	prompt := fmt.Sprintf(
		`Translate the following text to %s. Return ONLY the translation, no explanation, no quotes, no prefix.

%s`,
		lang, text,
	)

	ch, err := t.prov.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: prompt},
		},
		Temperature: 0.1, // low temperature for deterministic translation
	})
	if err != nil {
		return "", fmt.Errorf("translate stream: %w", err)
	}

	var b strings.Builder
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkError:
			if chunk.Err != nil {
				return "", fmt.Errorf("translate: %w", chunk.Err)
			}
		case provider.ChunkText:
			b.WriteString(chunk.Text)
		}
	}

	result := strings.TrimSpace(b.String())
	if result == "" {
		return "", fmt.Errorf("empty translation result")
	}
	return result, nil
}

// WireI18n installs the translation resolver chain for the given language.
// The chain order: compiled catalog → LiveResolver → English fallback.
// This should be called once during boot, after the provider is created.
func WireI18n(prov provider.Provider, lang string) {
	// 1. Precompiled resolver for the target language (if available)
	var resolvers []i18n.TranslationResolver
	if r := i18n.ResolveTag(lang); r != nil {
		resolvers = append(resolvers, r)
	}

	// 2. Live resolver — catches all languages without compiled catalogs
	if prov != nil {
		live := i18n.NewLiveResolver(&translateProvider{prov: prov}, lang)
		resolvers = append(resolvers, live)
	}

	// 3. English fallback — always works
	resolvers = append(resolvers, i18n.EnglishResolver())

	chain := i18n.NewResolverChain(resolvers...)
	i18n.SetResolverChain(chain)
}
