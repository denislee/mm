// Package ui renders the mm Gio interface: a scrollable list of
// Anthropic accounts, each showing the Claude Code usage limits returned by
// the /usage endpoint, plus an inline form to add new accounts.
package ui

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"github.com/denislee/mm/internal/accounts"
	"github.com/denislee/mm/internal/fonts"
	"github.com/denislee/mm/internal/quota"
	"github.com/denislee/mm/internal/settings"
)

// Material design icons, decoded once at init.
var (
	iconRefresh  = mustIcon(icons.NavigationRefresh)
	iconEdit     = mustIcon(icons.ImageEdit)
	iconRemove   = mustIcon(icons.ActionDelete)
	iconAdd      = mustIcon(icons.ContentAdd)
	iconSave     = mustIcon(icons.NavigationCheck)
	iconCancel   = mustIcon(icons.NavigationClose)
	iconShow     = mustIcon(icons.ActionVisibility)
	iconHide     = mustIcon(icons.ActionVisibilityOff)
	iconSettings = mustIcon(icons.ActionSettings)
	iconMinus    = mustIcon(icons.ContentRemove)
	iconDropdown = mustIcon(icons.NavigationArrowDropDown)
	iconDropup   = mustIcon(icons.NavigationArrowDropUp)
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

// accountState bundles persisted account info with its per-row widget state.
type accountState struct {
	Name      string
	CredsPath string

	refreshBtn widget.Clickable
	editBtn    widget.Clickable
	removeBtn  widget.Clickable
	copyErrBtn widget.Clickable
	hover      gesture.Hover

	quota        quota.Snapshot
	quotaErr     string
	quotaLoading bool
	copiedAt     time.Time
}

// UI is the long-lived UI struct holding widget state.
type UI struct {
	Theme *material.Theme

	list     widget.List
	accounts []accountState

	// Add/edit form state. formOpen toggles visibility; editIdx is -1 for
	// "add new" and otherwise the index of the account being edited.
	addBtn         widget.Clickable
	saveBtn        widget.Clickable
	cancelBtn      widget.Clickable
	nameEdit       widget.Editor
	pathEdit       widget.Editor
	formOpen       bool
	editIdx        int
	formErr        string

	// Sonnet-only quota row is hidden by default; toggled via header button.
	sonnetToggleBtn widget.Clickable
	showSonnet      bool

	// Settings form state. settingsOpen toggles its visibility; it shares
	// space with the add/edit account form (only one is open at a time).
	settingsBtn       widget.Clickable
	settingsSaveBtn   widget.Clickable
	settingsCancelBtn widget.Clickable
	fontScaleEdit     widget.Editor
	fontScaleIncBtn   widget.Clickable
	fontScaleDecBtn   widget.Clickable
	fontPickerBtn     widget.Clickable
	fontPickerOpen    bool
	fontList          []fonts.Font
	fontOptionBtns    []widget.Clickable
	fontPickedPath    string
	themePickerBtn    widget.Clickable
	themePickerOpen   bool
	themePickedName   string
	themeOptionBtns   [3]widget.Clickable
	layoutPickerBtn   widget.Clickable
	layoutPickerOpen  bool
	layoutPickedName  string
	layoutOptionBtns  [2]widget.Clickable
	settingsOpen      bool
	settingsErr       string
	settings          settings.Settings
	// settingsBeforeEdit holds the values active when the form opened so a
	// Cancel click can revert the live-preview edits the user made.
	settingsBeforeEdit settings.Settings
	// lastScaleText is the scale editor text we last applied, used to detect
	// direct keyboard edits and apply them live.
	lastScaleText string

	// Callbacks invoked from the UI thread. Implementations should be quick
	// (kick off fetches in goroutines, persist synchronously is fine).
	OnRefreshAccount func(idx int)
	OnAddAccount     func(a accounts.Account)
	OnUpdateAccount  func(idx int, a accounts.Account)
	OnRemoveAccount  func(idx int)
	OnSaveSettings   func(s settings.Settings)
	OnQuit           func()
}

// New returns a UI ready to use.
func New() *UI {
	u := &UI{Theme: material.NewTheme(), settings: settings.Default()}
	u.applyListAxis()
	u.nameEdit.SingleLine = true
	u.pathEdit.SingleLine = true
	u.fontScaleEdit.SingleLine = true
	return u
}

// applyListAxis syncs the scrollable list orientation with the current
// layout setting.
func (u *UI) applyListAxis() {
	if u.settings.Layout == settings.LayoutHorizontal {
		u.list.Axis = layout.Horizontal
	} else {
		u.list.Axis = layout.Vertical
	}
}

// ApplySettings installs UI settings: applies the font scale to the theme,
// loads the custom font file (if any), and updates the shaper. Safe to call
// from the UI goroutine before rendering or after the user edits settings.
func (u *UI) ApplySettings(s settings.Settings) {
	if s.FontScale <= 0 {
		s.FontScale = 1.0
	}
	if s.Theme == "" {
		s.Theme = "dark"
	}
	if s.Layout != settings.LayoutHorizontal {
		s.Layout = settings.LayoutVertical
	}
	applyPalette(s.Theme)
	u.settings = s
	u.applyListAxis()
	// Material's Theme.TextSize is the base size used by labels that don't
	// pass an explicit size. Our labels all pass explicit sp values, but we
	// still keep TextSize in sync so anything we miss scales too.
	u.Theme.TextSize = unit.Sp(float32(16) * float32(s.FontScale))
	if path := accounts.ExpandHome(s.FontPath); path != "" {
		if face, err := loadFontFace(path); err == nil {
			u.Theme.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection([]font.FontFace{{Font: font.Font{}, Face: face}}))
		}
	}
}

func loadFontFace(path string) (font.Face, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return opentype.Parse(b)
}

// sp returns the given size in Sp, multiplied by the configured font scale.
func (u *UI) sp(n float32) unit.Sp {
	scale := float32(u.settings.FontScale)
	if scale <= 0 {
		scale = 1
	}
	return unit.Sp(n * scale)
}

// Init populates the UI with the initial account list. Call once at startup.
func (u *UI) Init(accs []accounts.Account) {
	u.accounts = u.accounts[:0]
	for _, a := range accs {
		u.accounts = append(u.accounts, accountState{
			Name:      a.Name,
			CredsPath: a.CredsPath,
		})
	}
}

// AppendAccount adds an account and returns its index.
func (u *UI) AppendAccount(a accounts.Account) int {
	u.accounts = append(u.accounts, accountState{
		Name:      a.Name,
		CredsPath: a.CredsPath,
	})
	return len(u.accounts) - 1
}

// RemoveAccountAt drops the account at i.
func (u *UI) RemoveAccountAt(i int) {
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.accounts = append(u.accounts[:i], u.accounts[i+1:]...)
}

// UpdateAccountAt replaces the name/path of the account at i.
// Per-row widget state and quota snapshot are preserved.
func (u *UI) UpdateAccountAt(i int, a accounts.Account) {
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.accounts[i].Name = a.Name
	u.accounts[i].CredsPath = a.CredsPath
}

// CredsPath returns the credentials path for the account at i (empty if oob).
func (u *UI) CredsPath(i int) string {
	if i < 0 || i >= len(u.accounts) {
		return ""
	}
	return u.accounts[i].CredsPath
}

// AccountName returns the display name for the account at i (empty if oob).
func (u *UI) AccountName(i int) string {
	if i < 0 || i >= len(u.accounts) {
		return ""
	}
	return u.accounts[i].Name
}

// AccountCount returns the number of configured accounts.
func (u *UI) AccountCount() int { return len(u.accounts) }

// QuotaFetchedAt returns the timestamp on the snapshot currently displayed
// for account i, or the zero time if no snapshot has been installed.
func (u *UI) QuotaFetchedAt(i int) time.Time {
	if i < 0 || i >= len(u.accounts) {
		return time.Time{}
	}
	return u.accounts[i].quota.FetchedAt
}

// SetQuota installs a snapshot for account i. Call from any goroutine.
func (u *UI) SetQuota(i int, s quota.Snapshot) {
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.accounts[i].quota = s
	u.accounts[i].quotaErr = ""
	u.accounts[i].quotaLoading = false
}

// SetQuotaErr records a failed fetch for account i.
func (u *UI) SetQuotaErr(i int, msg string) {
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.accounts[i].quotaErr = msg
	u.accounts[i].quotaLoading = false
}

// SetQuotaLoading toggles the spinner state for account i.
func (u *UI) SetQuotaLoading(i int, b bool) {
	if i < 0 || i >= len(u.accounts) {
		return
	}
	u.accounts[i].quotaLoading = b
}

// Run is the main event loop. It blocks until the window is closed.
func (u *UI) Run(w *app.Window) error {
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// Live palette vars — mutated by applyPalette (see theme.go). Initialized to
// the dark variant so packages importing ui get sensible defaults before
// ApplySettings runs.
var (
	bg         = darkPalette().Bg
	panelBg    = darkPalette().PanelBg
	cardBg     = darkPalette().CardBg
	barBg      = darkPalette().BarBg
	tickCol    = darkPalette().TickCol
	accent     = darkPalette().Accent
	good       = darkPalette().Good
	danger     = darkPalette().Danger
	mute       = darkPalette().Mute
	neutral    = darkPalette().Neutral
	white      = darkPalette().White
	overMarker = darkPalette().OverMarker
)

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{
		R: uint8(hex >> 16),
		G: uint8(hex >> 8),
		B: uint8(hex),
		A: 0xff,
	}
}

func (u *UI) layout(gtx layout.Context) layout.Dimensions {
	fillRect(gtx.Ops, gtx.Constraints.Max, bg)

	// Register the UI as a key event target and drain shortcuts. Shortcuts are
	// suppressed while the add/edit form is open so users can type "q" and "r"
	// into the form fields — and we only claim focus when the form is closed
	// so the form's text editors can receive keystrokes.
	event.Op(gtx.Ops, u)
	if !u.formOpen && !gtx.Focused(u) {
		gtx.Execute(key.FocusCmd{Tag: u})
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: u},
			key.Filter{Focus: u, Name: "R"},
			key.Filter{Focus: u, Name: "Q"},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press || u.formOpen {
			continue
		}
		switch ke.Name {
		case "R":
			if u.OnRefreshAccount != nil {
				for i := range u.accounts {
					u.accounts[i].quotaLoading = true
					u.OnRefreshAccount(i)
				}
			}
		case "Q":
			if u.OnQuit != nil {
				u.OnQuit()
			}
		}
	}

	// Drain per-account button clicks before drawing.
	for i := range u.accounts {
		if u.accounts[i].refreshBtn.Clicked(gtx) && u.OnRefreshAccount != nil {
			u.accounts[i].quotaLoading = true
			u.OnRefreshAccount(i)
		}
		if u.accounts[i].removeBtn.Clicked(gtx) && u.OnRemoveAccount != nil {
			u.OnRemoveAccount(i)
			// Indices shift; bail out of this frame's click pass.
			break
		}
		if u.accounts[i].copyErrBtn.Clicked(gtx) && u.accounts[i].quotaErr != "" {
			gtx.Execute(clipboard.WriteCmd{
				Type: "application/text",
				Data: io.NopCloser(strings.NewReader(u.accounts[i].quotaErr)),
			})
			u.accounts[i].copiedAt = time.Now()
		}
	}

	if u.sonnetToggleBtn.Clicked(gtx) {
		u.showSonnet = !u.showSonnet
	}

	if u.settingsBtn.Clicked(gtx) {
		u.openSettings()
	}
	if u.settingsCancelBtn.Clicked(gtx) {
		u.cancelSettings()
	}
	if u.settingsSaveBtn.Clicked(gtx) {
		u.submitSettings()
	}
	if u.settingsOpen {
		if u.fontPickerBtn.Clicked(gtx) {
			u.fontPickerOpen = !u.fontPickerOpen
		}
		for i := range u.fontOptionBtns {
			if u.fontOptionBtns[i].Clicked(gtx) {
				u.fontPickedPath = u.fontList[i].Path
				u.fontPickerOpen = false
				u.applyLive()
			}
		}
		if u.themePickerBtn.Clicked(gtx) {
			u.themePickerOpen = !u.themePickerOpen
		}
		themeNames := themeOptions()
		for i := range u.themeOptionBtns {
			if u.themeOptionBtns[i].Clicked(gtx) {
				u.themePickedName = themeNames[i]
				u.themePickerOpen = false
				u.applyLive()
			}
		}
		if u.layoutPickerBtn.Clicked(gtx) {
			u.layoutPickerOpen = !u.layoutPickerOpen
		}
		layoutNames := layoutOptions()
		for i := range u.layoutOptionBtns {
			if u.layoutOptionBtns[i].Clicked(gtx) {
				u.layoutPickedName = layoutNames[i]
				u.layoutPickerOpen = false
				u.applyLive()
			}
		}
		scaleChanged := false
		if u.fontScaleDecBtn.Clicked(gtx) {
			u.stepScale(-0.1)
			scaleChanged = true
		}
		if u.fontScaleIncBtn.Clicked(gtx) {
			u.stepScale(+0.1)
			scaleChanged = true
		}
		if cur := u.fontScaleEdit.Text(); cur != u.lastScaleText {
			u.lastScaleText = cur
			scaleChanged = true
		}
		if scaleChanged {
			u.applyLive()
		}
	}

	// Add/edit form: open via "+ Add account", per-account "edit", close via
	// cancel, submit via save.
	if u.addBtn.Clicked(gtx) {
		u.openForm(-1, accounts.Account{CredsPath: defaultCredsPath})
	}
	for i := range u.accounts {
		if u.accounts[i].editBtn.Clicked(gtx) {
			u.openForm(i, accounts.Account{
				Name:      u.accounts[i].Name,
				CredsPath: u.accounts[i].CredsPath,
			})
		}
	}
	if u.cancelBtn.Clicked(gtx) {
		u.formOpen = false
		u.formErr = ""
	}
	if u.saveBtn.Clicked(gtx) {
		name := strings.TrimSpace(u.nameEdit.Text())
		path := strings.TrimSpace(u.pathEdit.Text())
		acc := accounts.Account{Name: name, CredsPath: path}
		switch {
		case name == "":
			u.formErr = "name is required"
		case path == "":
			u.formErr = "credentials path is required"
		case u.editIdx < 0:
			if u.OnAddAccount != nil {
				u.OnAddAccount(acc)
			}
			u.formOpen = false
			u.formErr = ""
		default:
			if u.OnUpdateAccount != nil {
				u.OnUpdateAccount(u.editIdx, acc)
			}
			u.formOpen = false
			u.formErr = ""
		}
	}

	return panel(gtx, panelBg, unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
		sonnetIcon, sonnetDesc := iconShow, "show sonnet quota"
		sonnetCol := mute
		if u.showSonnet {
			sonnetIcon, sonnetDesc = iconHide, "hide sonnet quota"
			sonnetCol = accent
		}
		horizontal := u.settings.Layout == settings.LayoutHorizontal
		// Vertical layout reuses a trailing list slot for the form/settings
		// panel; horizontal layout shows them below the scrollable row of
		// cards so they don't get cramped into a 320dp column.
		extraRows := 0
		if !horizontal && (u.formOpen || u.settingsOpen) {
			extraRows = 1
		}
		itemCount := len(u.accounts) + extraRows
		header := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(u.bigLabel("Model usage", accent)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(u.iconBtn(&u.sonnetToggleBtn, sonnetIcon, sonnetCol, sonnetDesc)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
						layout.Rigid(u.iconBtn(&u.settingsBtn, iconSettings, neutral, "settings")),
						layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
						layout.Rigid(u.iconBtn(&u.addBtn, iconAdd, accent, "add account")),
					)
				}),
			)
		})
		listChild := layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			// Compute a per-card width that divides the list's available
			// horizontal space evenly across the accounts, falling back to a
			// minimum so the row scrolls when there are too many to fit.
			gapPx := gtx.Dp(unit.Dp(8))
			minCardW := gtx.Dp(unit.Dp(280))
			cardW := gtx.Dp(unit.Dp(320))
			if horizontal && len(u.accounts) > 0 {
				avail := gtx.Constraints.Max.X
				n := len(u.accounts)
				cardW = (avail - gapPx*(n-1)) / n
				if cardW < minCardW {
					cardW = minCardW
				}
			}
			return material.List(u.Theme, &u.list).Layout(gtx, itemCount, func(gtx layout.Context, i int) layout.Dimensions {
				if i < len(u.accounts) {
					if horizontal {
						isLast := i == len(u.accounts)-1
						rightGap := unit.Dp(8)
						if isLast {
							rightGap = 0
						}
						return layout.Inset{Right: rightGap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							w := cardW
							if w > gtx.Constraints.Max.X && gtx.Constraints.Max.X > 0 {
								w = gtx.Constraints.Max.X
							}
							gtx.Constraints.Min.X = w
							gtx.Constraints.Max.X = w
							// The list propagates a tight cross-axis (Y)
							// constraint from its Flexed slot. Drop the min so
							// the card uses its natural content height; the
							// list's Alignment=Start tops it in the row.
							gtx.Constraints.Min.Y = 0
							return u.accountCard(gtx, i)
						})
					}
					return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return u.accountCard(gtx, i)
					})
				}
				if u.settingsOpen {
					return u.settingsRow(gtx)
				}
				return u.formRow(gtx)
			})
		})
		children := []layout.FlexChild{header, layout.Rigid(spacer(unit.Dp(8))), listChild}
		if horizontal && (u.formOpen || u.settingsOpen) {
			children = append(children, layout.Rigid(spacer(unit.Dp(8))))
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.settingsOpen {
					return u.settingsRow(gtx)
				}
				return u.formRow(gtx)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func (u *UI) accountCard(gtx layout.Context, idx int) layout.Dimensions {
	a := &u.accounts[idx]
	hovered := a.hover.Update(gtx.Source)

	// Record the card layout, then scope a hover-tracking clip area around
	// its full bounds so per-card hover state covers the whole panel.
	macro := op.Record(gtx.Ops)
	dims := panel(gtx, cardBg, unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
		header := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(u.label(a.Name, neutral, u.sp(15))),
					layout.Rigid(u.label(a.CredsPath, mute, u.sp(10))),
					layout.Rigid(u.label(refreshHint(a.quotaLoading, a.quota.FetchedAt), mute, u.sp(10))),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !hovered {
					return layout.Dimensions{}
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.iconBtn(&a.refreshBtn, iconRefresh, accent, "refresh (r)")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
					layout.Rigid(u.iconBtn(&a.editBtn, iconEdit, neutral, "edit")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
					layout.Rigid(u.iconBtn(&a.removeBtn, iconRemove, danger, "remove")),
				)
			}),
		)

		rows := make([]quota.NamedWindow, 0, len(a.quota.Windows))
		for _, nw := range a.quota.Windows {
			if nw.Hidden && !u.showSonnet {
				continue
			}
			if !nw.Window.Active() && nw.Window.Utilization == 0 {
				continue
			}
			rows = append(rows, nw)
		}

		children := []layout.FlexChild{
			layout.Rigid(func(layout.Context) layout.Dimensions { return header }),
			layout.Rigid(spacer(unit.Dp(6))),
		}

		if a.quotaErr != "" {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return u.errorRow(gtx, idx, a.quotaErr)
			}))
			if len(rows) > 0 {
				children = append(children, layout.Rigid(spacer(unit.Dp(6))))
			}
		}
		switch {
		case len(rows) > 0:
			for _, r := range rows {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.quotaRow(gtx, r.Label, r.Window, r.Duration)
				}))
				children = append(children, layout.Rigid(spacer(unit.Dp(4))))
			}
		case a.quotaErr == "":
			children = append(children, layout.Rigid(u.label("click refresh to load quota usage", mute, u.sp(12))))
		}

		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	call := macro.Stop()

	// Register hover detection over the whole card area, then replay the
	// recorded card content on top.
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	a.hover.Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}

// defaultCredsPath is the suggested credentials-file path pre-filled in the
// add-account form.
const defaultCredsPath = "~/.claude/.credentials.json"

// openForm shows the add/edit form. editIdx < 0 means "add new"; otherwise the
// form is bound to that account row. Pass the initial Account values to seed
// the editors.
func (u *UI) openForm(editIdx int, a accounts.Account) {
	u.formOpen = true
	u.settingsOpen = false
	u.editIdx = editIdx
	u.formErr = ""
	u.nameEdit.SetText(a.Name)
	path := a.CredsPath
	if path == "" {
		path = defaultCredsPath
	}
	u.pathEdit.SetText(path)
}

// openSettings shows the settings form, seeded with the current values.
func (u *UI) openSettings() {
	u.settingsOpen = true
	u.formOpen = false
	u.settingsErr = ""
	u.fontPickerOpen = false
	u.fontList = fonts.CatalogIncluding(u.settings.FontPath)
	u.fontOptionBtns = make([]widget.Clickable, len(u.fontList))
	u.fontPickedPath = u.settings.FontPath
	u.settingsBeforeEdit = u.settings
	u.themePickedName = u.settings.Theme
	if u.themePickedName == "" {
		u.themePickedName = "dark"
	}
	u.themePickerOpen = false
	u.layoutPickedName = u.settings.Layout
	if u.layoutPickedName == "" {
		u.layoutPickedName = settings.LayoutVertical
	}
	u.layoutPickerOpen = false
	scaleText := formatScale(u.settings.FontScale)
	u.fontScaleEdit.SetText(scaleText)
	u.lastScaleText = scaleText
}

// cancelSettings closes the form and reverts any live-preview edits.
func (u *UI) cancelSettings() {
	if u.settingsOpen {
		u.ApplySettings(u.settingsBeforeEdit)
	}
	u.settingsOpen = false
	u.settingsErr = ""
}

// applyLive previews the in-form font and scale without persisting to disk.
// Path errors are surfaced inline; scale errors leave the live state alone.
func (u *UI) applyLive() {
	scale, err := strconv.ParseFloat(strings.TrimSpace(u.fontScaleEdit.Text()), 64)
	if err != nil || scale <= 0 {
		scale = u.settings.FontScale
	}
	path := u.fontPickedPath
	if path != "" {
		if _, err := loadFontFace(accounts.ExpandHome(path)); err != nil {
			u.settingsErr = "cannot load font: " + err.Error()
			return
		}
	}
	u.settingsErr = ""
	u.ApplySettings(settings.Settings{FontPath: path, FontScale: scale, Theme: u.themePickedName, Layout: u.layoutPickedName})
}

// submitSettings validates the settings form and, if valid, applies and
// persists the new values.
func (u *UI) submitSettings() {
	scaleStr := strings.TrimSpace(u.fontScaleEdit.Text())
	if scaleStr == "" {
		scaleStr = "1"
	}
	scale, err := strconv.ParseFloat(scaleStr, 64)
	if err != nil || scale <= 0 {
		u.settingsErr = "font scale must be a positive number (e.g. 1.0, 1.25)"
		return
	}
	path := u.fontPickedPath
	if path != "" {
		if _, err := loadFontFace(accounts.ExpandHome(path)); err != nil {
			u.settingsErr = "cannot load font: " + err.Error()
			return
		}
	}
	s := settings.Settings{FontPath: path, FontScale: scale, Theme: u.themePickedName, Layout: u.layoutPickedName}
	u.ApplySettings(s)
	if u.OnSaveSettings != nil {
		u.OnSaveSettings(s)
	}
	u.settingsOpen = false
	u.settingsErr = ""
}

// stepScale parses the current scale editor value, applies delta, clamps to
// [0.5, 3.0], rounds to one decimal, and writes it back.
func (u *UI) stepScale(delta float64) {
	s, err := strconv.ParseFloat(strings.TrimSpace(u.fontScaleEdit.Text()), 64)
	if err != nil || s <= 0 {
		s = 1.0
	}
	s = math.Round((s+delta)*10) / 10
	switch {
	case s < 0.5:
		s = 0.5
	case s > 3.0:
		s = 3.0
	}
	u.fontScaleEdit.SetText(formatScale(s))
}

func formatScale(s float64) string {
	return strconv.FormatFloat(math.Round(s*10)/10, 'f', -1, 64)
}

// fontPickedName returns the display label for the currently-selected font.
func (u *UI) fontPickedName() string {
	for _, f := range u.fontList {
		if f.Path == u.fontPickedPath {
			return f.Name
		}
	}
	return "Built-in (Go)"
}

func (u *UI) settingsRow(gtx layout.Context) layout.Dimensions {
	return panel(gtx, cardBg, unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(u.label("Settings", accent, u.sp(14))),
			layout.Rigid(spacer(unit.Dp(8))),
			layout.Rigid(u.label("Theme", mute, u.sp(11))),
			layout.Rigid(u.themeDropdown()),
			layout.Rigid(spacer(unit.Dp(8))),
			layout.Rigid(u.label("Card layout", mute, u.sp(11))),
			layout.Rigid(u.layoutDropdown()),
			layout.Rigid(spacer(unit.Dp(8))),
			layout.Rigid(u.label("Font face", mute, u.sp(11))),
			layout.Rigid(u.fontDropdown()),
			layout.Rigid(spacer(unit.Dp(8))),
			layout.Rigid(u.label("Font scale", mute, u.sp(11))),
			layout.Rigid(u.scaleStepper()),
		}
		if u.settingsErr != "" {
			children = append(children,
				layout.Rigid(spacer(unit.Dp(6))),
				layout.Rigid(u.label(u.settingsErr, danger, u.sp(12))),
			)
		}
		children = append(children,
			layout.Rigid(spacer(unit.Dp(10))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.iconBtn(&u.settingsSaveBtn, iconSave, good, "save")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.iconBtn(&u.settingsCancelBtn, iconCancel, mute, "cancel")),
				)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// fontDropdown is the selected-font display button. When fontPickerOpen is
// true, the list of all catalog options expands directly underneath.
func (u *UI) fontDropdown() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		chev := iconDropdown
		if u.fontPickerOpen {
			chev = iconDropup
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.fontPickerBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return panel(gtx, panelBg, unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
								layout.Rigid(u.label(u.fontPickedName(), neutral, u.sp(13))),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Max.X = gtx.Dp(unit.Dp(16))
									gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(16))
									return chev.Layout(gtx, mute)
								}),
							)
						})
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !u.fontPickerOpen {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, panelBg, unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
						items := make([]layout.FlexChild, 0, len(u.fontList))
						for i := range u.fontList {
							items = append(items, layout.Rigid(u.fontOptionRow(i)))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
					})
				})
			}),
		)
	}
}

// layoutOptions returns the layout ids in display order. Must stay in sync
// with the layoutOptionBtns array length on UI.
func layoutOptions() []string {
	return []string{settings.LayoutVertical, settings.LayoutHorizontal}
}

// layoutLabel returns the user-visible name for a layout id.
func layoutLabel(id string) string {
	switch id {
	case settings.LayoutHorizontal:
		return "Horizontal (cards side by side)"
	default:
		return "Vertical (cards stacked)"
	}
}

// layoutDropdown is the selected-layout display button. When
// layoutPickerOpen is true, vertical/horizontal options expand underneath.
func (u *UI) layoutDropdown() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		chev := iconDropdown
		if u.layoutPickerOpen {
			chev = iconDropup
		}
		opts := layoutOptions()
		picked := u.layoutPickedName
		if picked == "" {
			picked = settings.LayoutVertical
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.layoutPickerBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return panel(gtx, panelBg, unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
								layout.Rigid(u.label(layoutLabel(picked), neutral, u.sp(13))),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Max.X = gtx.Dp(unit.Dp(16))
									gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(16))
									return chev.Layout(gtx, mute)
								}),
							)
						})
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !u.layoutPickerOpen {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, panelBg, unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
						items := make([]layout.FlexChild, 0, len(opts))
						for i, id := range opts {
							col := neutral
							if id == picked {
								col = accent
							}
							btn := &u.layoutOptionBtns[i]
							label := layoutLabel(id)
							items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return u.label(label, col, u.sp(12))(gtx)
									})
								})
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
					})
				})
			}),
		)
	}
}

// themeDropdown is the selected-theme display button. When themePickerOpen is
// true, dark/light/linear options expand directly underneath.
func (u *UI) themeDropdown() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		chev := iconDropdown
		if u.themePickerOpen {
			chev = iconDropup
		}
		opts := themeOptions()
		picked := u.themePickedName
		if picked == "" {
			picked = "dark"
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.themePickerBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return panel(gtx, panelBg, unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
								layout.Rigid(u.label(themeLabel(picked), neutral, u.sp(13))),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Max.X = gtx.Dp(unit.Dp(16))
									gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(16))
									return chev.Layout(gtx, mute)
								}),
							)
						})
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !u.themePickerOpen {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, panelBg, unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
						items := make([]layout.FlexChild, 0, len(opts))
						for i, id := range opts {
							col := neutral
							if id == picked {
								col = accent
							}
							btn := &u.themeOptionBtns[i]
							label := themeLabel(id)
							items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return u.label(label, col, u.sp(12))(gtx)
									})
								})
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
					})
				})
			}),
		)
	}
}

func (u *UI) fontOptionRow(i int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		opt := u.fontList[i]
		selected := opt.Path == u.fontPickedPath
		col := neutral
		if selected {
			col = accent
		}
		return u.fontOptionBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return u.label(opt.Name, col, u.sp(12))(gtx)
			})
		})
	}
}

// scaleStepper renders [-]  <editor>  [+] for the font scale value.
func (u *UI) scaleStepper() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.iconBtn(&u.fontScaleDecBtn, iconMinus, neutral, "decrease scale")),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, u.editor(&u.fontScaleEdit, "1.0")),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(u.iconBtn(&u.fontScaleIncBtn, iconAdd, neutral, "increase scale")),
		)
	}
}

func (u *UI) formRow(gtx layout.Context) layout.Dimensions {
	title := "New account"
	if u.editIdx >= 0 {
		title = "Edit account"
	}
	return panel(gtx, cardBg, unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(u.label(title, accent, u.sp(14))),
			layout.Rigid(spacer(unit.Dp(8))),
			layout.Rigid(u.label("Name", mute, u.sp(11))),
			layout.Rigid(u.editor(&u.nameEdit, "e.g. work")),
			layout.Rigid(spacer(unit.Dp(6))),
			layout.Rigid(u.label("Credentials path (~/.claude/.credentials.json)", mute, u.sp(11))),
			layout.Rigid(u.editor(&u.pathEdit, defaultCredsPath)),
		}
		if u.formErr != "" {
			children = append(children,
				layout.Rigid(spacer(unit.Dp(6))),
				layout.Rigid(u.label(u.formErr, danger, u.sp(12))),
			)
		}
		children = append(children,
			layout.Rigid(spacer(unit.Dp(10))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.iconBtn(&u.saveBtn, iconSave, good, "save")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.iconBtn(&u.cancelBtn, iconCancel, mute, "cancel")),
				)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func (u *UI) editor(ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, panelBg, unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
				e := material.Editor(u.Theme, ed, hint)
				e.Color = neutral
				e.HintColor = mute
				e.TextSize = u.sp(13)
				return e.Layout(gtx)
			})
		})
	}
}

// errorRow renders the quota error as a clickable row that copies the full
// error text to the system clipboard.
func (u *UI) errorRow(gtx layout.Context, idx int, msg string) layout.Dimensions {
	a := &u.accounts[idx]
	hint := "click to copy"
	hintCol := mute
	if !a.copiedAt.IsZero() && time.Since(a.copiedAt) < 2*time.Second {
		hint = "copied!"
		hintCol = good
	}
	return a.copyErrBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.label("error: "+msg, danger, u.sp(12))),
			layout.Rigid(u.label(hint, hintCol, u.sp(10))),
		)
	})
}

func (u *UI) quotaRow(gtx layout.Context, label string, w quota.Window, dur time.Duration) layout.Dimensions {
	pct := w.Utilization
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	barW := gtx.Constraints.Max.X - 1
	if barW < 100 {
		barW = 100
	}
	const barH = 18
	fill := int(float64(barW) * pct / 100)

	// Pacing decoration: split the fill at the ideal-pace point so the
	// portion past ideal renders as an overshoot, mark each day-budget
	// boundary, and stamp the ideal position with a tall marker.
	idealPct, paceHead, paceDetail, paceCol, showPace := pacing(w, dur)
	idealX := int(float64(barW) * idealPct / 100)

	primary := accent
	overStart := -1
	if showPace {
		delta := w.Utilization - idealPct
		switch {
		case delta > 1:
			overStart = idealX
		case delta < -1:
			primary = good
		}
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(u.label(label, neutral, u.sp(13))),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(u.boldLabel(fmt.Sprintf("%.0f%%", pct), white, u.sp(13))),
						layout.Rigid(u.label(fmt.Sprintf("  •  resets %s (in %s)", formatReset(w.ResetsAt), formatUntil(w.ResetsAt)), mute, u.sp(11))),
					)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			ops := gtx.Ops
			drawRect(ops, image.Rect(0, 0, barW, barH), barBg)

			// Primary fill: up to either current usage or the ideal split.
			if fill > 0 {
				end := fill
				if overStart >= 0 && end > overStart {
					end = overStart
				}
				drawRect(ops, image.Rect(0, 0, end, barH), primary)
			}
			// Overshoot zone: idealX..fill in danger.
			if overStart >= 0 && fill > overStart {
				drawRect(ops, image.Rect(overStart, 0, fill, barH), danger)
			}

			// Day-budget tick marks across the full bar. Each tick marks
			// where ideal usage would sit at the end of that day. Drawn on
			// top so they remain visible over the fill.
			if showPace && dur >= 48*time.Hour {
				days := int(dur / (24 * time.Hour))
				for d := 1; d < days; d++ {
					x := int(float64(barW) * float64(d) / float64(days))
					// Short tick from top and bottom edges, leaving the
					// middle of the bar untouched.
					drawRect(ops, image.Rect(x, 0, x+1, 4), tickCol)
					drawRect(ops, image.Rect(x, barH-4, x+1, barH), tickCol)
				}
			}

			// Ideal-position marker: full-height bright line on top.
			if showPace && idealX > 0 && idealX < barW {
				markerCol := neutral
				if overStart >= 0 {
					markerCol = overMarker
				}
				drawRect(ops, image.Rect(idealX-1, 0, idealX+2, barH), markerCol)
			}
			return layout.Dimensions{Size: image.Pt(barW, barH)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !showPace {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
					layout.Rigid(u.paceLegend(idealPct, overStart >= 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(u.boldLabel(paceHead, paceCol, u.sp(14))),
							layout.Rigid(u.label("  •  "+paceDetail, mute, u.sp(11))),
						)
					}),
				)
			})
		}),
	)
}

// paceLegend renders a swatch+caption matching the ideal-position marker on
// the bar so the bright vertical line above it is self-explanatory.
func (u *UI) paceLegend(idealPct float64, over bool) layout.Widget {
	markerCol := neutral
	if over {
		markerCol = overMarker
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				drawRect(gtx.Ops, image.Rect(0, 0, 3, 10), markerCol)
				return layout.Dimensions{Size: image.Pt(3, 10)}
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Rigid(u.label("ideal ", mute, u.sp(10))),
			layout.Rigid(u.boldLabel(fmt.Sprintf("%.0f%%", idealPct), white, u.sp(10))),
		)
	}
}

// pacing returns the ideal-pace percentage, a prominent headline (e.g. "12%
// under"), a smaller detail string with the remaining-day budget, and a color
// indicating whether the user is over, under, or on pace for a window of the
// given total duration. Only emits output for windows ≥24h since shorter
// windows already display their own utilization clearly.
func pacing(w quota.Window, dur time.Duration) (idealPct float64, headline, detail string, col color.NRGBA, ok bool) {
	if !w.Active() || dur < 24*time.Hour {
		return 0, "", "", mute, false
	}
	remaining := time.Until(w.ResetsAt)
	if remaining <= 0 || remaining > dur {
		return 0, "", "", mute, false
	}
	elapsed := dur - remaining
	idealPct = float64(elapsed) / float64(dur) * 100
	delta := w.Utilization - idealPct

	remainingPct := 100 - w.Utilization
	if remainingPct < 0 {
		remainingPct = 0
	}
	days := remaining.Hours() / 24
	var budget string
	switch {
	case days >= 1:
		budget = fmt.Sprintf("budget %.1f%%/day for %s", remainingPct/days, formatUntil(w.ResetsAt))
	case remaining > 0:
		budget = fmt.Sprintf("budget %.1f%%/hr for %s", remainingPct/remaining.Hours(), formatUntil(w.ResetsAt))
	}

	switch {
	case delta > 1:
		return idealPct, fmt.Sprintf("%.0f%% over", delta), budget, danger, true
	case delta < -1:
		return idealPct, fmt.Sprintf("%.0f%% under", -delta), budget, good, true
	default:
		return idealPct, "on pace", budget, mute, true
	}
}

// iconBtn returns a small icon-only button (~22dp) tinted with col.
func (u *UI) iconBtn(btn *widget.Clickable, ic *widget.Icon, col color.NRGBA, desc string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.IconButton(u.Theme, btn, ic, desc)
		b.Background = panelBg
		b.Color = col
		b.Size = unit.Dp(14)
		b.Inset = layout.UniformInset(unit.Dp(5))
		return b.Layout(gtx)
	}
}

func refreshHint(loading bool, fetched time.Time) string {
	if loading {
		return "refreshing…"
	}
	if fetched.IsZero() {
		return "press refresh to load quota"
	}
	return fmt.Sprintf("updated %s ago", since(fetched))
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

// --- small helpers --------------------------------------------------------

func (u *UI) bigLabel(s string, c color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Label(u.Theme, u.sp(17), s)
		lbl.Color = c
		lbl.Font.Weight = 600
		return lbl.Layout(gtx)
	}
}

func (u *UI) label(s string, c color.NRGBA, sp unit.Sp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Label(u.Theme, sp, s)
		lbl.Color = c
		return lbl.Layout(gtx)
	}
}

func (u *UI) boldLabel(s string, c color.NRGBA, sp unit.Sp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		lbl := material.Label(u.Theme, sp, s)
		lbl.Color = c
		lbl.Font.Weight = 700
		return lbl.Layout(gtx)
	}
}

func panel(gtx layout.Context, c color.NRGBA, pad unit.Dp, inner layout.Widget) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: pad, Bottom: pad, Left: pad, Right: pad}.Layout(gtx, inner)
		call := macro.Stop()
		fillRect(gtx.Ops, dims.Size, c)
		call.Add(gtx.Ops)
		return dims
	})
}

func fillRect(ops *op.Ops, size image.Point, c color.NRGBA) {
	defer clip.Rect{Max: size}.Push(ops).Pop()
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
}

func drawRect(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	defer clip.Rect{Min: r.Min, Max: r.Max}.Push(ops).Pop()
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
}

func spacer(d unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: d}.Layout(gtx)
	}
}

func since(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
