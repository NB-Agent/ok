// Package i18n provides translation resolution with a chain-of-responsibility
// pattern: compiled catalogs first, then runtime LLM translation, falling back
// to English. This lets OK reach every user in their own language.
package i18n

import (
	"fmt"
	"reflect"
)

// TranslationResolver resolves a translated UI string by field key.
// Each implementation returns "" to signal "not found, try the next resolver".
type TranslationResolver interface {
	// T returns the translated string for the given Messages field key,
	// formatted with optional args. Returns "" when the key is unknown.
	T(key string, args ...any) string
}

// ResolverChain combines multiple resolvers in order. The first resolver
// that returns a non-empty string wins. If all resolvers return "", the
// chain returns "" (the caller should fall back to English).
type ResolverChain struct {
	resolvers []TranslationResolver
}

// NewResolverChain builds a chain from the given resolvers.
func NewResolverChain(resolvers ...TranslationResolver) *ResolverChain {
	return &ResolverChain{resolvers: resolvers}
}

// T walks the resolver chain and returns the first non-empty result.
func (c *ResolverChain) T(key string, args ...any) string {
	for _, r := range c.resolvers {
		if s := r.T(key, args...); s != "" {
			return s
		}
	}
	return ""
}

// PrecompiledResolver serves strings from a compiled-in Messages catalog.
// It is the first link in the chain — zero latency, always available.
type PrecompiledResolver struct {
	catalog map[string]string // field name → translated string
}

// NewPrecompiledResolver wraps a compiled Messages catalog as a resolver.
func NewPrecompiledResolver(catalog *Messages) *PrecompiledResolver {
	return &PrecompiledResolver{catalog: buildStringIndex(catalog)}
}

// T looks up a string by field key. Returns "" if not found.
func (p *PrecompiledResolver) T(key string, args ...any) string {
	s, ok := p.catalog[key]
	if !ok || s == "" {
		return ""
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// buildStringIndex uses reflect to extract all string fields from a Messages
// struct into a map. Called once per catalog, then O(1) lookups at runtime.
func buildStringIndex(m *Messages) map[string]string {
	v := reflect.ValueOf(m).Elem()
	t := v.Type()
	out := make(map[string]string, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Type.Kind() == reflect.String {
			out[f.Name] = v.Field(i).String()
		}
	}
	return out
}

// EnglishResolver returns a PrecompiledResolver for the English catalog.
func EnglishResolver() *PrecompiledResolver {
	return NewPrecompiledResolver(&English)
}

// allResolvers stores compiled resolvers keyed by language tag.
// Populated in init() by each language file via registerResolver.
var allResolvers = map[string]*PrecompiledResolver{
	"en": EnglishResolver(),
}

// registerResolver registers a compiled catalog resolver for a language tag.
//
//nolint:unused
func registerResolver(tag string, catalog *Messages) {
	allResolvers[tag] = NewPrecompiledResolver(catalog)
	registerLanguage(tag, catalog)
}

// ResolveTag returns the compiled resolver for a language tag.
func ResolveTag(tag string) *PrecompiledResolver {
	return allResolvers[tag]
}

// CompiledTags returns all language tags with compiled catalogs.
func CompiledTags() []string {
	tags := make([]string, 0, len(allResolvers))
	for tag := range allResolvers {
		tags = append(tags, tag)
	}
	return tags
}

// EnglishField returns the English value for a field key.
// Used by gen.go and LiveResolver as source text for translation.
func EnglishField(key string) string {
	if r, ok := allResolvers["en"]; ok {
		return r.T(key)
	}
	return ""
}

// KeyExists checks if a key exists in the English catalog.
func KeyExists(key string) bool {
	_, ok := allResolvers["en"].catalog[key]
	return ok
}

// ── convenience access ──

// activeResolver is installed by SetResolverChain.
// Defaults to a compiled English catalog to avoid reflect-based fallback.
var activeResolver TranslationResolver = NewPrecompiledResolver(&English)

// SetResolverChain installs a resolver chain for future T() calls.
// When nil, T() falls back to old-style M.* field access via reflection.
func SetResolverChain(chain TranslationResolver) {
	activeResolver = chain
}

// T is the top-level translation function. It resolves key through the
// active resolver chain, which defaults to a compiled English catalog.
func T(key string, args ...any) string {
	return activeResolver.T(key, args...)
}

// TFunc returns a function that calls T with the given key.
func TFunc(key string) func(...any) string {
	return func(args ...any) string {
		return T(key, args...)
	}
}

// ── translation progress ──

// NeedsTranslation returns true when a field value still contains its English
// source text — used to detect fields that haven't been translated yet.
func NeedsTranslation(catalog *Messages, key string) bool {
	en := EnglishField(key)
	if en == "" {
		return false
	}
	idx := buildStringIndex(catalog)
	val, ok := idx[key]
	return !ok || val == "" || val == en
}

// TranslationProgress returns the fraction (0.0–1.0) of completed translations.
func TranslationProgress(catalog *Messages) float64 {
	en := buildStringIndex(&English)
	idx := buildStringIndex(catalog)
	if len(en) == 0 {
		return 1.0
	}
	done := 0
	for key := range en {
		val, ok := idx[key]
		if ok && val != "" && val != en[key] {
			done++
		}
	}
	return float64(done) / float64(len(en))
}
