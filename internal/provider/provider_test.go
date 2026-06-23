package provider

import (
	"testing"
)

func TestPricingCost(t *testing.T) {
	p := &Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}
	u := &Usage{CacheHitTokens: 500_000, CacheMissTokens: 100_000, CompletionTokens: 50_000}
	cost := p.Cost(u)
	// (500000*0.02 + 100000*1 + 50000*2) / 1e6
	// = (10000 + 100000 + 100000) / 1e6 = 210000/1e6 = 0.21
	if cost != 0.21 {
		t.Errorf("Cost = %v, want 0.21", cost)
	}
}

func TestPricingCostNilPricing(t *testing.T) {
	var p *Pricing
	cost := p.Cost(&Usage{
		CacheHitTokens:   100,
		CacheMissTokens:  100,
		CompletionTokens: 100,
	})
	if cost != 0 {
		t.Errorf("nil Pricing.Cost = %v, want 0", cost)
	}
}

func TestPricingCostNilUsage(t *testing.T) {
	p := &Pricing{CacheHit: 1, Input: 1, Output: 1}
	cost := p.Cost(nil)
	if cost != 0 {
		t.Errorf("nil Usage.Cost = %v, want 0", cost)
	}
}

func TestPricingSymbolDefault(t *testing.T) {
	var p *Pricing
	if s := p.Symbol(); s != "¥" {
		t.Errorf("nil Pricing.Symbol = %q, want ¥", s)
	}
	p = &Pricing{}
	if s := p.Symbol(); s != "¥" {
		t.Errorf("empty Currency.Symbol = %q, want ¥", s)
	}
}

func TestPricingSymbolCustom(t *testing.T) {
	p := &Pricing{Currency: "$"}
	if s := p.Symbol(); s != "$" {
		t.Errorf("Custom Symbol = %q, want $", s)
	}
}

func TestAuthErrorMessages(t *testing.T) {
	tests := []struct {
		name     string
		ae       AuthError
		contains []string
	}{
		{
			"with key env",
			AuthError{Provider: "deepseek", KeyEnv: "DEEPSEEK_API_KEY", Status: 401},
			[]string{"deepseek", "HTTP 401", "DEEPSEEK_API_KEY"},
		},
		{
			"without key env",
			AuthError{Provider: "mimo", Status: 403},
			[]string{"mimo", "HTTP 403", "the API key"},
		},
	}
	for _, tt := range tests {
		msg := tt.ae.Error()
		for _, c := range tt.contains {
			if !contains(msg, c) {
				t.Errorf("%s: Error() missing %q in %q", tt.name, c, msg)
			}
		}
	}
}

func TestRegisterAndNew(t *testing.T) {
	kind := "testkind"
	called := false
	Register(kind, func(cfg Config) (Provider, error) {
		called = true
		if cfg.Name != "my-provider" {
			t.Errorf("Name = %q", cfg.Name)
		}
		return nil, nil
	})

	p, err := New(kind, Config{Name: "my-provider"})
	if err != nil {
		t.Fatal("New:", err)
	}
	if p != nil {
		t.Error("factory returned nil Provider, should be nil")
	}
	if !called {
		t.Error("factory was not called")
	}
}

func TestNewUnknownKind(t *testing.T) {
	_, err := New("nonexistent", Config{})
	if err == nil {
		t.Fatal("New(unknown) should error")
	}
}

func TestKindsRegistered(t *testing.T) {
	ks := Kinds()
	for _, k := range ks {
		if k == "" {
			t.Error("Kinds should not contain empty strings")
		}
	}
	// "openai" is registered by init() in provider/openai
}

func TestRegisterDuplicateSkipped(t *testing.T) {
	// Duplicate registration now logs a warning and skips (no panic).
	kind := "testdup"
	Register(kind, func(Config) (Provider, error) { return nil, nil })
	Register(kind, func(Config) (Provider, error) { return nil, nil })
	// Verify the original factory is still registered (first wins).
	_, err := New(kind, Config{})
	if err != nil {
		t.Errorf("original registration should still work, got: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
