//go:build !treesitter

package agent

// extractStructuralLinesTS returns nil when tree-sitter is not available
// (no treesitter build tag). The caller falls back to the heuristic
// isStructuralLine-based extraction.
func extractStructuralLinesTS(lines []string) []string {
	return nil
}
