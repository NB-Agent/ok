package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type PolicyFile struct {
	Deny  []string `toml:"deny"`
	Allow []string `toml:"allow"`
	Ask   []string `toml:"ask"`
}

func LoadPolicy(homeDir string) (PolicyFile, error) {
	path := filepath.Join(homeDir, ".config", "ok", "policy.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PolicyFile{}, nil // no policy file is not an error
		}
		return PolicyFile{}, fmt.Errorf("policy: read %s: %w", path, err)
	}
	var pf PolicyFile
	if err := toml.Unmarshal(b, &pf); err != nil {
		return PolicyFile{}, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	return pf, nil
}

func MergeModeRules(allow, ask, deny []string, policy PolicyFile) ([]string, []string, []string) {
	deny = append(policy.Deny, deny...)
	allow = append(allow, policy.Allow...)
	ask = append(ask, policy.Ask...)
	return allow, ask, deny
}
