package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

const (
	owner = "esengine"
	repo  = "ok"
)

// updateCommand checks GitHub for a newer release and optionally downloads it.
func updateCommand(args []string, currentVersion string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only check for update, don't download")
	force := fs.Bool("force", false, "force re-download even if same version")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "update: parse flags: %v\n", err)
		return 1
	}

	if currentVersion == "dev" {
		fmt.Println("⚠️  Dev build — auto-update not available (build from source or download a release)")
		return 0
	}

	fmt.Printf("🔍 Checking for updates (current: v%s)...\n", currentVersion)

	release, err := fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to check: %v\n", err)
		return 1
	}

	latest := strings.TrimPrefix(release.TagName, "v")

	if latest == currentVersion && !*force {
		fmt.Printf("✅ Already up-to-date (v%s)\n", currentVersion)
		return 0
	}

	fmt.Printf("📦 New version available: v%s → v%s\n", currentVersion, latest)

	if *checkOnly {
		return 0
	}

	if !*yes {
		fmt.Print("Download and install? [Y/n] ")
		var reply string
		_, _ = fmt.Scanln(&reply)
		if reply != "" && !strings.HasPrefix(strings.ToLower(reply), "y") {
			fmt.Println("Update cancelled.")
			return 0
		}
	}

	return downloadAndInstall(latest, release)
}

// downloadAndInstall downloads the binary, verifies it, backs up the old one, and replaces it.
func downloadAndInstall(version string, _ *gitHubRelease) int {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	arch := runtime.GOARCH
	if arch == "aarch64" {
		arch = "arm64"
	}
	binaryName := fmt.Sprintf("ok-%s-%s%s", runtime.GOOS, arch, ext)
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s", owner, repo, version, binaryName)

	fmt.Printf("⬇️  Downloading %s ...\n", binaryName)

	out, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot determine executable path: %v\n", err)
		return 1
	}

	// Resolve symlinks (common on macOS where the binary might be a symlink).
	if resolved, err := filepath.EvalSymlinks(out); err == nil {
		out = resolved
	}

	// Download to temp file.
	tmpPath := out + ".tmp"
	if err := downloadFile(downloadURL, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Download failed: %v\n", err)
		os.Remove(tmpPath)
		return 1
	}
	fmt.Printf("   Downloaded to temp file\n")

	// Verify SHA256 if checksums file is available.
	checksumsURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/checksums.txt", owner, repo, version)
	if ok, err := verifyChecksum(checksumsURL, tmpPath, binaryName); err != nil {
		fmt.Printf("   ⚠️  Checksum verification skipped: %v\n", err)
	} else if !ok {
		fmt.Fprintf(os.Stderr, "❌ Checksum mismatch! Download may be corrupted or tampered.\n")
		fmt.Fprintf(os.Stderr, "   Try again or download manually from %s\n", downloadURL)
		os.Remove(tmpPath)
		return 1
	} else {
		fmt.Printf("   ✅ SHA256 checksum verified\n")
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Could not set executable permission: %v\n", err)
	}

	// On Windows you cannot rename over a running exe. Workaround:
	// rename the current binary to .old, then rename .tmp into place.
	// If the running exe prevents the rename of current to .old, fall
	// back to a batch script that performs the swap after exit.
	// The .old file is cleaned up on next successful update.
	oldPath := out + ".old"
	renamed := false
	if runtime.GOOS == "windows" {
		if err := os.Rename(out, oldPath); err == nil {
			renamed = true
			fmt.Printf("   💾 Old binary moved to %s\n", filepath.Base(oldPath))
		}
	}

	// Replace binary.
	if err := os.Rename(tmpPath, out); err != nil {
		if runtime.GOOS == "windows" {
			// Windows locks running exes — write a batch script that does the swap.
			batPath := out + ".update.bat"
			bat := fmt.Sprintf("@echo off\r\nping -n 2 127.0.0.1 >nul\r\nmove /Y \"%s\" \"%s\"\r\ndel \"%%~f0\"\r\n", tmpPath, out)
			if werr := os.WriteFile(batPath, []byte(bat), 0o644); werr == nil {
				fmt.Printf("   📝 Update script written: %s\n", filepath.Base(batPath))
				fmt.Println("   Run it after OK exits to complete the update.")
				fmt.Printf("✅ Update staged. Close OK and double-click %s\n", filepath.Base(batPath))
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "❌ Cannot update binary: %v\n", err)
		// Restore old binary if we renamed it.
		if renamed {
			if restoreErr := os.Rename(oldPath, out); restoreErr == nil {
				fmt.Println("   ✅ Binary restored from backup")
			}
		}
		os.Remove(tmpPath)
		return 1
	}

	// Clean up the .old file from this or a previous update attempt.
	os.Remove(oldPath)

	fmt.Printf("✅ Updated to v%s\n", version)
	fmt.Println("   Restart OK to use the new version.")
	return 0
}

// downloadFile downloads a URL to a local path.
func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer log.Close("response", resp.Body)

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			return fmt.Errorf("HTTP %d (body unreadable: %w)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download incomplete after %d bytes: %w", written, err)
	}
	return nil
}

// verifyChecksum downloads the checksums file and verifies the downloaded binary.
func verifyChecksum(checksumsURL, binaryPath, binaryName string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return false, fmt.Errorf("request error: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("network error: %w", err)
	}
	defer log.Close("response", resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("no checksums file for this release")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("checksums HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read checksums: %w", err)
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
		return false, fmt.Errorf("no checksum entry for %s", binaryName)
	}

	f, err := os.Open(binaryPath)
	if err != nil {
		return false, fmt.Errorf("cannot open downloaded file: %w", err)
	}
	defer log.Close("file", f)

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("hash error: %w", err)
	}
	gotHash := hex.EncodeToString(h.Sum(nil))

	return strings.EqualFold(gotHash, expectedHash), nil
}

// gitHubRelease represents a GitHub release API response.
type gitHubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		Size               int    `json:"size"`
		DownloadCount      int    `json:"download_count"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// checkForUpdateResult holds the result of a background update check.
type checkForUpdateResult struct {
	Available bool
	Current   string
	Latest    string
	Release   *gitHubRelease
	Err       error
}

// checkForUpdate performs a non-blocking update check.
func checkForUpdate(currentVersion string) <-chan checkForUpdateResult {
	ch := make(chan checkForUpdateResult, 1)
	if currentVersion == "dev" {
		ch <- checkForUpdateResult{Available: false, Current: currentVersion}
		return ch
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- checkForUpdateResult{Current: currentVersion, Err: fmt.Errorf("panic: %v", r)}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		release, err := fetchRelease(ctx)
		if err != nil {
			ch <- checkForUpdateResult{Current: currentVersion, Err: err}
			return
		}

		latest := strings.TrimPrefix(release.TagName, "v")
		available := latest != currentVersion

		ch <- checkForUpdateResult{
			Available: available,
			Current:   currentVersion,
			Latest:    latest,
			Release:   release,
		}
	}()
	return ch
}

// lastUpdateCheckFile returns the path to the timestamp file.
func lastUpdateCheckFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ok", ".last-update-check")
}

// shouldCheckUpdate returns true if it's been >24h since the last check.
func shouldCheckUpdate() bool {
	path := lastUpdateCheckFile()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > 24*time.Hour
}

// markUpdateChecked records that we just checked for an update.
func markUpdateChecked() {
	path := lastUpdateCheckFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "update: mkdir for check file: %v\n", err)
		return
	}
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "update: write check file: %v\n", err)
	}
}

// printUpdateNotice prints a one-line update notice to stderr.
func printUpdateNotice(result checkForUpdateResult) {
	if result.Err != nil {
		return
	}
	if result.Available {
		fmt.Fprintf(os.Stderr, "\n📦 Update available: v%s → v%s — run `ok update`\n", result.Current, result.Latest)
	}
}

// fetchLatestRelease is the public API (no context timeout, used by the CLI).
func fetchLatestRelease() (*gitHubRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return fetchRelease(ctx)
}

// fetchRelease calls the GitHub API to get the latest release.
func fetchRelease(ctx context.Context) (*gitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "ok-updater/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer log.Close("response", resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		resetStr := resp.Header.Get("X-RateLimit-Reset")
		if resetStr != "" {
			if resetUnix, parseErr := strconv.ParseInt(resetStr, 10, 64); parseErr == nil {
				wait := time.Until(time.Unix(resetUnix, 0))
				return nil, fmt.Errorf("GitHub API rate-limited — try again in %v", wait.Round(time.Second))
			}
		}
		return nil, fmt.Errorf("GitHub API rate-limited — try again later")
	case http.StatusNotFound:
		return nil, fmt.Errorf("no releases found for %s/%s", owner, repo)
	default:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256))
		if readErr != nil {
			return nil, fmt.Errorf("API returned HTTP %d (body unreadable: %w)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release gitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("API response missing tag_name")
	}

	return &release, nil
}
