// Package cache persists fetched usage snapshots to disk so multiple running
// instances of mm can share the same data without each hitting the
// provider's usage endpoint on its own timer.
//
// The cache lives at ~/.config/mm/usage_cache.json and is keyed by
// "<provider>:<expanded-creds-path>" so renaming an account doesn't lose its
// data and two accounts pointing at the same credentials file share state.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/denislee/mm/internal/accounts"
	"github.com/denislee/mm/internal/quota"
)

// TTL is how long a cached entry is considered fresh enough that a second
// instance can use it instead of re-hitting the provider. Slightly under the
// 5-minute auto-refresh tick so a sibling instance's data is reused even if
// its tick fired moments before this instance's tick.
const TTL = 4 * time.Minute

// Cache is the on-disk shape of usage_cache.json.
type Cache struct {
	Entries map[string]quota.Snapshot `json:"entries"`
}

// Key returns the stable cache key for an account.
func Key(provider, credsPath string) string {
	if provider == "" {
		provider = accounts.ProviderAnthropic
	}
	return provider + ":" + accounts.ExpandHome(credsPath)
}

// Path returns the canonical path to usage_cache.json.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mm", "usage_cache.json"), nil
}

// Load reads usage_cache.json, returning an empty Cache if the file doesn't
// exist yet.
func Load() (Cache, error) {
	path, err := Path()
	if err != nil {
		return Cache{Entries: map[string]quota.Snapshot{}}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cache{Entries: map[string]quota.Snapshot{}}, nil
		}
		return Cache{Entries: map[string]quota.Snapshot{}}, err
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return Cache{Entries: map[string]quota.Snapshot{}}, err
	}
	if c.Entries == nil {
		c.Entries = map[string]quota.Snapshot{}
	}
	return c, nil
}

// Put updates a single entry, performing a read-modify-write under a file
// lock so concurrent writers (in this process or across instances) can't
// clobber each other's untouched accounts. The write itself is also atomic
// via temp-file + rename, so readers never see a torn file.
func Put(key string, snap quota.Snapshot) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	c, _ := Load()
	if c.Entries == nil {
		c.Entries = map[string]quota.Snapshot{}
	}
	c.Entries[key] = snap
	return save(c)
}

// Get returns the snapshot for key, ok=true if present (regardless of age —
// callers decide whether it's fresh enough via Fresh).
func Get(key string) (quota.Snapshot, bool) {
	c, err := Load()
	if err != nil {
		return quota.Snapshot{}, false
	}
	s, ok := c.Entries[key]
	return s, ok
}

// Fresh reports whether a snapshot is recent enough to use in place of a
// network fetch.
func Fresh(s quota.Snapshot) bool {
	return !s.FetchedAt.IsZero() && time.Since(s.FetchedAt) < TTL
}

func save(c Cache) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
