package permission

import "strings"

// stripShellWrappers removes common shell wrappers from a command string
// and returns the actual command and its arguments.
//
// Examples:
//
//	"timeout 30s nice -n 10 npm install" → "npm", "install"
//	"npx eslint ." → "npx", "eslint ."
//	"/usr/bin/python3 test.py" → "python3", "test.py"
//	"npm test" → "npm", "test"
//	"docker compose up" → "docker", "compose up"
//	"go build ./..." → "go", "build ./..."
func stripShellWrappers(cmd string) (binary string, rest string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", ""
	}

	// Tokenize
	tokens := tokenize(cmd)
	if len(tokens) == 0 {
		return "", ""
	}

	// Skip known wrapper prefixes
	wrappers := map[string]bool{
		"timeout": true, "nice": true, "nohup": true,
		"env": true, "sudo": true, "doas": true,
		"eval": true, "sh": true, "bash": true, "zsh": true,
		"cmd": true, "powershell": true,
	}

	start := 0
	for start < len(tokens) && wrappers[tokens[start]] {
		start++
		// Skip wrapper arguments: flags (-x), key=value pairs (env vars), and numeric durations
		for start < len(tokens) && (strings.HasPrefix(tokens[start], "-") ||
			strings.Contains(tokens[start], "=") ||
			isFlagArg(tokens[start])) {
			start++
		}
	}

	if start >= len(tokens) {
		bin := tokens[0]
		if idx := strings.LastIndex(bin, "/"); idx >= 0 && idx < len(bin)-1 {
			bin = bin[idx+1:]
		}
		return bin, ""
	}

	binary = tokens[start]
	// Strip common path prefixes (e.g. /usr/bin/python3 → python3)
	if idx := strings.LastIndex(binary, "/"); idx >= 0 && idx < len(binary)-1 {
		binary = binary[idx+1:]
	}

	return binary, strings.Join(tokens[start+1:], " ")
}

// tokenize splits a command string into tokens, respecting quotes.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inQuote = true
			quoteChar = ch
			continue
		}
		if ch == ' ' || ch == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// isFlagArg returns true if the token looks like a flag argument:
//   - starts with - (like -n, --timeout)
//   - is a number (like 30s, 10, 0.5)
//   - is a shell redirect (like >, >>, 2>&1)
func isFlagArg(s string) bool {
	if strings.HasPrefix(s, "-") {
		return true
	}
	if strings.HasPrefix(s, ">") || strings.HasPrefix(s, "<") || s == "2>&1" || s == "2>" {
		return true
	}
	// Numbers and durations (e.g. 30s, 10, 0.5)
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		return true
	}
	return false
}

// CleanCommand extracts the meaningful command from a possibly-wrapped shell command.
// Used by Subject() to improve permission matching.
func CleanCommand(cmd string) string {
	bin, args := stripShellWrappers(cmd)
	if args == "" {
		return bin
	}
	return bin + " " + args
}
