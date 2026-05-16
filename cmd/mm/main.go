// Command mm is a small dashboard that shows the Anthropic
// Claude Code usage limits (the same numbers as the `/usage` slash command)
// for one or more accounts. Accounts are configured from inside the UI and
// persisted to ~/.config/mm/accounts.json.
//
// Flags:
//
//	-log <path>  tee log output to a file (in addition to stderr)
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/unit"

	"github.com/denislee/mm/internal/accounts"
	"github.com/denislee/mm/internal/cache"
	"github.com/denislee/mm/internal/gemini"
	"github.com/denislee/mm/internal/quota"
	"github.com/denislee/mm/internal/settings"
	"github.com/denislee/mm/internal/ui"
	"github.com/denislee/mm/internal/usage"
)

func main() {
	logPath := flag.String("log", "", "tee log output to this file (in addition to stderr)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("open log file: %v", err)
		}
		log.SetOutput(io.MultiWriter(os.Stderr, f))
		log.Printf("mm starting; logging to %s", *logPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	accs, err := accounts.Load()
	if err != nil {
		log.Fatalf("load accounts: %v", err)
	}
	prefs, err := settings.Load()
	if err != nil {
		log.Printf("load settings: %v (using defaults)", err)
		prefs = settings.Default()
	}

	w := new(app.Window)
	w.Option(
		app.Title("mm"),
		app.Size(unit.Dp(620), unit.Dp(560)),
	)

	u := ui.New()
	u.ApplySettings(prefs)
	u.Init(accs)

	u.OnSaveSettings = func(s settings.Settings) {
		if err := settings.Save(s); err != nil {
			log.Printf("save settings: %v", err)
		}
		w.Invalidate()
	}

	// fetch hits the provider's usage endpoint, updates the UI, and persists
	// the result to the shared cache so sibling instances can reuse it.
	fetch := func(idx int) {
		credsPath := accounts.ExpandHome(u.CredsPath(idx))
		if credsPath == "" {
			return
		}
		provider := u.Provider(idx)
		projectID := u.ProjectID(idx)
		key := cache.Key(provider, credsPath)
		go func() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			var (
				snap quota.Snapshot
				err  error
			)
			switch provider {
			case accounts.ProviderGemini:
				c := gemini.NewClient()
				c.CredsPath = credsPath
				c.ProjectID = projectID
				snap, err = c.Fetch(ctx)
			default:
				c := usage.NewClient()
				c.CredsPath = credsPath
				snap, err = c.Fetch(ctx)
			}
			if err != nil {
				u.SetQuotaErr(idx, err.Error())
			} else {
				u.SetQuota(idx, snap)
				if cerr := cache.Put(key, snap); cerr != nil {
					log.Printf("save usage cache: %v", cerr)
				}
			}
			w.Invalidate()
		}()
	}

	// fetchIfStale uses the shared on-disk cache when it has a fresh enough
	// snapshot, otherwise falls through to a network fetch. Use this for
	// automatic refreshes; manual refresh button keeps using fetch() directly
	// so the user always gets fresh data when they ask for it.
	fetchIfStale := func(idx int) {
		provider := u.Provider(idx)
		credsPath := accounts.ExpandHome(u.CredsPath(idx))
		if credsPath == "" {
			return
		}
		key := cache.Key(provider, credsPath)
		if snap, ok := cache.Get(key); ok && cache.Fresh(snap) {
			u.SetQuota(idx, snap)
			u.SetQuotaLoading(idx, false)
			w.Invalidate()
			return
		}
		fetch(idx)
	}

	u.OnRefreshAccount = fetch

	u.OnAddAccount = func(a accounts.Account) {
		idx := u.AppendAccount(a)
		if err := persistAccounts(u); err != nil {
			log.Printf("save accounts: %v", err)
		}
		u.SetQuotaLoading(idx, true)
		fetch(idx)
	}

	u.OnUpdateAccount = func(idx int, a accounts.Account) {
		u.UpdateAccountAt(idx, a)
		if err := persistAccounts(u); err != nil {
			log.Printf("save accounts: %v", err)
		}
		u.SetQuotaLoading(idx, true)
		fetch(idx)
	}

	u.OnQuit = func() {
		w.Perform(system.ActionClose)
	}

	u.OnRemoveAccount = func(i int) {
		u.RemoveAccountAt(i)
		if err := persistAccounts(u); err != nil {
			log.Printf("save accounts: %v", err)
		}
		w.Invalidate()
	}

	// Initial load: seed the UI from the shared on-disk cache where we can,
	// otherwise kick off a real fetch. Avoids two instances both hitting the
	// usage endpoint on startup when one of them already has fresh data.
	for i := 0; i < u.AccountCount(); i++ {
		provider := u.Provider(i)
		credsPath := accounts.ExpandHome(u.CredsPath(i))
		key := cache.Key(provider, credsPath)
		if snap, ok := cache.Get(key); ok {
			u.SetQuota(i, snap)
			if !cache.Fresh(snap) {
				u.SetQuotaLoading(i, true)
				fetch(i)
			}
			continue
		}
		u.SetQuotaLoading(i, true)
		fetch(i)
	}

	// Repaint once a second so the "x ago" labels tick.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.Invalidate()
			}
		}
	}()

	// Auto-refresh every account on a 5-minute cadence. The 5-hour Anthropic
	// window shifts ~1.7% per 5 min at full burn, the 7-day windows shift
	// well under 1% — fine-grained enough to track active usage without
	// surprising the providers' undocumented internal endpoints.
	//
	// Goes through fetchIfStale so a sibling instance's recent snapshot
	// satisfies the tick without a duplicate network call.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for i := 0; i < u.AccountCount(); i++ {
					fetchIfStale(i)
				}
			}
		}
	}()

	// Cache-sync poller: every 15s, look for fresher snapshots written by
	// another running instance and adopt them. Cheap (just a small JSON
	// read) and lets multiple instances stay roughly in sync without each
	// hitting the network themselves.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c, err := cache.Load()
				if err != nil {
					continue
				}
				changed := false
				for i := 0; i < u.AccountCount(); i++ {
					key := cache.Key(u.Provider(i), accounts.ExpandHome(u.CredsPath(i)))
					snap, ok := c.Entries[key]
					if !ok {
						continue
					}
					if cur := u.QuotaFetchedAt(i); !cur.IsZero() && !snap.FetchedAt.After(cur) {
						continue
					}
					u.SetQuota(i, snap)
					changed = true
				}
				if changed {
					w.Invalidate()
				}
			}
		}
	}()

	go func() {
		if err := u.Run(w); err != nil {
			log.Printf("ui: %v", err)
		}
		cancel()
		os.Exit(0)
	}()

	app.Main()
}

func persistAccounts(u *ui.UI) error {
	out := make([]accounts.Account, 0, u.AccountCount())
	for i := 0; i < u.AccountCount(); i++ {
		out = append(out, accounts.Account{
			Name:      u.AccountName(i),
			CredsPath: u.CredsPath(i),
			Provider:  u.Provider(i),
			ProjectID: u.ProjectID(i),
		})
	}
	return accounts.Save(out)
}
