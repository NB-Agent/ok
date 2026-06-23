// Package i18n provides runtime on-demand translation via the configured LLM
// provider. This is the second link in the translation chain — it catches any
// language that doesn't have a compiled catalog.
package i18n

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// LiveResolver translates UI strings at runtime using the configured LLM
// provider. It is the fallback for languages without a compiled catalog.
// Translations are cached so each unique (lang, key) pair is translated once.
type LiveResolver struct {
	provider LiveProvider
	lang     string // target language tag

	mu    sync.RWMutex
	cache map[string]string // "lang:fieldname" → translated string
	miss  atomic.Uint64     // translation cache misses
	hit   atomic.Uint64     // translation cache hits
}

// LiveProvider is the interface LiveResolver uses to translate text.
// The agent's LLM provider satisfies this interface.
type LiveProvider interface {
	// Translate asks the LLM to translate the given English text to lang.
	// Returns the translated text.
	Translate(text, lang string) (string, error)
}

// NewLiveResolver creates a resolver that translates into the given language
// using the provided LLM backend.
func NewLiveResolver(provider LiveProvider, lang string) *LiveResolver {
	return &LiveResolver{
		provider: provider,
		lang:     lang,
		cache:    make(map[string]string),
	}
}

// T resolves a key by translating the English value at runtime.
// Results are cached so repeated lookups are zero-latency.
func (l *LiveResolver) T(key string, args ...any) string {
	eng := EnglishField(key)
	if eng == "" {
		return ""
	}

	cacheKey := l.lang + ":" + key

	l.mu.RLock()
	val, ok := l.cache[cacheKey]
	l.mu.RUnlock()

	if ok {
		l.hit.Add(1)
		if len(args) > 0 {
			return fmt.Sprintf(val, args...)
		}
		return val
	}

	l.miss.Add(1)

	// Translate
	translated, err := l.provider.Translate(eng, l.lang)
	if err != nil || translated == "" {
		// Fall back to English
		if len(args) > 0 {
			return fmt.Sprintf(eng, args...)
		}
		return eng
	}

	l.mu.Lock()
	l.cache[cacheKey] = translated
	l.mu.Unlock()

	if len(args) > 0 {
		return fmt.Sprintf(translated, args...)
	}
	return translated
}

// Stats returns cache hit/miss counts for monitoring.
func (l *LiveResolver) Stats() (hit, miss uint64) {
	return l.hit.Load(), l.miss.Load()
}

// CacheSize returns the number of cached translations.
func (l *LiveResolver) CacheSize() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.cache)
}

// ResetCache clears all cached translations (e.g. after language switch).
func (l *LiveResolver) ResetCache() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = make(map[string]string)
	l.hit.Store(0)
	l.miss.Store(0)
}

// Ensure LiveResolver satisfies TranslationResolver.
var _ TranslationResolver = (*LiveResolver)(nil)
