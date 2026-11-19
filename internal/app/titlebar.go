package app

// The window's own title bar.
//
// The desktop's title bar is drawn by the window manager in its own colors, and
// a window with a dark sea behind it framed in a light grey strip is two designs
// on one screen. A custom title bar keeps the traffic lights and lets the page
// paint the strip, so the window is one surface from its top edge to its bottom.

import (
	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

const (
	// titlebarHeight is the strip the traffic lights and window buttons sit in.
	titlebarHeight = 38
)

// titlebar is the strip at the top of the window: the application's mark, the
// page's name, and the space the window buttons need. Dragging it moves the
// window; a double click zooms it (both are MovesWindow's).
func (a *App) titlebar(c ui.C) {
	theme := c.Theme()
	leading, trailing := app.TitlebarInsets()
	top := a.top()

	bar := ui.Row(c).FillWidth().Height(titlebarHeight).Items(ui.Center).MovesWindow()
	// The strip is the top of the sea: the same texture shows through it, with a
	// hairline under it instead of a frame around it.
	bar.PadLeft(trailingFor(leading, trailing)).PadRight(trailing)
	bar.Children(func() {
		ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
			ui.Text(c, "Cetone").FontSize(12).Weight(gfx.WeightMedium).Color(theme.MutedForeground)
			ui.Text(c, "·").FontSize(12).Color(theme.MutedForeground).Opacity(0.6)
			ui.Text(c, top.title).FontSize(12).Color(theme.MutedForeground)
		})
	})
}

// trailingFor is the leading inset: the strip starts at the traffic lights.
func trailingFor(leading, _ float32) float32 {
	if leading > 0 {
		return leading
	}
	return 10
}

// windowControlsStyle is how Viva draws the minimize, maximize and close buttons
// on Linux and Windows. It is a function because the config calls it at every
// paint, which is what makes the buttons follow a theme change.
func (a *App) windowControlsStyle() app.WindowControlsStyle {
	theme := a.theme
	dark := isDark(theme)
	foreground := gfx.RGB24(0x5B7C93)
	hover := theme.Muted
	if dark {
		foreground = gfx.RGB24(0x8FB3CB)
	}
	return app.WindowControlsStyle{
		Foreground:      foreground,
		Hover:           hover,
		Pressed:         theme.Accent,
		Close:           gfx.RGB24(0xD92B45),
		CloseForeground: gfx.RGB24(0xFFFFFF),
		Minimize:        "最小化",
		Maximize:        "最大化",
		Restore:         "还原",
		CloseName:       "关闭",
	}
}

// clearColor is the window's own background, behind the page's first frame: the
// sea's base, so the window never flashes a colour the application does not use.
func (a *App) clearColor() app.Color {
	// app.Color is linear RGBA in float64; the theme's colors are sRGB in float32.
	c := gfx.RGB24(0xF0F4F8)
	if isDark(a.theme) {
		c = gfx.RGB24(0x05080E)
	}
	return app.Color{Red: float64(c.Red), Green: float64(c.Green), Blue: float64(c.Blue), Alpha: 1}
}
