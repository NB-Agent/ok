// Command ok-installer is the Windows one-click installer for OK Universal Agent.
// It downloads the latest release, installs to Program Files, adds to PATH,
// creates startup entry, and writes a .env template with detected API keys.
//
// Build: cd cmd/ok-installer && go build -ldflags="-s -w" -o ../../ok-installer.exe
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

const (
	owner = "esengine"
	repo  = "ok"
)

var installDir = filepath.Join(os.Getenv("ProgramFiles"), "OK Agent")

func main() {
	fmt.Println("")
	fmt.Println("  ╔══════════════════════════════════════╗")
	fmt.Println("  ║   OK Universal Agent - Installer    ║")
	fmt.Println("  ╚══════════════════════════════════════╝")
	fmt.Println("")
	fmt.Println("This will install OK Agent to:", installDir)
	fmt.Println("")

	// 1. Check if already installed.
	if _, err := os.Stat(filepath.Join(installDir, "ok.exe")); err == nil {
		fmt.Print("OK is already installed. Reinstall? [y/N] ")
		var reply string
		_, _ = fmt.Scanln(&reply)
		if !strings.HasPrefix(strings.ToLower(reply), "y") {
			fmt.Println("Installation cancelled.")
			pauseExit(0)
			return
		}
	}

	// 2. Fetch latest release.
	fmt.Print("Fetching latest release... ")
	release, err := fetchLatestRelease()
	if err != nil {
		fmt.Println("FAILED")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		pauseExit(1)
		return
	}
	version := strings.TrimPrefix(release.TagName, "v")
	fmt.Printf("v%s\n", version)

	// 3. Find and download the Windows binary.
	binaryName := fmt.Sprintf("ok-windows-%s.exe", archSuffix())
	downloadURL := findAssetURL(release.Assets, binaryName)
	if downloadURL == "" {
		binaryName = fmt.Sprintf("ok-%s-%s.exe", runtime.GOOS, archSuffix())
		downloadURL = findAssetURL(release.Assets, binaryName)
	}
	if downloadURL == "" {
		fmt.Fprintf(os.Stderr, "Error: no binary found for %s\n", binaryName)
		pauseExit(1)
		return
	}

	fmt.Printf("Downloading %s... ", binaryName)
	tmpDir, err := os.MkdirTemp("", "ok-install-*")
	if err != nil {
		fmt.Println("FAILED")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		pauseExit(1)
		return
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "ok.exe")
	if err := downloadFile(downloadURL, binPath); err != nil {
		fmt.Println("FAILED")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		pauseExit(1)
		return
	}
	fmt.Println("done")

	// 4. Verify checksum.
	checksumsURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/checksums.txt", owner, repo, version)
	fmt.Print("Verifying checksum... ")
	if ok, err := verifyChecksum(checksumsURL, binPath, binaryName); err != nil {
		fmt.Printf("skipped (%v)\n", err)
	} else if !ok {
		fmt.Println("FAILED - download may be corrupted!")
		os.Remove(binPath)
		pauseExit(1)
		return
	} else {
		fmt.Println("verified")
	}

	// 5. Install to Program Files.
	fmt.Print("Installing... ")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		fmt.Println("FAILED")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		pauseExit(1)
		return
	}
	destPath := filepath.Join(installDir, "ok.exe")
	if err := copyFile(binPath, destPath); err != nil {
		fmt.Println("FAILED")
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		pauseExit(1)
		return
	}
	fmt.Println("done")

	// 6. Add to PATH (user-level).
	fmt.Print("Adding to PATH... ")
	if err := addToPATH(installDir); err != nil {
		fmt.Printf("warning: %v\n", err)
	} else {
		fmt.Println("done")
	}

	// 7. Create startup entry.
	fmt.Print("Creating startup entry... ")
	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startupDir, 0o755); err == nil {
		batContent := fmt.Sprintf("@echo off\nstart \"\" \"%s\"\n", destPath)
		if err := os.WriteFile(filepath.Join(startupDir, "OK_Agent.bat"), []byte(batContent), 0o644); err != nil {
			fmt.Printf("warning: could not write startup script: %v\n", err)
		} else {
			fmt.Println("done")
		}
	} else {
		fmt.Printf("skipped (%v)\n", err)
	}

	// 8. Create .env template with detected keys.
	fmt.Print("Creating .env template... ")
	envPath := filepath.Join(installDir, ".env.example")
	detectedKeys := detectAPIKeys()
	if err := writeEnvFile(envPath, detectedKeys); err != nil {
		fmt.Printf("warning: %v\n", err)
	} else {
		fmt.Println("done")
	}

	// 9. Success.
	fmt.Println("")
	fmt.Println("OK Agent installed successfully!")
	fmt.Printf("  Version: v%s\n", version)
	fmt.Printf("  Location: %s\n", destPath)
	fmt.Println("")
	fmt.Println("What's next:")
	fmt.Println("  1. Open a new terminal and run: ok chat")
	fmt.Println("  2. Or press Ctrl+Alt+O to open OK from anywhere")
	fmt.Println("  3. Set API keys in your environment (see .env.example)")
	fmt.Println("")
	fmt.Println("Press Enter to exit.")
	_, _ = fmt.Scanln()
}

func archSuffix() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "386":
		return "386"
	default:
		return runtime.GOARCH
	}
}

func findAssetURL(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}, name string) string {
	for _, a := range assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func fetchLatestRelease() (*struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "ok-installer/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer log.Close("GitHub API response", resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ok-installer/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer log.Close("download response", resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer log.Close("download file", f)

	_, err = io.Copy(f, resp.Body)
	return err
}

func verifyChecksum(checksumsURL, binaryPath, binaryName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer log.Close("checksums response", resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("no checksums file")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	expectedHash := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "  "+binaryName) || strings.HasSuffix(line, " *"+binaryName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				expectedHash = parts[0]
			}
			break
		}
	}
	if expectedHash == "" {
		return false, fmt.Errorf("no entry for %s", binaryName)
	}

	f, err := os.Open(binaryPath)
	if err != nil {
		return false, err
	}
	defer log.Close("checksum file", f)

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expectedHash), nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer log.Close("source file", s)

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer log.Close("dest file", d)

	_, err = io.Copy(d, s)
	return err
}

func addToPATH(dir string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::GetEnvironmentVariable("PATH", "User")`)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("read PATH: %w", err)
	}
	currentPATH := strings.TrimSpace(string(out))

	if strings.Contains(currentPATH, dir) {
		return nil
	}

	newPATH := currentPATH
	if newPATH != "" && !strings.HasSuffix(newPATH, ";") {
		newPATH += ";"
	}
	newPATH += dir

	// Pass newPATH via stdin to avoid PowerShell injection through the
	// command line.  A PATH value containing $() or backticks would be
	// interpreted by PowerShell when embedded in a double-quoted string.
	// The set command reads path data from stdin, keeping untrusted
	// content out of the -Command argument entirely.
	setCmd := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::SetEnvironmentVariable("PATH", [Console]::In.ReadLine(), "User")`)
	setCmd.Stdin = strings.NewReader(newPATH)
	if output, err := setCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set PATH: %w\n%s", err, string(output))
	}

	os.Setenv("PATH", os.Getenv("PATH")+";"+dir)
	return nil
}

func detectAPIKeys() map[string]string {
	keys := map[string]string{
		"DEEPSEEK_API_KEY":  os.Getenv("DEEPSEEK_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		"GEMINI_API_KEY":    os.Getenv("GEMINI_API_KEY"),
		"GROQ_API_KEY":      os.Getenv("GROQ_API_KEY"),
		"MIMO_API_KEY":      os.Getenv("MIMO_API_KEY"),
	}
	for k, v := range keys {
		if v == "" {
			delete(keys, k)
		}
	}
	return keys
}

func writeEnvFile(path string, detectedKeys map[string]string) error {
	var b strings.Builder
	b.WriteString("# OK Agent - API Keys\n")
	b.WriteString("# Copy this file to the OK working directory as .env\n")
	b.WriteString("# Uncomment and set the keys for the providers you want to use.\n\n")

	allKeys := []struct {
		Key     string
		Example string
		Comment string
	}{
		{"DEEPSEEK_API_KEY", "sk-...", "DeepSeek (default, 1M context)"},
		{"OPENAI_API_KEY", "sk-...", "OpenAI GPT-4o"},
		{"ANTHROPIC_API_KEY", "sk-ant-...", "Anthropic Claude"},
		{"GEMINI_API_KEY", "AIza...", "Google Gemini"},
		{"GROQ_API_KEY", "gsk_...", "Groq (free tier available)"},
		{"MIMO_API_KEY", "...", "MiMo"},
		{"PERPLEXITY_API_KEY", "pplx-...", "Perplexity"},
		{"TOGETHER_API_KEY", "...", "Together AI"},
		{"OPENROUTER_API_KEY", "sk-or-...", "OpenRouter (200+ models)"},
		{"XAI_API_KEY", "...", "xAI Grok"},
	}

	for _, k := range allKeys {
		if val, found := detectedKeys[k.Key]; found {
			b.WriteString(fmt.Sprintf("%s=%s   # %s (detected from environment)\n", k.Key, val, k.Comment))
		} else {
			b.WriteString(fmt.Sprintf("# %s=%s   # %s\n", k.Key, k.Example, k.Comment))
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func pauseExit(code int) {
	fmt.Println("Press Enter to exit.")
	_, _ = fmt.Scanln()
	os.Exit(code)
}
