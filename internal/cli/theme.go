// Package cli provides the terminal UI. This file defines the theme system
// which lets users customize colors, spacing, and rendering style.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Theme defines customizable TUI colors and spacing.
type Theme struct {
	// Name identifies the theme for /theme list.
	Name string `json:"name"`

	// Colors
	Primary   string `json:"primary,omitempty"`   // user text / accent
	Secondary string `json:"secondary,omitempty"` // assistant text
	Muted     string `json:"muted,omitempty"`     // reasoning / notices
	Error     string `json:"error,omitempty"`     // error messages
	Success   string `json:"success,omitempty"`   // tool results
	Warning   string `json:"warning,omitempty"`   // warnings
	Bg        string `json:"bg,omitempty"`        // background
	CodeBg    string `json:"code_bg,omitempty"`   // code block background

	// Spacing
	Padding    int `json:"padding,omitempty"`
	Gap        int `json:"gap,omitempty"`
	CodeWidth  int `json:"code_width,omitempty"`
	CodeHeight int `json:"code_height,omitempty"`
}

// DefaultTheme returns the built-in dark theme.
func DefaultTheme() Theme {
	return Theme{
		Name:       "default",
		Primary:    "#7c3aed", // purple
		Secondary:  "#e0e0e0", // light gray
		Muted:      "#888888", // gray
		Error:      "#ef4444", // red
		Success:    "#22c55e", // green
		Warning:    "#f59e0b", // amber
		Bg:         "#0a0a0a", // near-black
		CodeBg:     "#1a1a2e", // dark blue-gray
		Padding:    1,
		Gap:        0,
		CodeWidth:  80,
		CodeHeight: 20,
	}
}

// LightTheme returns a light mode theme.
func LightTheme() Theme {
	return Theme{
		Name:       "light",
		Primary:    "#7c3aed",
		Secondary:  "#1a1a1a",
		Muted:      "#888888",
		Error:      "#dc2626",
		Success:    "#16a34a",
		Warning:    "#d97706",
		Bg:         "#ffffff",
		CodeBg:     "#f5f5f5",
		Padding:    1,
		Gap:        0,
		CodeWidth:  80,
		CodeHeight: 20,
	}
}

// SolarizedTheme returns a solarized-inspired theme.
func SolarizedTheme() Theme {
	return Theme{
		Name:       "solarized",
		Primary:    "#268bd2",
		Secondary:  "#839496",
		Muted:      "#586e75",
		Error:      "#dc322f",
		Success:    "#859900",
		Warning:    "#b58900",
		Bg:         "#002b36",
		CodeBg:     "#073642",
		Padding:    1,
		Gap:        0,
		CodeWidth:  80,
		CodeHeight: 20,
	}
}

// activeTheme is the currently active theme. Default is the dark default.
var activeTheme = DefaultTheme()

// ThemeDir returns the directory where user themes are stored.
func ThemeDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ok", "themes")
	}
	d := filepath.Join(cfg, "ok", "themes")
	os.MkdirAll(d, 0755) //nolint:errcheck
	return d
}

// SetTheme switches the active theme by name. Returns the resolved theme.
func SetTheme(name string) (Theme, error) {
	switch name {
	case "default":
		activeTheme = DefaultTheme()
	case "light":
		activeTheme = LightTheme()
	case "solarized":
		activeTheme = SolarizedTheme()
	default:
		// Try loading from user themes directory
		path := filepath.Join(ThemeDir(), name+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			return Theme{}, fmt.Errorf("theme %q not found (built-in: default, light, solarized)", name)
		}
		var t Theme
		if err := json.Unmarshal(data, &t); err != nil {
			return Theme{}, fmt.Errorf("theme %q: invalid JSON: %w", name, err)
		}
		t.Name = name
		activeTheme = t
	}
	return activeTheme, nil
}

// ListThemes returns all available theme names (built-in + user).
func ListThemes() []string {
	themes := []string{"default", "light", "solarized"}
	dir := ThemeDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return themes
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			themes = append(themes, e.Name()[:len(e.Name())-5])
		}
	}
	return themes
}

// CurrentTheme returns the active theme.
func CurrentTheme() Theme { return activeTheme }
