package app

// The application's look: the sea the icon is drawn from, painted under the
// content.
//
// Nothing here animates. A background that moved would keep the window awake for
// as long as it was on screen, and this application is a music player that sits
// idle between songs.

import (
	"math"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// oceanHero is the greeting band at the top of the discover page: a deep-water
// gradient with the light coming from the upper left, as the whale in the icon
// has it.
func (a *App) oceanHero(c ui.C, body func()) {
	theme := c.Theme()
	dark := isDark(*theme)
	from, to := gfx.RGB24(0xE3F4FE), gfx.RGB24(0xFAFDFF)
	if dark {
		from, to = gfx.RGB24(0x0E3550), gfx.RGB24(0x07131C)
	}
	band := ui.Row(c).FillWidth().PadSides(20, 20, 18, 20).Radius(theme.Metrics.RadiusSurface)
	band.Gradient(ui.ToBottomRight, from, to)
	band.Children(func() {
		ui.Row(c).FillWidth().Children(body)
	})
}

// oceanStrip draws the two thin bands that read as water under a surface: a
// lighter line over a darker one, the cyan of the icon's rim light.
func oceanStrip(c ui.C) {
	theme := c.Theme()
	dark := isDark(*theme)
	top, bottom := gfx.RGB24(0xBFE7FA), gfx.RGB24(0xE8F6FE)
	if dark {
		top, bottom = gfx.RGB24(0x1E6E96), gfx.RGB24(0x0F3B55)
	}
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Height(2).Gradient(ui.ToRight, fromAlpha(top, 0.0), fromAlpha(top, 0.9))
		ui.Row(c).FillWidth().Height(1).Gradient(ui.ToRight, fromAlpha(bottom, 0.0), fromAlpha(bottom, 0.6))
	})
}

// fromAlpha is a color at a given opacity, for a gradient stop that fades out.
func fromAlpha(c gfx.Color, alpha float32) gfx.Color { return c.WithAlpha(alpha) }

// brandTint is the accent color of the current appearance, for the small marks
// that carry the brand (the playing row, the icon of a section).
func brandTint(theme gfx.Theme, dark bool) gfx.Color {
	if dark {
		return gfx.RGB24(colorCyan)
	}
	return gfx.RGB24(colorBlue)
}

// luminance is the relative luminance of a color (WCAG 2.x). The pages read it
// to know which appearance they paint in: a theme is a value, so "is this dark"
// is a question about the background, not a flag the page has to carry.
func luminance(c gfx.Color) float64 {
	channel := func(v float32) float64 {
		x := float64(v)
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.Red) + 0.7152*channel(c.Green) + 0.0722*channel(c.Blue)
}

// isDark reports whether a theme is the dark appearance.
func isDark(theme gfx.Theme) bool { return luminance(theme.Background) < 0.3 }

// contrast is the WCAG contrast ratio between two colors: 4.5 is body text,
// 3.0 large text and interface shapes.
func contrast(a, b gfx.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// glass is a translucent panel over the ocean. The alpha is low enough that the
// texture reads through it: a panel that hides the sea behind it turns the window
// into three white plates on a picture, which is exactly the seam this is here to
// avoid. A little of the card color is mixed in so text on a panel has a steadier
// surface than the texture alone.
func glass(theme gfx.Theme) gfx.Color {
	if isDark(theme) {
		return gfx.RGB24(0x0B1A26).WithAlpha(0.55)
	}
	return gfx.RGB24(0xFFFFFF).WithAlpha(0.46)
}

// glassBorder is the hairline around a glass panel. It is the accent color at a
// low alpha: a frame in the accent color, drawn at full strength, is what makes a
// panel look pasted on instead of part of the surface.
func glassBorder(theme gfx.Theme) gfx.Color {
	if isDark(theme) {
		return gfx.RGB24(colorBlue).WithAlpha(0.10)
	}
	// A pale panel on a pale sea needs almost no outline: the shadow and the
	// change of tone already separate it. A line any stronger reads as a box
	// drawn around the content.
	return gfx.RGB24(0x7FA8C4).WithAlpha(0.16)
}

// glassPanel dresses a box as a glass card.
func glassPanel(box ui.Box, theme gfx.Theme) ui.Box {
	box.Background(glass(theme))
	box.Border(1, glassBorder(theme))
	return box
}
