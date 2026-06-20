// Package builtin provides the built-in tools for the OK agent.
//
// bash_security.go — command classification and whitelist for bash.Execute
package builtin

import (
	"fmt"
	"strings"
)

// bashSecurity holds command classification rules.
type bashSecurity struct {
	AllowList []string
	AskList   []string
	DenyList  []string
}

var defaultBashSecurity = bashSecurity{
	AllowList: []string{
		"ls", "cat", "head", "tail", "echo", "pwd", "which",
		"find", "grep", "sort", "uniq", "wc", "diff",
		"npm", "npx", "node", "go", "rustc", "cargo",
		"git status", "git log", "git diff", "git show",
		"python", "python3", "pip",
		"make", "cmake",
		"docker ps", "docker images",
		"date", "whoami", "hostname", "uname",
		"tree", "stat", "file", "du", "df",
	},
	AskList: []string{
		"git push", "git commit", "git merge", "git rebase",
		"docker run", "docker build", "docker push",
		"npm publish", "npm install",
		"go install", "go build",
		"pip install", "pip3 install",
		"kill", "pkill",
		"sudo", "su",
		"chmod", "chown",
		"mkdir", "rmdir",
		"cp", "mv", "rm", "rm -rf",
		"curl", "wget",
		"env",
	},
	DenyList: []string{
		"rm -rf /", "rm -rf /*", "rm -rf --no-preserve-root",
		"dd if=", ":(){ :|:& };:",
		"mkfs", "fdisk", "format",
		"shutdown", "reboot", "halt",
		"passwd", "useradd", "userdel",
		"iptables", "ufw",
	},
}

// hasShellChaining reports whether cmd contains shell metacharacters that allow
// chaining multiple commands — the primary attack vector against the prefix
// whitelist. Commands containing these are never auto-allowed.
func hasShellChaining(cmd string) bool {
	// Track both single-quote and double-quote context.
	// Inside single quotes the shell treats everything literally.
	// Inside double quotes, ; and ` are literal, but $(), ${}, and & are not.
	inSingle := false
	inDouble := false
	for i := 0; i < len(cmd); i++ {
		switch cmd[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';', '|':
			if !inSingle && !inDouble {
				return true
			}
		case '&':
			if !inSingle && !inDouble {
				// & is chaining unless it's part of "2>&1" redirect.
				// Check: digit preceded by > is a redirect, not chaining.
				if i > 0 && cmd[i-1] == '>' {
					continue // "2>&1" redirect
				}
				return true
			}
		case '`':
			if !inSingle && !inDouble {
				return true // backtick command substitution
			}
		case '$':
			// $() is POSIX command substitution — equivalent to backticks.
			if !inSingle && !inDouble && i+1 < len(cmd) && cmd[i+1] == '(' {
				return true
			}
		default: // not a shell metacharacter
		}
	}
	return false
}

// classifyCommand returns "allow", "ask", or "deny" for a command string.
func classifyCommand(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return "deny"
	}

	// Check deny list with substring match (not prefix) so dangerous commands
	// are caught even when embedded: "echo hi; rm -rf /" matches "rm -rf /".
	for _, denied := range defaultBashSecurity.DenyList {
		if strings.Contains(trimmed, denied) {
			return "deny"
		}
	}

	// Check for shell chaining metacharacters. If found and the command is not
	// in the allow list as a full-prefix match, force "ask" (user approval).
	hasChain := hasShellChaining(trimmed)

	for _, prefix := range defaultBashSecurity.AllowList {
		if strings.HasPrefix(trimmed, prefix) {
			if hasChain {
				// Command starts with an allowed prefix but chains more commands
				// (e.g. "ls -la; rm -rf /"). Require approval.
				return "ask"
			}
			return "allow"
		}
	}
	for _, prefix := range defaultBashSecurity.AskList {
		if strings.HasPrefix(trimmed, prefix) {
			return "ask"
		}
	}

	return "ask"
}

// CheckCommand runs before bash execution. Returns nil to allow, error to deny.
func CheckCommand(cmd string) error {
	// "ask" commands require user approval, but the permission gate is not
	// wired through this path — treat as denied until the full gate is connected.
	switch classifyCommand(cmd) {
	case "deny", "ask":
		return fmt.Errorf("command denied by security policy: %q", cmd)
	case "allow":
		return nil
	default:
		return fmt.Errorf("command not classified: %q", cmd)
	}
}
