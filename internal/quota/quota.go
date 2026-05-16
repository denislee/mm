// Package quota defines the provider-agnostic data model the mm UI
// consumes. Each backend (Anthropic /usage, Gemini retrieveUserQuota, ...)
// converts its native response into a Snapshot so the rendering code can
// treat them uniformly.
package quota

import "time"

// Window is a single rate-limit bucket: how much of it has been consumed and
// when it resets.
type Window struct {
	Utilization float64   // 0..100
	ResetsAt    time.Time
}

// Active reports whether the provider actually returned this window. Inactive
// (zero-value) windows are skipped by the UI.
func (w Window) Active() bool {
	return !w.ResetsAt.IsZero()
}

// NamedWindow attaches a stable key, human-readable label, and total duration
// to a Window so the UI can render pacing math and a sortable list.
type NamedWindow struct {
	Key      string        // stable id, e.g. "five_hour", "gemini_pro"
	Label    string        // user-visible label
	Duration time.Duration // total window length, used for pacing
	Window   Window
	// Hidden hints the UI to keep this row collapsed behind the
	// "show extra rows" toggle. Used for Anthropic's per-model breakdowns.
	Hidden bool
}

// Snapshot is a provider's full response, normalised into a slice of windows
// plus a wall-clock fetch time used for the "updated N ago" label.
type Snapshot struct {
	Windows   []NamedWindow
	FetchedAt time.Time
}
