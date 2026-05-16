// Package usage queries the Anthropic Claude Code "/usage" endpoint and
// returns the same quota information shown by Claude Code's /usage slash
// command. The endpoint and field shapes are derived from the bundled CLI;
// they're not part of any documented public API and may change.
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/denislee/mm/internal/quota"
)

// Endpoint is the OAuth-authenticated usage URL.
const Endpoint = "https://api.anthropic.com/api/oauth/usage"

// rawWindow mirrors a single window in the JSON response.
type rawWindow struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// rawSnapshot mirrors the full /usage response.
type rawSnapshot struct {
	FiveHour         rawWindow `json:"five_hour"`
	SevenDay         rawWindow `json:"seven_day"`
	SevenDayOpus     rawWindow `json:"seven_day_opus"`
	SevenDaySonnet   rawWindow `json:"seven_day_sonnet"`
	PurchasesResetAt time.Time `json:"purchases_reset_at"`
}

// credentialsFile mirrors ~/.claude/.credentials.json (only the fields we need).
type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // ms since epoch; treat 0 as unknown
	} `json:"claudeAiOauth"`
}

// Client fetches usage from the Anthropic API on demand.
type Client struct {
	HTTP      *http.Client
	CredsPath string // defaults to ~/.claude/.credentials.json
	UserAgent string
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	home, _ := os.UserHomeDir()
	return &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		CredsPath: filepath.Join(home, ".claude", ".credentials.json"),
		UserAgent: "mm/0.1",
	}
}

// Fetch reads the OAuth token from disk and calls the usage endpoint. The
// token itself is never logged.
func (c *Client) Fetch(ctx context.Context) (quota.Snapshot, error) {
	tok, err := c.readToken()
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("read credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		return quota.Snapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return quota.Snapshot{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Truncate body in case the server returned something huge.
		preview := string(body)
		if len(preview) > 240 {
			preview = preview[:240] + "..."
		}
		return quota.Snapshot{}, fmt.Errorf("usage endpoint: HTTP %d: %s", resp.StatusCode, preview)
	}
	var raw rawSnapshot
	if err := json.Unmarshal(body, &raw); err != nil {
		return quota.Snapshot{}, fmt.Errorf("decode usage response: %w", err)
	}
	return toSnapshot(raw), nil
}

func toSnapshot(raw rawSnapshot) quota.Snapshot {
	cvt := func(w rawWindow) quota.Window {
		return quota.Window{Utilization: w.Utilization, ResetsAt: w.ResetsAt}
	}
	const week = 7 * 24 * time.Hour
	return quota.Snapshot{
		FetchedAt: time.Now(),
		Windows: []quota.NamedWindow{
			{Key: "five_hour", Label: "Current session", Duration: 5 * time.Hour, Window: cvt(raw.FiveHour)},
			{Key: "seven_day", Label: "Current week (all models)", Duration: week, Window: cvt(raw.SevenDay)},
			{Key: "seven_day_sonnet", Label: "Current week (Sonnet only)", Duration: week, Window: cvt(raw.SevenDaySonnet), Hidden: true},
			{Key: "seven_day_opus", Label: "Current week (Opus only)", Duration: week, Window: cvt(raw.SevenDayOpus)},
		},
	}
}

func (c *Client) readToken() (string, error) {
	b, err := os.ReadFile(c.CredsPath)
	if err != nil {
		return "", err
	}
	var f credentialsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return "", err
	}
	tok := f.ClaudeAiOauth.AccessToken
	if tok == "" {
		return "", fmt.Errorf("no accessToken in %s", c.CredsPath)
	}
	// Soft warning only — don't refuse to send. The server is authoritative.
	if f.ClaudeAiOauth.ExpiresAt > 0 {
		exp := time.UnixMilli(f.ClaudeAiOauth.ExpiresAt)
		if time.Now().After(exp) {
			return tok, fmt.Errorf("oauth token expired at %s — run `claude` to refresh", exp.Format(time.RFC3339))
		}
	}
	return tok, nil
}
