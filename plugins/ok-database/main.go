// @ok/database — MCP plugin: SQLite/PostgreSQL/MySQL via CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NB-Agent/ok/internal/plugin"
)

type server struct{}

func (server) Info() (string, string) { return "ok-database", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{
			Name:        "database",
			Description: "Query SQLite/PostgreSQL/MySQL databases",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"driver": plugin.StrEnum("sqlite3", "postgres", "mysql"),
					"dsn":    plugin.StrProp(),
					"query":  plugin.StrProp(),
				},
				"required": []string{"dsn", "query"},
			},
		},
	}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "database" {
		return "", fmt.Errorf("unknown: %s", name)
	}
	var p struct{ Driver, DSN, Query string }
	json.Unmarshal(args, &p)
	if p.DSN == "" || p.Query == "" {
		return "", fmt.Errorf("dsn and query required")
	}
	return queryDB(p.Driver, p.DSN, p.Query)
}

func main() { plugin.RunStdio(server{}) }

func queryDB(driver, dsn, q string) (string, error) {
	if hasMultipleStatements(q) {
		return "", fmt.Errorf("multiple SQL statements are not allowed for security reasons")
	}
	if !isValidDSN(driver, dsn) {
		return "", fmt.Errorf("invalid or unsafe DSN for driver %s", driver)
	}
	switch driver {
	case "sqlite3":
		out, err := exec.Command("sqlite3", dsn, "-json", q).Output()
		if err != nil {
			return "", fmt.Errorf("sqlite3: %w", err)
		}
		var buf bytes.Buffer
		json.Indent(&buf, out, "", "  ")
		return buf.String(), nil
	case "postgres":
		out, err := exec.Command("psql", dsn, "-c", q).Output()
		return strings.TrimSpace(string(out)), err
	case "mysql":
		out, err := exec.Command("mysql", "-e", q).Output()
		return strings.TrimSpace(string(out)), err
	}
	return "", fmt.Errorf("unsupported driver: %s", driver)
}

func isValidDSN(driver, dsn string) bool {
	if dsn == "" {
		return false
	}
	for _, r := range dsn {
		if r == ';' || r == '|' || r == '&' || r == '$' || r == '`' || r == '"' || r == '\'' || r == '\n' || r == '\r' {
			return false
		}
	}
	switch driver {
	case "sqlite3":
		if strings.Contains(dsn, "..") {
			return false
		}
		if strings.HasPrefix(dsn, "/dev/") || strings.HasPrefix(dsn, "/proc/") || strings.HasPrefix(dsn, "/sys/") {
			return false
		}
	case "postgres":
		if strings.HasPrefix(dsn, "-") {
			return false
		}
		if strings.Contains(dsn, " -c ") || strings.Contains(dsn, " -f ") {
			return false
		}
	case "mysql":
	}
	return true
}

func hasMultipleStatements(query string) bool {
	inString := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			inString = !inString
			continue
		}
		if ch == ';' && !inString {
			return true
		}
	}
	return false
}
