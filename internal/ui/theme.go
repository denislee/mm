package ui

import "image/color"

// Palette is the set of colors cc-monitor renders with. Variants are picked at
// runtime via Settings.Theme; applyPalette swaps the live values used by the
// rest of the package.
type Palette struct {
	Bg         color.NRGBA
	PanelBg    color.NRGBA
	CardBg     color.NRGBA
	BarBg      color.NRGBA
	TickCol    color.NRGBA
	Accent     color.NRGBA
	Good       color.NRGBA
	Danger     color.NRGBA
	Mute       color.NRGBA
	Neutral    color.NRGBA
	White      color.NRGBA
	OverMarker color.NRGBA
}

// darkPalette mirrors wlslack's dark theme.
func darkPalette() Palette {
	return Palette{
		Bg:         rgb(0x0f1115),
		PanelBg:    rgb(0x0b0c10),
		CardBg:     rgb(0x16191f),
		BarBg:      rgb(0x191c22),
		TickCol:    rgb(0x262b33),
		Accent:     rgb(0x5294ff),
		Good:       rgb(0x4cc38a),
		Danger:     rgb(0xef476f),
		Mute:       rgb(0x8a93a0),
		Neutral:    rgb(0xd7dae0),
		White:      rgb(0xeef0f3),
		OverMarker: rgb(0xffe07a),
	}
}

// lightPalette mirrors wlslack's light theme. OverMarker keeps the amber tone
// so the pacing line stays legible on a light background.
func lightPalette() Palette {
	return Palette{
		Bg:         rgb(0xffffff),
		PanelBg:    rgb(0xf4f5f7),
		CardBg:     rgb(0xeef0f3),
		BarBg:      rgb(0xd7dae0),
		TickCol:    rgb(0x8a93a0),
		Accent:     rgb(0x0052cc),
		Good:       rgb(0x4cc38a),
		Danger:     rgb(0xef476f),
		Mute:       rgb(0x8a93a0),
		Neutral:    rgb(0x191c22),
		White:      rgb(0x0b0c10),
		OverMarker: rgb(0xcc7a00),
	}
}

// linearPalette mirrors wlslack's linear (purple) theme.
func linearPalette() Palette {
	return Palette{
		Bg:         rgb(0x141417),
		PanelBg:    rgb(0x1b1b20),
		CardBg:     rgb(0x232329),
		BarBg:      rgb(0x1b1b20),
		TickCol:    rgb(0x4a3d80),
		Accent:     rgb(0x7d56f4),
		Good:       rgb(0x4ade80),
		Danger:     rgb(0xeb5757),
		Mute:       rgb(0x666666),
		Neutral:    rgb(0xededed),
		White:      rgb(0xffffff),
		OverMarker: rgb(0xa885ff),
	}
}

// paletteFor returns the named palette, falling back to dark.
func paletteFor(name string) Palette {
	switch name {
	case "light":
		return lightPalette()
	case "linear":
		return linearPalette()
	default:
		return darkPalette()
	}
}

// themeOptions returns the theme ids in display order. Must stay in sync with
// the themeOptionBtns array length on UI.
func themeOptions() []string {
	return []string{"dark", "light", "linear"}
}

// themeLabel returns the user-visible name for a theme id.
func themeLabel(id string) string {
	switch id {
	case "light":
		return "Light"
	case "linear":
		return "Linear (purple)"
	default:
		return "Dark"
	}
}

// applyPalette writes the named palette into the package-level color vars that
// the rest of the UI reads from. Safe to call from the UI goroutine.
func applyPalette(name string) {
	p := paletteFor(name)
	bg = p.Bg
	panelBg = p.PanelBg
	cardBg = p.CardBg
	barBg = p.BarBg
	tickCol = p.TickCol
	accent = p.Accent
	good = p.Good
	danger = p.Danger
	mute = p.Mute
	neutral = p.Neutral
	white = p.White
	overMarker = p.OverMarker
}
