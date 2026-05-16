// Package settings persists user-tweakable UI options for mm.
// Currently this covers the font family (path to a .ttf/.otf file, or empty
// to use the bundled Go font) and a scale multiplier applied to every text
// size in the interface.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Settings is the on-disk shape of ~/.config/mm/settings.json.
type Settings struct {
	// FontPath is an absolute (or ~/-prefixed) path to a TrueType or OpenType
	// font file. Empty means "use the bundled Go font".
	FontPath string `json:"font_path"`
	// FontScale multiplies every Sp text size in the UI. 1.0 is the default.
	FontScale float64 `json:"font_scale"`
	// Theme is one of "dark", "light", "linear". Empty falls back to "dark".
	Theme string `json:"theme"`
	// Layout is the card arrangement: "vertical" (default) stacks cards top
	// to bottom; "horizontal" lays them out side by side in a scrollable row.
	Layout string `json:"layout"`
}

// Layout values for the Layout field.
const (
	LayoutVertical   = "vertical"
	LayoutHorizontal = "horizontal"
)

// Default returns sensible defaults: bundled font, no scaling, dark theme,
// vertical card layout.
func Default() Settings {
	return Settings{FontPath: "", FontScale: 1.0, Theme: "dark", Layout: LayoutVertical}
}

// ConfigPath returns the canonical path to settings.json.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mm", "settings.json"), nil
}

// Load reads settings.json, returning defaults if it doesn't exist.
func Load() (Settings, error) {
	path, err := ConfigPath()
	if err != nil {
		return Settings{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Settings{}, err
	}
	s := Default()
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	if s.FontScale <= 0 {
		s.FontScale = 1.0
	}
	if s.Theme == "" {
		s.Theme = "dark"
	}
	return s, nil
}

// Save writes settings.json, creating its parent directory if needed.
func Save(s Settings) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
