// System prompt component sizes (measured):
// - DefaultSystemPrompt:    ~1900 bytes (~475 tokens)
// - LanguagePolicy:        ~290 bytes  (~72 tokens)
// - Memory/skills/env:     ~1500 bytes (~375 tokens)
// - Tool schemas (36):     ~14723 bytes (~3680 tokens)
// --------------------------------------------------
// TOTAL per-turn prefix:   ~18413 bytes (~4602 tokens)
//
// Optimization targets:
// 1. Minify schemas (-30% = -4400 bytes / -1100 tokens) ← BIGGEST WIN
// 2. Shorten descriptions (-50% = -2400 bytes / -600 tokens)
// 3. Remove redundant tools from prompt during simple tasks
//
// Every turn saved = ~1800 fewer tokens in prefix.
// 20-turn session = 36,000 fewer tokens = ~$3.60 savings (DeepSeek Flash).
package builtin

// Auto-generated schema minification hint: schemas use compact JSON.
// All tool description and schema definitions below should be as short as
// possible while remaining unambiguous to the LLM.
