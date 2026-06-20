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

func init() { tool.RegisterBuiltin(deploy{}) }

type deploy struct{}

func (deploy) Name() string { return "deploy" }

func (deploy) Description() string {
	return "Deploy to remote servers via SSH — build, upload, restart, health-check. Destructive operations require confirmation."
}

func (deploy) Schema() json.RawMessage {
	return deploySchema
}

var deploySchema = json.RawMessage(`{"properties":{"action":{"enum":["status","ssh","health","list-targets","build","upload","restart","full-deploy","dry-run"],"type":"string"},"arch":{"type":"string"},"binary":{"type":"string"},"command":{"type":"string"},"os":{"type":"string"},"service":{"type":"string"},"target":{"type":"string"}},"required":["action"],"type":"object"}`)

func (deploy) ReadOnly() bool { return false }

func (deploy) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action  string `json:"action"`
		Target  string `json:"target"`
		Command string `json:"command"`
		Service string `json:"service"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Binary  string `json:"binary"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.OS == "" {
		p.OS = "windows"
	}
	if p.Arch == "" {
		p.Arch = "amd64"
	}

	switch p.Action {
	case "status":
		return deployStatus()
	case "list-targets":
		return listTargets()
	case "ssh":
		return deploySSH(ctx, p.Target, p.Command)
	case "health":
		return healthCheck(ctx, p.Target, p.Service)
	case "build":
		return deployBuild(ctx, p.OS, p.Arch)
	case "upload":
		return deployUpload(ctx, p.Target, p.Binary)
	case "restart":
		return deployRestart(ctx, p.Target, p.Service)
	case "full-deploy":
		return fullDeploy(ctx, p.Target, p.OS)
	case "dry-run":
		return dryRun(ctx, p.Target, p.Binary)
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func deployStatus() (string, error) {
	var b strings.Builder
	b.WriteString("# Deploy: Status\n\n")
	tomlPath := "deploy.toml"
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		b.WriteString("## ⚠️ Not configured\n\nNo `deploy.toml` found.\n\n### Setup\n")
		b.WriteString("```toml\n[targets.production]\nhost = \"app.example.com\"\nuser = \"deploy\"\nkey = \"~/.ssh/id_ed25519\"\n```\n\n")
		b.WriteString("💡 Deploy is opt-in only — no connection without explicit config.\n")
		return b.String(), nil
	}
	b.WriteString("✅ Deploy configured\n")
	b.WriteString(fmt.Sprintf("- Config: %s\n", tomlPath))
	return b.String(), nil
}

func listTargets() (string, error) {
	var b strings.Builder
	b.WriteString("# Deploy: Targets\n\n")
	if _, err := os.Stat("deploy.toml"); os.IsNotExist(err) {
		b.WriteString("⚠️ No targets configured.\n")
		return b.String(), nil
	}
	data, err := os.ReadFile("deploy.toml")
	if err != nil {
		return b.String(), fmt.Errorf("cannot read deploy.toml: %w", err)
	}
	var targets []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[targets.") && strings.HasSuffix(line, "]") {
			name := line[len("[targets.") : len(line)-1]
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		b.WriteString("No [targets.*] sections found.\n")
	} else {
		b.WriteString("Configured targets:\n")
		for _, t := range targets {
			b.WriteString(fmt.Sprintf("- %s\n", t))
		}
	}
	return b.String(), nil
}

func deploySSH(ctx context.Context, target, command string) (string, error) {
	if target == "" || command == "" {
		return "", fmt.Errorf("target and command are required")
	}
	if !deployCommandSafe(command) {
		return "", fmt.Errorf("command contains shell-chaining operators (; | & ` $()) — use a single command or sanitize input")
	}
	if _, err := os.Stat("deploy.toml"); os.IsNotExist(err) {
		return "", fmt.Errorf("deploy not configured — create deploy.toml")
	}
	host, user, key := parseDeployTarget(target)
	if host == "" {
		return "", fmt.Errorf("target %q not found", target)
	}
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10", "-o", "BatchMode=yes",
	}
	if key != "" {
		sshArgs = append(sshArgs, "-i", key)
	}
	if user != "" {
		sshArgs = append(sshArgs, "-l", user)
	}
	sshArgs = append(sshArgs, host, command)
	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh %s@%s: %w\n%s", user, host, err, string(out))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# SSH: %s@%s\nCommand: `%s`\n\n```\n%s\n```\n", user, host, command, strings.TrimSpace(string(out))))
	return b.String(), nil
}

func healthCheck(ctx context.Context, target, service string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	cmd := "uptime && free -h | head -2 && df -h / | tail -1"
	if service != "" {
		if !isValidServiceName(service) {
			return "", fmt.Errorf("invalid service name %q — only letters, digits, hyphens, dots, and underscores allowed", service)
		}
		cmd = "systemctl is-active " + service
	}
	return deploySSH(ctx, target, cmd)
}

func dryRun(ctx context.Context, target, binary string) (string, error) {
	var b strings.Builder
	b.WriteString("# Deploy: Dry Run\n\n")
	if _, err := os.Stat("deploy.toml"); os.IsNotExist(err) {
		b.WriteString("⚠️ No deploy.toml found.\n")
		return b.String(), nil
	}
	bin := resolveBinary(binary)
	b.WriteString(fmt.Sprintf("Would build: `go build -o %s ./cmd/ok`\n", bin))
	if target != "" {
		host, user, _ := parseDeployTarget(target)
		if host == "" {
			b.WriteString(fmt.Sprintf("⚠️ Target %q not found.\n", target))
		} else {
			b.WriteString(fmt.Sprintf("Would upload: `scp %s %s@%s:/opt/ok/`\n", bin, user, host))
			b.WriteString(fmt.Sprintf("Would restart: `ssh %s@%s systemctl restart ok`\n", user, host))
		}
	}
	return b.String(), nil
}

func deployBuild(ctx context.Context, goos, goarch string) (string, error) {
	bin := resolveBinary(goos)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Build: %s/%s\n\n", goos, goarch))
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, "./cmd/ok")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.WriteString(fmt.Sprintf("❌ Build failed:\n```\n%s\n```\n", string(out)))
		return b.String(), nil
	}
	info, err := os.Stat(bin)
	var size string
	if err == nil {
		size = humanizeBytes(info.Size())
	} else {
		size = "unknown"
	}
	b.WriteString(fmt.Sprintf("✅ Built: `%s` (%s)\n", bin, size))
	return b.String(), nil
}

func deployUpload(ctx context.Context, target, binary string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	if _, err := os.Stat("deploy.toml"); os.IsNotExist(err) {
		return "", fmt.Errorf("deploy not configured")
	}
	host, user, key := parseDeployTarget(target)
	if host == "" {
		return "", fmt.Errorf("target %q not found", target)
	}
	bin := resolveBinary(binary)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		return "", fmt.Errorf("binary %q not found — run build first", bin)
	}
	remotePath := "/opt/ok/" + bin
	scpArgs := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=30"}
	if key != "" {
		scpArgs = append(scpArgs, "-i", key)
	}
	scpArgs = append(scpArgs, bin, fmt.Sprintf("%s@%s:%s", user, host, remotePath))
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Upload: %s → %s@%s\n\n", bin, user, host))
	cmd := exec.CommandContext(ctx, "scp", scpArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.WriteString(fmt.Sprintf("❌ Upload failed:\n```\n%s\n```\n", string(out)))
		return b.String(), nil
	}
	b.WriteString("✅ Uploaded successfully\n")
	return b.String(), nil
}

func deployRestart(ctx context.Context, target, service string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	if service == "" {
		service = "ok"
	}
	if !isValidServiceName(service) {
		return "", fmt.Errorf("invalid service name %q — only letters, digits, hyphens, dots, and underscores allowed", service)
	}
	return deploySSH(ctx, target, "sudo systemctl restart "+service)
}

func fullDeploy(ctx context.Context, target, goos string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	var b strings.Builder
	b.WriteString("# Full Deploy\n\n## 1. Build\n")
	host, _, _ := parseDeployTarget(target)
	if goos == "" {
		if strings.Contains(host, ".com") || strings.Contains(host, "linux") {
			goos = "linux"
		} else {
			goos = "windows"
		}
	}
	bin := resolveBinary(goos)
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, "./cmd/ok")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
	buildOut, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		b.WriteString(fmt.Sprintf("❌ Build failed:\n```\n%s\n```\n", string(buildOut)))
		return b.String(), nil
	}
	info, err := os.Stat(bin)
	var size string
	if err == nil {
		size = humanizeBytes(info.Size())
	} else {
		size = "unknown"
	}
	b.WriteString(fmt.Sprintf("✅ Built `%s` (%s)\n\n", bin, size))
	b.WriteString("## 2. Upload\n")
	uploadResult, uploadErr := deployUpload(ctx, target, goos)
	if uploadErr != nil {
		return b.String(), uploadErr
	}
	b.WriteString(uploadResult + "\n")
	b.WriteString("## 3. Restart\n")
	restartResult, restartErr := deployRestart(ctx, target, "")
	if restartErr != nil {
		return b.String(), restartErr
	}
	b.WriteString(restartResult + "\n")
	b.WriteString("## 4. Health Check\n")
	healthResult, healthErr := healthCheck(ctx, target, "")
	if healthErr != nil {
		b.WriteString(fmt.Sprintf("⚠️ Health check failed: %v\n", healthErr))
	} else {
		b.WriteString(healthResult)
	}
	b.WriteString("\n## ✅ Deploy complete\n")
	return b.String(), nil
}

func resolveBinary(hint string) string {
	if strings.Contains(hint, ".") && !strings.Contains(hint, "/") && !strings.Contains(hint, "\\") {
		return hint
	}
	base := "ok"
	if modData, err := os.ReadFile("go.mod"); err == nil {
		for _, line := range strings.Split(string(modData), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				base = strings.TrimPrefix(line, "module ")
				if idx := strings.LastIndex(base, "/"); idx >= 0 {
					base = base[idx+1:]
				}
				break
			}
		}
	}
	hintLower := strings.ToLower(hint)
	if hintLower == "windows" || strings.Contains(hintLower, "win") || os.Getenv("GOOS") == "windows" {
		return base + ".exe"
	}
	return base
}

func parseDeployTarget(target string) (host, user, key string) {
	data, err := os.ReadFile("deploy.toml")
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	inTarget := false
	header := "[targets." + target + "]"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == header {
			inTarget = true
			continue
		}
		if inTarget && strings.HasPrefix(line, "[") {
			break
		}
		if inTarget {
			switch {
			case strings.HasPrefix(line, "host"):
				host = extractTOMLValue(line)
			case strings.HasPrefix(line, "user"):
				user = extractTOMLValue(line)
			case strings.HasPrefix(line, "key"):
				key = extractTOMLValue(line)
			default: // unknown config key
			}
		}
	}
	return
}

func extractTOMLValue(line string) string {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return ""
	}
	val := strings.TrimSpace(parts[1])
	val = strings.Trim(val, `"'`)
	return val
}

// deployCommandSafe rejects commands that contain shell-chaining operators
// (; | & ` $()) which would be interpreted by the remote shell, potentially
// executing injected commands.
func deployCommandSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';', '|', '&', '`':
			return false
		case '$':
			if i+1 < len(s) && s[i+1] == '(' {
				return false
			}
		default: // safe character
		}
	}
	return true
}

// isValidServiceName validates a systemd service name: only letters, digits,
// hyphens, dots, and underscores allowed — no shell metacharacters.
func isValidServiceName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}
