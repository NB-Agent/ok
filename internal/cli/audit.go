package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/log"
)

// auditRun implements the "ok audit" subcommand: it displays the most recent 50
// audit records in a compact table format, or --json for the full JSON output.
func auditRun(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output full JSON")
	verifyFlag := fs.Bool("verify", false, "verify audit chain integrity")
	exportFlag := fs.Bool("export", false, "export compliance report")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctrl, err := setup(context.Background(), "", 0, false, nil) // CLI entry point — no parent context
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit: setup error:", err)
		return 1
	}
	defer log.CloseSimple("controller", ctrl)

	records := ctrl.AuditLog()
	if *verifyFlag {
		return auditVerify(records)
	}
	if *exportFlag {
		return auditExport(records)
	}
	if *jsonFlag {
		return auditJSON(records)
	}
	return auditTable(records)
}

// auditVerify validates the entire audit chain integrity.
func auditVerify(records []core.AuditRecord) int {
	if len(records) == 0 {
		fmt.Println("(no audit records to verify)")
		return 0
	}

	failed := 0
	for i, entry := range records {
		prevHash := ""
		if i > 0 {
			prevHash = records[i-1].Hash
		}
		h := sha256.New()
		fmt.Fprintf(h, "%d|%s|%s|%s|%v|%s",
			entry.Index, entry.Tool, entry.Args, entry.Result, entry.Allowed, prevHash)
		expected := hex.EncodeToString(h.Sum(nil))

		if entry.Hash != expected {
			fmt.Printf("  \u274c #%d (%s): hash MISMATCH\n", i, entry.Tool)
			failed++
		}
		if i > 0 && entry.PrevHash != records[i-1].Hash {
			fmt.Printf("  \u274c #%d (%s): prevHash link BROKEN\n", i, entry.Tool)
			failed++
		}
	}

	if failed == 0 {
		fmt.Printf("\u2705 Audit chain intact: %d entries verified\n", len(records))
		return 0
	}
	fmt.Printf("\u274c Audit chain CORRUPTED: %d/%d entries failed\n", failed, len(records))
	return 1
}

// auditExport generates a compliance report.
func auditExport(records []core.AuditRecord) int {
	if len(records) == 0 {
		fmt.Println("(no audit records to export)")
		return 0
	}

	report := struct {
		GeneratedAt  string `json:"generated_at"`
		TotalEntries int    `json:"total_entries"`
		TimeRange    struct {
			First string `json:"first"`
			Last  string `json:"last"`
		} `json:"time_range"`
		Summary struct {
			Allowed int `json:"allowed"`
			Blocked int `json:"blocked"`
		} `json:"summary"`
		ChainVerified bool               `json:"chain_verified"`
		Entries       []core.AuditRecord `json:"entries"`
	}{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		TotalEntries: len(records),
		Entries:      records,
	}

	if len(records) > 0 {
		report.TimeRange.First = records[0].Timestamp.Format(time.RFC3339)
		report.TimeRange.Last = records[len(records)-1].Timestamp.Format(time.RFC3339)
	}

	for _, r := range records {
		if r.Allowed {
			report.Summary.Allowed++
		} else {
			report.Summary.Blocked++
		}
	}

	ac := core.NewAuditChain()
	ac.Entries = records
	report.ChainVerified = ac.VerifyChain() == nil

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "audit: export error:", err)
		return 1
	}
	return 0
}

// auditTable prints the most recent 50 audit records in a readable table.
func auditTable(records []core.AuditRecord) int {
	if len(records) == 0 {
		fmt.Println("(no audit records yet)")
		return 0
	}

	// Show latest 50
	const maxDisplay = 50
	start := 0
	if len(records) > maxDisplay {
		start = len(records) - maxDisplay
	}

	for _, r := range records[start:] {
		// Timestamp in local time
		ts := r.Timestamp.Local().Format("2006-01-02 15:04:05")

		// Status indicator
		status := "OK"
		if !r.Allowed {
			status = "BLOCKED"
		}

		// Truncate result to first line for display
		resultLine := truncateResult(r.Result)
		// Truncate args to a reasonable display length
		argsDisplay := truncateArgs(r.Args)

		// Format: #1  2026-06-06 18:23:29  read_file  OK    internal/main.go
		line := fmt.Sprintf("  #%-4d %s  %-12s %-7s  %s",
			r.Index, ts, r.Tool, status, argsDisplay)
		if resultLine != "" && resultLine != argsDisplay {
			line += " → " + resultLine
		}
		fmt.Println(line)
	}

	if len(records) > maxDisplay {
		fmt.Printf("  … %d older entries omitted\n", len(records)-maxDisplay)
	}
	return 0
}

// auditJSON prints the full audit trail as pretty-printed JSON.
func auditJSON(records []core.AuditRecord) int {
	if records == nil {
		records = []core.AuditRecord{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(records); err != nil {
		fmt.Fprintln(os.Stderr, "audit: json error:", err)
		return 1
	}
	return 0
}

// truncateResult returns the first line of a result, truncated to 80 chars.
func truncateResult(s string) string {
	if s == "" {
		return ""
	}
	// Take first line
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// truncateArgs returns a shortened version of the args string for display.
func truncateArgs(s string) string {
	if s == "" {
		return ""
	}
	// Try to extract a meaningful path from JSON args like {"path":"foo.go"}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &raw) == nil {
		// Show path or file if present
		for _, key := range []string{"path", "file", "name", "command"} {
			if val, ok := raw[key]; ok {
				var str string
				if json.Unmarshal(val, &str) == nil {
					return str
				}
				return string(val)
			}
		}
	}
	// Fallback: truncate raw args
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}
