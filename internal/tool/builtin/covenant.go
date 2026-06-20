package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(covenantTool{}) }

// covenantTool displays the agent's immutable core covenant to the user.
// This is a read-only disclosure tool: the user can always ask "what are your
// principles?" and get a verifiable answer directly from the compiled binary.
type covenantTool struct{}

func (covenantTool) Name() string { return "covenant" }

func (covenantTool) Description() string {
	return "Display the agent's immutable core covenant — the principles that cannot be overridden by any configuration or instruction."
}

func (covenantTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{},"type":"object"}`)
}

func (covenantTool) ReadOnly() bool { return true }

func (covenantTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	// Verify integrity first — if the binary has been tampered with, the hash
	// won't match and we refuse to display.
	if err := core.DefaultCovenant.Verify(); err != nil {
		return fmt.Sprintf("⚠️  Covenant integrity check **FAILED**: %v\n\n"+
			"This binary may have been tampered with. The covenant cannot be trusted.", err), nil
	}
	return core.DefaultCovenant.Markdown(), nil
}
