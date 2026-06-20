package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/config"
)

// sessionCommand handles "ok session export <path>" and "ok session import <path>".
func sessionCommand(args []string) int {
	if len(args) == 0 {
		fmt.Println("Usage: ok session export <path>")
		fmt.Println("       ok session import <path>")
		fmt.Println("       ok session list")
		return 0
	}

	switch args[0] {
	case "export":
		return sessionExport(args[1:])
	case "import":
		return sessionImport(args[1:])
	case "list":
		return sessionList()
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n", args[0])
		return 2
	}
}

func sessionExport(args []string) int {
	sessionDir := config.SessionDir()
	sessions, err := agent.ListSessions(sessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot list sessions: %v\n", err)
		return 1
	}

	if len(sessions) == 0 {
		fmt.Println("No saved sessions to export.")
		return 0
	}

	// Print sessions list.
	fmt.Println("Available sessions:")
	for i, s := range sessions {
		preview := s.Preview
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  %2d. %s — %s\n", i+1, s.ModTime.Format(time.RFC3339), preview)
	}

	// If a path argument is given, export the most recent session.
	if len(args) > 0 {
		dst := args[0]
		src := sessions[0].Path

		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Cannot read session: %v\n", err)
			return 1
		}

		envelope := map[string]any{
			"version":     "1",
			"preview":     sessions[0].Preview,
			"turns":       sessions[0].Turns,
			"exported_at": time.Now().UTC().Format(time.RFC3339),
			"data":        string(data),
		}

		out, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Cannot marshal: %v\n", err)
			return 1
		}

		if err := os.WriteFile(dst, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Cannot write: %v\n", err)
			return 1
		}

		fmt.Printf("✅ Exported to %s\n", dst)
		return 0
	}

	fmt.Println("\nRun `ok session export <path>` to export the most recent session.")
	return 0
}

func sessionImport(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: ok session import <path>")
		return 2
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot read: %v\n", err)
		return 1
	}

	var envelope struct {
		Version    string `json:"version"`
		Preview    string `json:"preview"`
		ExportedAt string `json:"exported_at"`
		Data       string `json:"data"`
	}

	if err := json.Unmarshal(data, &envelope); err != nil {
		// Try loading as raw session data (back-compat).
		envelope.Data = string(data)
	}

	sessionDir := config.SessionDir()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot create session dir: %v\n", err)
		return 1
	}

	name := fmt.Sprintf("imported-%s.jsonl", time.Now().Format("20060102-150405"))
	dst := filepath.Join(sessionDir, name)

	if err := os.WriteFile(dst, []byte(envelope.Data), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot write: %v\n", err)
		return 1
	}

	fmt.Printf("✅ Imported to %s\n", dst)
	return 0
}

func sessionList() int {
	sessionDir := config.SessionDir()
	sessions, err := agent.ListSessions(sessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot list sessions: %v\n", err)
		return 1
	}

	if len(sessions) == 0 {
		fmt.Println("No saved sessions.")
		return 0
	}

	for i, s := range sessions {
		preview := s.Preview
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("%2d. %-50s %s (%d turns)\n", i+1, preview, s.ModTime.Format(time.RFC3339), s.Turns)
	}
	return 0
}
