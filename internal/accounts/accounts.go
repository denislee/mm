// Package accounts persists the list of Anthropic accounts cc-monitor watches.
// Each account is a (name, credentials-file path) pair; the credentials file
// is the OAuth token blob written by `claude` login.
package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Provider identifies which backend an account targets. Stored as a string
// to keep accounts.json forward-compatible; empty means "anthropic" for
// backward compatibility with configs written before Gemini support landed.
const (
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
)

// Account is one entry in the accounts.json config file.
type Account struct {
	Name      string `json:"name"`
	CredsPath string `json:"creds_path"`
	// Provider is "anthropic" (default) or "gemini".
	Provider string `json:"provider,omitempty"`
	// ProjectID is the GCP project id used for the Gemini quota call. Empty
	// means "auto-resolve via GOOGLE_CLOUD_PROJECT or loadCodeAssist".
	ProjectID string `json:"project_id,omitempty"`
}

// ProviderOrDefault returns the account's provider, defaulting to Anthropic
// for entries written before the field existed.
func (a Account) ProviderOrDefault() string {
	if a.Provider == "" {
		return ProviderAnthropic
	}
	return a.Provider
}

// ExpandHome turns a leading "~/" into the user's home directory. Returns the
// input unchanged if expansion fails or no prefix is present.
func ExpandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// ConfigPath returns the canonical path to accounts.json.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cc-monitor", "accounts.json"), nil
}

// Load reads accounts.json. If the file doesn't exist (or is empty), returns
// a single default entry pointing at ~/.claude/.credentials.json so first-run
// users see something useful.
func Load() ([]Account, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultAccounts(), nil
		}
		return nil, err
	}
	var accs []Account
	if err := json.Unmarshal(b, &accs); err != nil {
		return nil, err
	}
	if len(accs) == 0 {
		return defaultAccounts(), nil
	}
	return accs, nil
}

// Save writes accounts.json, creating its parent directory if needed.
func Save(accs []Account) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(accs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func defaultAccounts() []Account {
	home, _ := os.UserHomeDir()
	return []Account{{
		Name:      "default",
		CredsPath: filepath.Join(home, ".claude", ".credentials.json"),
	}}
}
