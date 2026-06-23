// Package credential provides portable OS keyring access for storing API keys
// and other secrets. On macOS it uses the Keychain, on Windows the Credential
// Manager, on Linux the D-Bus Secret Service (falls back to env var when
// keyring is unavailable).
package credential

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "ok"

// Set stores a secret in the OS keyring. On Linux the D-Bus Secret Service
// must be running; Set returns an error when unavailable (callers should
// fall back to env-var or file storage).
func Set(key, value string) error {
	return keyring.Set(service, key, value)
}

// Get retrieves a secret from the OS keyring.
func Get(key string) (string, error) {
	val, err := keyring.Get(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("credential %q not found", key)
	}
	return val, err
}

// Delete removes a secret from the OS keyring.
func Delete(key string) error {
	return keyring.Delete(service, key)
}
