package builtin

// ok-verify v1 removed in favor of okverify_v2.go which delegates to the
// v2 engine (internal/verification/v2/). The old v1 had 16 hand-written
// Go AST analyzers; v2 has 14 engine-level analyzers covering 6 languages,
// external tool adapters, and semantic/architecture scanners.
