// Package browser — multi-backend browser automation engine.
// Backends: chrome (built-in), playwright (plugin).
// The builtin browser.go delegates here for extensibility.
package browser

import (
	"context"
	"sync"
)

// Page represents a browser page/tab.
type Page interface {
	Navigate(ctx context.Context, url string) error
	Screenshot(ctx context.Context) ([]byte, error)
	Text(ctx context.Context) (string, error)
	Click(ctx context.Context, selector string) error
	Type(ctx context.Context, selector, value string) error
	Eval(ctx context.Context, js string) (string, error)
	Close() error
}

// Browser opens and manages browser instances.
type Browser interface {
	// Name returns the backend name.
	Name() string
	// NewPage opens a new page/tab.
	NewPage(ctx context.Context) (Page, error)
	// Close shuts down the browser.
	Close() error
}

// Factory creates a Browser from a config string.
type Factory func(config string) (Browser, error)

// Registry holds all registered browser backends.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

var global = &Registry{factories: map[string]Factory{}}

// Register adds a backend factory. Called from init().
func Register(name string, f Factory) {
	global.mu.Lock()
	global.factories[name] = f
	global.mu.Unlock()
}

// Get returns a Browser by name, or nil.
func Get(name, config string) Browser {
	global.mu.RLock()
	fn, ok := global.factories[name]
	global.mu.RUnlock()
	if !ok {
		return nil
	}
	b, err := fn(config)
	if err != nil {
		return nil
	}
	return b
}

// List returns all registered backend names.
func List() []string {
	global.mu.RLock()
	defer global.mu.RUnlock()
	names := make([]string, 0, len(global.factories))
	for n := range global.factories {
		names = append(names, n)
	}
	return names
}
