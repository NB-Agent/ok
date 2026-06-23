package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/log"
)

// loadDotEnv loads .env files into the process environment without overriding
// variables that are already set. The working-directory .env is read first, so a
// project-local key takes precedence; then ~/.env is read as a fallback. This
// unifies the key source across frontends: the desktop app's working dir is
// $HOME so it writes ~/.env, and the CLI — run from any project directory — now
// picks up that same key instead of needing a copy in every project's .env.
// Existing environment variables always win over both files.
func loadDotEnv() {
	loadDotEnvFile(".env")
	if home, err := os.UserHomeDir(); err == nil {
		loadDotEnvFile(filepath.Join(home, ".env"))
	}
}

// loadDotEnvFile reads one .env file (if present) and sets any keys not already
// present in the environment. Lenient, zero-dependency parsing.
func loadDotEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer log.Close("dotenv", f)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		// Strip surrounding quotes but preserve embedded ones.
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			q := val[0]
			if (q == '\'' || q == '"') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
