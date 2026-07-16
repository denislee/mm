// Command cc-waybar prints one line of Waybar JSON describing the current
// Claude Code usage limits, for use as a Waybar `custom/*` module:
//
//	"custom/claude": {
//	    "exec": "~/.local/bin/cc-waybar -width 16 -max-age 11m",
//	    "return-type": "json",
//	    "interval": 300,
//	    "on-click": "mm"
//	}
//
// With the -max-age above and a `cc-waybar -refresh` systemd --user timer doing
// the actual polling, the bar (and the mm GUI) are pure cache readers that only
// self-fetch if the timer stops writing for longer than -max-age.
//
// The `text` draws a filled bar for the session and weekly (all-models)
// windows out of block glyphs coloured with Pango markup (fill / over-budget /
// ideal-pace marker), a compact pace label (▼N% under / ▲N% over) next to the
// week %, and a muted reset countdown (2h30m) after each window showing how
// long until it resets. The `tooltip` reproduces the full detail (absolute
// reset times, ideal pace, over/under budget). `class` drives a fallback CSS
// colour and `percentage` is the larger of the two windows so Waybar `states`
// also work.
//
// To stay under the endpoint's rate limit, a cached snapshot newer than
// -max-age (default: the cache package TTL) is reused instead of fetching, so
// this command, the mm GUI, and repeated polls/clicks coordinate through one
// shared cache rather than each hitting the network.
//
// On a failed fetch (e.g. the /usage endpoint's HTTP 429 rate limit) the last
// successful snapshot is rendered from mm's shared on-disk cache rather than
// blanking the module; the whole module dims and the tooltip notes it's stale.
// Reset/pace math stays correct off cache because ResetsAt is absolute — only
// the utilisation % is frozen. The -mock and -fail flags exercise the render
// and fallback paths without touching the network.
//
// Waybar renders Pango markup in a custom module's `text` unless `escape:true`
// is set, so the <span> colours below Just Work with the default config.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/denislee/mm/internal/cache"
	"github.com/denislee/mm/internal/quota"
	"github.com/denislee/mm/internal/usage"
)

// Colours mirror mm's dark palette (internal/ui/theme.go).
const (
	colTrack   = "#191c22" // empty track
	colFill    = "#5294ff" // accent / on-pace fill
	colUnder   = "#4cc38a" // green: under budget
	colOver    = "#ef476f" // red: over budget (overshoot segment)
	colNeutral = "#d7dae0" // ideal-pace marker while under / on pace
	colMark    = "#ffe07a" // ideal-pace marker when over budget (amber)
	colWarn    = "#ffc65c" // amber: hot session (no pacing target)
	colMute    = "#8a93a0" // dimmed text when showing stale/cached data
	glyph      = "█"       // full block — every cell, colour-coded, so widths align
)

// out is the Waybar custom-module JSON shape.
type out struct {
	Text       string `json:"text"`
	Tooltip    string `json:"tooltip"`
	Class      string `json:"class,omitempty"`
	Percentage int    `json:"percentage"`
}

func main() {
	width := flag.Int("width", 14, "bar width in cells (per window)")
	maxAge := flag.Duration("max-age", cache.TTL, "reuse cached data newer than this instead of fetching (0 = always fetch)")
	refresh := flag.Bool("refresh", false, "fetch and write the usage cache, then exit (for a scheduled poller); no Waybar output")
	mock := flag.String("mock", "", "render a synthetic snapshot instead of fetching: under|onpace|over|high (testing)")
	fail := flag.Bool("fail", false, "simulate a fetch failure, to test the last-known-state fallback")
	flag.Parse()

	client := usage.NewClient()
	key := cache.Key(client.CredsPath)

	// -refresh: the single scheduled poller (a systemd --user timer). Force a
	// fetch, update the shared cache, and exit without Waybar output. A
	// transient upstream error must not fail the unit — readers fall back to
	// the last known state, so we log and exit 0.
	if *refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		snap, err := client.Fetch(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cc-waybar: refresh failed, keeping cached data: %v\n", err)
			return
		}
		if err := cache.Put(key, snap); err != nil {
			fmt.Fprintf(os.Stderr, "cc-waybar: cache write failed: %v\n", err)
			return
		}
		s, _ := windowByKey(snap, "five_hour")
		w, _ := windowByKey(snap, "seven_day")
		fmt.Printf("cc-waybar: refreshed usage cache (session %.0f%%, week %.0f%%)\n",
			s.Window.Utilization, w.Window.Utilization)
		return
	}

	var (
		snap quota.Snapshot
		err  error
	)
	switch {
	case *mock != "":
		snap = mockSnapshot(*mock)
	case *fail:
		err = errors.New("simulated fetch failure (HTTP 429)")
	default:
		// Tier 2: reuse a fresh cached snapshot (written by us, the mm GUI, or
		// a sibling poll) instead of hitting the network — this is what keeps
		// multiple readers under the rate limit.
		if cached, ok := cache.Get(key); ok && len(cached.Windows) > 0 &&
			!cached.FetchedAt.IsZero() && *maxAge > 0 && time.Since(cached.FetchedAt) < *maxAge {
			render(cached, *width, false, nil)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		snap, err = client.Fetch(ctx)
	}

	if err == nil {
		_ = cache.Put(key, snap) // remember the last known good state
		render(snap, *width, false, nil)
		return
	}

	// Fetch failed (e.g. HTTP 429 rate limit): fall back to the last known
	// state rather than blanking the module.
	if cached, ok := cache.Get(key); ok && len(cached.Windows) > 0 {
		render(cached, *width, true, err)
		return
	}

	// Nothing cached yet — muted placeholder with the reason on hover.
	emit(out{
		Text:    "—",
		Tooltip: "Claude Code usage unavailable\n" + err.Error(),
		Class:   "error",
	})
}

// render builds and emits the Waybar JSON for a snapshot. When stale is set the
// data came from cache after a failed fetch: the whole module is dimmed and the
// tooltip gets a staleness note, but the bars still show the last known values.
func render(snap quota.Snapshot, width int, stale bool, cause error) {
	session, sOK := windowByKey(snap, "five_hour")
	week, wOK := windowByKey(snap, "seven_day")

	var parts []string
	if sOK {
		parts = append(parts, "S "+bar(session.Window, session.Duration, width)+
			fmt.Sprintf(" %.0f%%", session.Window.Utilization)+resetLabel(session.Window))
	}
	if wOK {
		wpart := "W " + bar(week.Window, week.Duration, width) +
			fmt.Sprintf(" %.0f%%", week.Window.Utilization)
		// Compact pace label next to the week %: ▼ under budget, ▲ over.
		if ideal, ok := idealPct(week.Window, week.Duration); ok {
			label, col := "on pace", colNeutral
			switch delta := week.Window.Utilization - ideal; {
			case delta > 1:
				label, col = fmt.Sprintf("▲%.0f%%", delta), colOver
			case delta < -1:
				label, col = fmt.Sprintf("▼%.0f%%", -delta), colUnder
			}
			wpart += fmt.Sprintf("  <span foreground='%s'>%s</span>", col, label)
		}
		wpart += resetLabel(week.Window)
		parts = append(parts, wpart)
	}

	text := "—"
	if len(parts) > 0 {
		text = strings.Join(parts, "  ")
	}
	// A failed fetch renders the last cached snapshot: dim the whole module so
	// stale data reads as visibly different from live (the tooltip explains why).
	// Inner per-cell spans keep their colour, so the bars stay legible.
	if stale {
		text = fmt.Sprintf("<span foreground='%s'>%s</span>", colMute, text)
	}

	tip := tooltip(session, sOK, week, wOK)
	if stale {
		reason := "usage endpoint unavailable"
		if cause != nil && strings.Contains(cause.Error(), "429") {
			reason = "rate-limited"
		}
		tip = fmt.Sprintf("⚠ last known state — %s (updated %s)\n\n", reason, ago(snap.FetchedAt)) + tip
	}

	emit(out{
		Text:       text,
		Tooltip:    tip,
		Class:      class(session, sOK, week, wOK),
		Percentage: int(math.Round(math.Max(utilOf(session, sOK), utilOf(week, wOK)))),
	})
}

// bar renders a filled bar for a usage window as Pango markup: the fill turns
// green while under the ideal pace, splits into a red overshoot segment past
// the ideal point when over, and the ideal-pace marker sits on the track
// (light grey normally, amber when over). Windows with no pace reference are
// coloured by level.
func bar(w quota.Window, dur time.Duration, width int) string {
	if width < 4 {
		width = 4
	}
	util := w.Utilization

	// ideal-pace position (0..100) from how far through the window we are.
	ideal, hasIdeal := 0.0, false
	if w.Active() {
		if remaining := time.Until(w.ResetsAt); remaining > 0 && remaining <= dur {
			ideal = float64(dur-remaining) / float64(dur) * 100
			hasIdeal = true
		}
	}

	fill := clampCell(util, width)
	over := hasIdeal && util-ideal > 1
	idealCell := -1
	marker := colNeutral
	base := colFill
	switch {
	case hasIdeal && util-ideal < -1:
		base = colUnder
	case !hasIdeal:
		base = levelColor(util)
	}
	if hasIdeal {
		idealCell = clampCell(ideal, width)
		if idealCell >= width {
			idealCell = width - 1
		}
		if over {
			marker = colMark
		}
	}

	cells := make([]string, width)
	for i := range cells {
		c := colTrack
		if i < fill {
			c = base
			if over && i >= idealCell {
				c = colOver // overshoot segment
			}
		}
		if hasIdeal && i == idealCell && i >= fill {
			c = marker // ideal marker sitting on the empty track
		}
		cells[i] = c
	}

	// Coalesce runs of one colour into a single span.
	var b strings.Builder
	for i := 0; i < width; {
		j := i
		for j < width && cells[j] == cells[i] {
			j++
		}
		fmt.Fprintf(&b, "<span foreground='%s'>%s</span>", cells[i], strings.Repeat(glyph, j-i))
		i = j
	}
	return b.String()
}

func clampCell(pct float64, width int) int {
	n := int(math.Round(pct / 100 * float64(width)))
	if n < 0 {
		return 0
	}
	if n > width {
		return width
	}
	return n
}

func levelColor(util float64) string {
	switch {
	case util >= 90:
		return colOver
	case util >= 75:
		return colWarn
	default:
		return colFill
	}
}

func emit(o out) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(o) // single line + newline: exactly what Waybar wants
}

func windowByKey(s quota.Snapshot, key string) (quota.NamedWindow, bool) {
	for _, nw := range s.Windows {
		if nw.Key == key {
			return nw, nw.Window.Active()
		}
	}
	return quota.NamedWindow{}, false
}

func utilOf(nw quota.NamedWindow, ok bool) float64 {
	if !ok {
		return 0
	}
	return nw.Window.Utilization
}

// class picks a CSS class from the most pressing signal: a nearly-exhausted
// window is critical; spending faster than the ideal weekly pace is "over"; a
// hot session bar is "warn"; otherwise "ok".
func class(session quota.NamedWindow, sOK bool, week quota.NamedWindow, wOK bool) string {
	if (sOK && session.Window.Utilization >= 90) || (wOK && week.Window.Utilization >= 90) {
		return "critical"
	}
	if wOK {
		if ideal, ok := idealPct(week.Window, week.Duration); ok && week.Window.Utilization-ideal > 1 {
			return "over"
		}
	}
	if sOK && session.Window.Utilization >= 75 {
		return "warn"
	}
	return "ok"
}

func tooltip(session quota.NamedWindow, sOK bool, week quota.NamedWindow, wOK bool) string {
	var b []byte
	if sOK {
		b = append(b, fmt.Sprintf("<b>Current session</b>  %.0f%%\n", session.Window.Utilization)...)
		b = append(b, fmt.Sprintf("  resets %s (in %s)", formatReset(session.Window.ResetsAt), formatUntil(session.Window.ResetsAt))...)
	}
	if wOK {
		if sOK {
			b = append(b, '\n')
		}
		b = append(b, fmt.Sprintf("<b>Current week (all models)</b>  %.0f%%\n", week.Window.Utilization)...)
		if head, detail, ok := pacing(week.Window, week.Duration); ok {
			ideal, _ := idealPct(week.Window, week.Duration)
			b = append(b, fmt.Sprintf("  ideal %.0f%%  ·  %s\n", ideal, head)...)
			if detail != "" {
				b = append(b, "  "+detail+"\n"...)
			}
		}
		b = append(b, fmt.Sprintf("  resets %s (in %s)", formatReset(week.Window.ResetsAt), formatUntil(week.Window.ResetsAt))...)
	}
	if !sOK && !wOK {
		return "No Claude Code usage windows returned"
	}
	return string(b)
}

// --- pacing math, mirrored from internal/ui (kept CLI-local so the UI package
// stays UI-only). ---

func idealPct(w quota.Window, dur time.Duration) (float64, bool) {
	if !w.Active() || dur < 24*time.Hour {
		return 0, false
	}
	remaining := time.Until(w.ResetsAt)
	if remaining <= 0 || remaining > dur {
		return 0, false
	}
	elapsed := dur - remaining
	return float64(elapsed) / float64(dur) * 100, true
}

func pacing(w quota.Window, dur time.Duration) (headline, detail string, ok bool) {
	ideal, valid := idealPct(w, dur)
	if !valid {
		return "", "", false
	}
	remaining := time.Until(w.ResetsAt)
	delta := w.Utilization - ideal

	remainingPct := 100 - w.Utilization
	if remainingPct < 0 {
		remainingPct = 0
	}
	days := remaining.Hours() / 24
	switch {
	case days >= 1:
		detail = fmt.Sprintf("budget %.1f%%/day for %s", remainingPct/days, formatUntil(w.ResetsAt))
	case remaining > 0:
		detail = fmt.Sprintf("budget %.1f%%/hr for %s", remainingPct/remaining.Hours(), formatUntil(w.ResetsAt))
	}

	switch {
	case delta > 1:
		return fmt.Sprintf("%.0f%% over", delta), detail, true
	case delta < -1:
		return fmt.Sprintf("%.0f%% under", -delta), detail, true
	default:
		return "on pace", detail, true
	}
}

func formatReset(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	local := t.Local()
	if time.Until(local) < 24*time.Hour {
		return local.Format("15:04")
	}
	return local.Format("Mon 15:04")
}

// resetLabel renders a compact, muted "time until this window resets" suffix
// for the bar text (e.g. 2h30m), or "" when the window has no reset time.
// ResetsAt is absolute, so this keeps counting down correctly even on a stale
// cached snapshot.
func resetLabel(w quota.Window) string {
	if w.ResetsAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("  <span foreground='%s'>%s</span>", colMute, formatUntilCompact(w.ResetsAt))
}

// formatUntilCompact is formatUntil with the spaces squeezed out ("3h 20m" ->
// "3h20m"), so the inline reset countdown stays terse in the bar text.
func formatUntilCompact(t time.Time) string {
	return strings.ReplaceAll(formatUntil(t), " ", "")
}

func formatUntil(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d / time.Hour)
		m := int(d%time.Hour) / int(time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days := int(d / (24 * time.Hour))
		h := int(d%(24*time.Hour)) / int(time.Hour)
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}

// mockSnapshot builds a synthetic snapshot for -mock testing. Reset times are
// chosen so the ideal-pace lands where the scenario wants it.
func mockSnapshot(scenario string) quota.Snapshot {
	now := time.Now()
	const week = 7 * 24 * time.Hour
	// resetIn returns a reset time such that the window's ideal-pace == target%.
	resetIn := func(dur time.Duration, target float64) time.Time {
		return now.Add(time.Duration(float64(dur) * (1 - target/100)))
	}
	sessUtil, weekUtil, weekIdeal := 8.0, 2.0, 48.0
	switch scenario {
	case "onpace":
		sessUtil, weekUtil, weekIdeal = 48, 47, 48
	case "over":
		sessUtil, weekUtil, weekIdeal = 66, 72, 48
	case "high":
		sessUtil, weekUtil, weekIdeal = 95, 93, 60
	}
	return quota.Snapshot{
		FetchedAt: now,
		Windows: []quota.NamedWindow{
			{Key: "five_hour", Label: "Current session", Duration: 5 * time.Hour,
				Window: quota.Window{Utilization: sessUtil, ResetsAt: resetIn(5*time.Hour, 50)}},
			{Key: "seven_day", Label: "Current week (all models)", Duration: week,
				Window: quota.Window{Utilization: weekUtil, ResetsAt: resetIn(week, weekIdeal)}},
		},
	}
}

// ago renders how long ago t was, for the stale-cache tooltip note.
func ago(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
