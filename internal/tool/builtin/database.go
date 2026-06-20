package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(databaseTool{}) }

type databaseTool struct{}

func (databaseTool) Name() string { return "database" }

func (databaseTool) Description() string {
	return "Query databases. Connects to SQLite by default; PostgreSQL and MySQL via CLI. Execute SQL SELECT/INSERT/UPDATE/DELETE and return structured results. ⚠️  Uses raw SQL — the caller is responsible for safe query construction."
}

func (databaseTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"args":{"type":"string"},"driver":{"enum":["sqlite3","postgres","mysql"],"type":"string"},"dsn":{"type":"string"},"query":{"type":"string"}},"required":["dsn","query"],"type":"object"}`)
}

func (databaseTool) ReadOnly() bool { return false }

func (databaseTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Driver string `json:"driver"`
		DSN    string `json:"dsn"`
		Query  string `json:"query"`
		Args   string `json:"args"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.DSN == "" {
		return "", fmt.Errorf("dsn is required")
	}
	if p.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if p.Driver == "" {
		p.Driver = "sqlite3"
	}

	// Normalise query: trim and remove trailing semicolon for CLI tools.
	query := strings.TrimSpace(p.Query)
	query = strings.TrimRight(query, ";")

	// Reject multi-statement queries to prevent SQL injection via
	// appended statements (e.g. "SELECT 1; DROP TABLE users").
	if hasMultipleStatements(query) {
		return "", fmt.Errorf("multi-statement queries are not allowed")
	}

	switch p.Driver {
	case "sqlite3":
		// Feed query via stdin to keep it off the command line.
		cmd := exec.CommandContext(ctx, "sqlite3", "-header", "-column", p.DSN)
		cmd.Stdin = strings.NewReader(query)
		out, err := cmd.CombinedOutput()
		output := strings.TrimSpace(string(out))

		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Database (SQLite)\n\n`%s`\n\n", maskDSN(p.DSN)))
		if err != nil {
			b.WriteString(fmt.Sprintf("⚠️  %v\n\n", err))
		}
		if output != "" {
			b.WriteString("```\n" + output + "\n```\n")
		}

		// Count rows affected for non-SELECT.
		upper := strings.ToUpper(query)
		if strings.HasPrefix(upper, "INSERT") || strings.HasPrefix(upper, "UPDATE") || strings.HasPrefix(upper, "DELETE") {
			rows := exec.CommandContext(ctx, "sqlite3", p.DSN)
			rows.Stdin = strings.NewReader("SELECT changes();")
			if r, e := rows.CombinedOutput(); e == nil {
				b.WriteString(fmt.Sprintf("\nRows affected: %s\n", strings.TrimSpace(string(r))))
			}
		}

		if output == "" && err == nil {
			b.WriteString("✅ Done\n")
		}
		return b.String(), nil

	case "postgres":
		// Use psql CLI. Pass password via PGPASSWORD env var to keep it off the command line.
		_, pass := extractUserPass(p.DSN)
		connStr := maskDSN(p.DSN)
		cmd := exec.CommandContext(ctx, "psql", connStr)
		if pass != "" {
			cmd.Env = append(os.Environ(), "PGPASSWORD="+pass)
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimSpace(string(out))
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Database (PostgreSQL)\n\n`%s`\n\n", connStr))
		if err != nil {
			b.WriteString(fmt.Sprintf("⚠️  %v\n\n", err))
		}
		if output != "" {
			b.WriteString("```\n" + output + "\n```\n")
		}
		if output == "" && err == nil {
			b.WriteString("✅ Done\n")
		}
		return b.String(), nil

	case "mysql":
		// Use mysql CLI. Pass password via MYSQL_PWD env var to keep it off the command line.
		user, pass := extractUserPass(p.DSN)
		cmd := exec.CommandContext(ctx, "mysql", "-h", extractHost(p.DSN), "-u", user, p.DSN)
		if pass != "" {
			cmd.Env = append(os.Environ(), "MYSQL_PWD="+pass)
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimSpace(string(out))
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Database (MySQL)\n\n`%s`\n\n", maskDSN(p.DSN)))
		if err != nil {
			b.WriteString(fmt.Sprintf("⚠️  %v\n\n", err))
		}
		if output != "" {
			b.WriteString("```\n" + output + "\n```\n")
		}
		if output == "" && err == nil {
			b.WriteString("✅ Done\n")
		}
		return b.String(), nil

	default:
		return "", fmt.Errorf("unknown driver: %s", p.Driver)
	}
}

func extractHost(dsn string) string {
	// Simple parser for mysql DSN: user:pass@tcp(host:port)/dbname
	if idx := strings.Index(dsn, "@tcp("); idx >= 0 {
		rest := dsn[idx+5:]
		if end := strings.Index(rest, ")"); end >= 0 {
			return rest[:end]
		}
	}
	return "localhost"
}

//lint:ignore U1000 kept for future DSN parsing use
func extractUser(dsn string) string {
	u, _ := extractUserPass(dsn)
	return u
}

// extractUserPass extracts username and password from a DSN.
// Supports formats: "user:pass@tcp(host:port)/db" or "postgres://user:pass@host/db".
func extractUserPass(dsn string) (user, pass string) {
	// Strip postgres:// or mysql:// prefix.
	dsn = strings.TrimPrefix(dsn, "postgres://")
	dsn = strings.TrimPrefix(dsn, "mysql://")

	// Find user:password before @
	aidx := strings.Index(dsn, "@")
	if aidx < 0 {
		return "", ""
	}
	creds := dsn[:aidx]
	cidx := strings.Index(creds, ":")
	if cidx < 0 {
		return creds, ""
	}
	return creds[:cidx], creds[cidx+1:]
}

// maskDSN returns a sanitized DSN with the password replaced by "****" for safe display.
func maskDSN(dsn string) string {
	_, pass := extractUserPass(dsn)
	if pass == "" {
		return dsn
	}
	return strings.Replace(dsn, ":"+pass+"@", ":****@", 1)
}

// hasMultipleStatements detects SQL injection by checking for multiple
// statements (semicolons outside of single-quoted string literals).
// Single-statement queries pass; anything with a real statement separator
// (the ; operator) is rejected.
func hasMultipleStatements(query string) bool {
	inString := false
	inDollar := false
	dollarTag := ""
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '$' && !inString && !inDollar {
			j := i + 1
			for j < len(query) && query[j] != '$' {
				j++
			}
			if j < len(query) && query[j] == '$' {
				dollarTag = query[i+1 : j]
				inDollar = true
				i = j
				continue
			}
		}
		if ch == '$' && inDollar && !inString {
			j := i + 1
			for j < len(query) && j-i-1 < len(dollarTag) && query[j] == dollarTag[j-i-1] {
				j++
			}
			if j < len(query) && query[j] == '$' && j-i-1 == len(dollarTag) {
				inDollar = false
				dollarTag = ""
				i = j
				continue
			}
		}
		if ch == '\'' && !inDollar {
			// Toggle string state; double-quote is for identifiers, not strings.
			inString = !inString
			continue
		}
		if ch == ';' && !inString && !inDollar {
			return true
		}
		// Skip escaped quotes.
		if ch == '\\' && i+1 < len(query) {
			i++
		}
	}
	return false
}
