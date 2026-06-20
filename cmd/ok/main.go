// Command ok is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"

	"github.com/NB-Agent/ok/internal/cli"
	"github.com/NB-Agent/ok/internal/sandbox"

	// Blank imports wire compile-time built-ins into their registries.
	_ "github.com/NB-Agent/ok/internal/provider/anthropic"
	_ "github.com/NB-Agent/ok/internal/provider/openai"
	_ "github.com/NB-Agent/ok/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// --landlock-exec (Linux) and --sandbox-exec (Windows) are internal re-exec
	// flags set by the sandbox package. When present, apply the OS sandbox and
	// exec the real command rather than entering the normal CLI.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--landlock-exec":
			// Format: /proc/self/exe --landlock-exec --roots <dirs> --net <0|1> -- <cmd...>
			sandbox.LandlockExec(os.Args[2:])
			// unreachable
		case "--sandbox-exec":
			// Format: ok.exe --sandbox-exec --roots <dirs> --net <0|1> -- <cmd...>
			sandbox.SandboxExec(os.Args[2:])
			// unreachable
		default: // passthrough — not an internal flag; let cli.Run handle it
		}
	}
	os.Exit(cli.Run(os.Args[1:], version))
}
