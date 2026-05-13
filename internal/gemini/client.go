// Package gemini queries Google's Code Assist quota endpoint — the one the
// Gemini CLI itself hits — and returns the model-tier usage windows shown by
// the CLI's "Model usage" panel (Pro, Flash, Flash-Lite).
//
// This only works for users signed in with `gemini auth login` (Google
// OAuth). API-key and Vertex auth paths don't expose a quota endpoint.
//
// Credentials are read from ~/.gemini/oauth_creds.json (the legacy plain-JSON
// path the CLI writes during migration). The newer encrypted file format and
// OS keychain storage are not yet supported.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dns/cc-monitor/internal/quota"
)

const (
	// quotaEndpoint mirrors the URL the Gemini CLI calls in
	// packages/core/src/code_assist/server.ts.
	quotaEndpoint = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	// loadCodeAssistEndpoint resolves a managed cloudaicompanionProject for
	// free-tier users who don't have GOOGLE_CLOUD_PROJECT set.
	loadCodeAssistEndpoint = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	// tokenEndpoint is Google's standard OAuth2 token URL used for refresh.
	tokenEndpoint = "https://oauth2.googleapis.com/token"

	// Env vars supplying the OAuth installed-app client credentials. Both
	// must be set; the quota endpoint requires a client registered with
	// Google's Code Assist project.
	envClientID     = "CC_MONITOR_GEMINI_CLIENT_ID"
	envClientSecret = "CC_MONITOR_GEMINI_CLIENT_SECRET"
)

// credentialsFile mirrors the layout written by google-auth-library:
// ~/.gemini/oauth_creds.json. ExpiryDate is in ms since epoch.
type credentialsFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiryDate   int64  `json:"expiry_date"`
}

// Client fetches Gemini quota on demand.
type Client struct {
	HTTP      *http.Client
	CredsPath string // defaults to ~/.gemini/oauth_creds.json
	ProjectID string // optional override; resolved via loadCodeAssist if empty
	UserAgent string
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	home, _ := os.UserHomeDir()
	return &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		CredsPath: filepath.Join(home, ".gemini", "oauth_creds.json"),
		UserAgent: "cc-monitor/0.1 (GeminiCLI-compat)",
	}
}

// Fetch loads the OAuth token (refreshing if expired), resolves the project
// id, calls retrieveUserQuota, and converts the response to a Snapshot.
func (c *Client) Fetch(ctx context.Context) (quota.Snapshot, error) {
	creds, err := c.loadCreds()
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("read credentials: %w", err)
	}
	tok, err := c.ensureToken(ctx, creds)
	if err != nil {
		return quota.Snapshot{}, err
	}
	proj := c.ProjectID
	if proj == "" {
		proj = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	}
	if proj == "" {
		proj, err = c.resolveProject(ctx, tok)
		if err != nil {
			return quota.Snapshot{}, fmt.Errorf("resolve project id: %w", err)
		}
	}

	body, err := c.callQuota(ctx, tok, proj)
	if err != nil {
		return quota.Snapshot{}, err
	}
	return decodeQuota(body), nil
}

// retrieveQuotaResponse mirrors the relevant fields of the JSON response.
type retrieveQuotaResponse struct {
	Buckets []bucket `json:"buckets"`
}

type bucket struct {
	ModelID            string    `json:"modelId"`
	RemainingAmount    string    `json:"remainingAmount"`
	RemainingFraction  *float64  `json:"remainingFraction"`
	ResetTime          time.Time `json:"resetTime"`
	TokenType          string    `json:"tokenType"`
}

func decodeQuota(body []byte) quota.Snapshot {
	var raw retrieveQuotaResponse
	_ = json.Unmarshal(body, &raw)

	// Group buckets by tier; the CLI does the same and picks the tightest
	// (lowest remainingFraction) bucket per tier so the bar reflects the
	// binding constraint.
	type tier struct {
		label   string
		key     string
		modelID string
		best    bucket
		seen    bool
	}
	tiers := map[string]*tier{
		"pro":        {label: "Pro", key: "gemini_pro"},
		"flash":      {label: "Flash", key: "gemini_flash"},
		"flash-lite": {label: "Flash Lite", key: "gemini_flash_lite"},
	}
	classify := func(modelID string) string {
		m := strings.ToLower(modelID)
		switch {
		case strings.Contains(m, "flash-lite"), strings.Contains(m, "flash_lite"):
			return "flash-lite"
		case strings.Contains(m, "flash"):
			return "flash"
		case strings.Contains(m, "pro"):
			return "pro"
		default:
			return ""
		}
	}
	for _, b := range raw.Buckets {
		key := classify(b.ModelID)
		t, ok := tiers[key]
		if !ok {
			continue
		}
		if !t.seen || (b.RemainingFraction != nil && t.best.RemainingFraction != nil && *b.RemainingFraction < *t.best.RemainingFraction) {
			t.best = b
			t.modelID = b.ModelID
			t.seen = true
		}
	}

	// Stable display order: Pro, Flash, Flash Lite.
	order := []string{"pro", "flash", "flash-lite"}
	wins := make([]quota.NamedWindow, 0, len(order))
	for _, k := range order {
		t := tiers[k]
		if !t.seen {
			continue
		}
		util := 0.0
		if t.best.RemainingFraction != nil {
			util = (1.0 - *t.best.RemainingFraction) * 100
		}
		// The server reports resetTime per bucket; cc-monitor's pacing math
		// needs a total window duration. Use the time until reset as a best
		// guess — for daily windows that's accurate the moment after a
		// reset and a slight under-estimate later in the day, which is
		// close enough for a pacing indicator.
		dur := time.Until(t.best.ResetTime)
		if dur < time.Hour {
			dur = time.Hour
		}
		wins = append(wins, quota.NamedWindow{
			Key:      t.key,
			Label:    t.label,
			Duration: dur,
			Window: quota.Window{
				Utilization: util,
				ResetsAt:    t.best.ResetTime,
			},
		})
	}
	// Keep a deterministic order even if the map iteration was used elsewhere.
	sort.SliceStable(wins, func(i, j int) bool {
		return indexIn(order, tierKey(wins[i].Key)) < indexIn(order, tierKey(wins[j].Key))
	})

	return quota.Snapshot{FetchedAt: time.Now(), Windows: wins}
}

func tierKey(windowKey string) string {
	switch windowKey {
	case "gemini_pro":
		return "pro"
	case "gemini_flash":
		return "flash"
	case "gemini_flash_lite":
		return "flash-lite"
	}
	return ""
}

func indexIn(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return len(xs)
}

func (c *Client) callQuota(ctx context.Context, tok, proj string) ([]byte, error) {
	payload, _ := json.Marshal(map[string]string{"project": proj})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, quotaEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 240 {
			preview = preview[:240] + "..."
		}
		return nil, fmt.Errorf("retrieveUserQuota: HTTP %d: %s", resp.StatusCode, preview)
	}
	return body, nil
}

// resolveProject calls loadCodeAssist with no project to get the managed
// cloudaicompanionProject assigned to free-tier accounts.
func (c *Client) resolveProject(ctx context.Context, tok string) (string, error) {
	// The CLI sends a metadata block describing platform; for the bare
	// project-resolution call we send the minimum that lets the server
	// echo back a managed project id for free-tier users.
	payload, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"pluginType": "GEMINI",
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loadCodeAssistEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 240 {
			preview = preview[:240] + "..."
		}
		return "", fmt.Errorf("loadCodeAssist: HTTP %d: %s — set GOOGLE_CLOUD_PROJECT or configure the account's project id", resp.StatusCode, preview)
	}
	var out struct {
		CloudaicompanionProject string `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode loadCodeAssist: %w", err)
	}
	if out.CloudaicompanionProject == "" {
		return "", fmt.Errorf("no cloudaicompanionProject in loadCodeAssist response — set GOOGLE_CLOUD_PROJECT")
	}
	return out.CloudaicompanionProject, nil
}

func (c *Client) loadCreds() (credentialsFile, error) {
	var f credentialsFile
	b, err := os.ReadFile(c.CredsPath)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse %s: %w", c.CredsPath, err)
	}
	if f.AccessToken == "" && f.RefreshToken == "" {
		return f, fmt.Errorf("no access_token or refresh_token in %s — run `gemini auth login`", c.CredsPath)
	}
	return f, nil
}

// ensureToken returns a valid access token, refreshing via the OAuth2
// endpoint if the cached one is expired or missing. The refreshed token is
// kept only in memory; the credentials file is not written back.
func (c *Client) ensureToken(ctx context.Context, creds credentialsFile) (string, error) {
	const skew = 60 * time.Second
	if creds.AccessToken != "" && creds.ExpiryDate > 0 {
		exp := time.UnixMilli(creds.ExpiryDate)
		if time.Now().Add(skew).Before(exp) {
			return creds.AccessToken, nil
		}
	}
	if creds.RefreshToken == "" {
		if creds.AccessToken != "" {
			// No refresh token — try the cached access token; the server
			// will reject it if it's stale.
			return creds.AccessToken, nil
		}
		return "", fmt.Errorf("no refresh_token; run `gemini auth login`")
	}
	return c.refresh(ctx, creds.RefreshToken)
}

func (c *Client) refresh(ctx context.Context, refreshToken string) (string, error) {
	clientID := strings.TrimSpace(os.Getenv(envClientID))
	clientSecret := strings.TrimSpace(os.Getenv(envClientSecret))
	if clientID == "" || clientSecret == "" {
		return "", fmt.Errorf("%s and %s must be set", envClientID, envClientSecret)
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 240 {
			preview = preview[:240] + "..."
		}
		return "", fmt.Errorf("token refresh: HTTP %d: %s", resp.StatusCode, preview)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode token refresh: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token refresh returned empty access_token")
	}
	return out.AccessToken, nil
}
