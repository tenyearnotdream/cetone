package app

import (
	"testing"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
)

func appConfigForTest() app.Config {
	return app.Config{Title: "cetone", Width: 1000, Height: 700}
}

// TestChangingTheThemeChangesThePaint is the regression: the page captured a
// pointer to a theme built once at startup, so choosing another theme in the
// settings changed the file and nothing on screen.
func TestChangingTheThemeChangesThePaint(t *testing.T) {
	a := headlessModel(t)
	a.config.Theme = "light"
	_, _ = a.Page(appConfigForTest())
	light := a.theme
	// Light is a pale surface; the application paints its own rather than the
	// framework's pure white, so the property is "bright", not "equal to white".
	if luminance(light.Background) < 0.9 {
		t.Fatalf("the light theme paints %v, which is not a light surface", light.Background)
	}

	// What the settings page does when the user picks dark.
	a.config.Theme = "dark"
	a.applyTheme()
	if equalColor(a.theme.Background, light.Background) {
		t.Fatal("the theme did not change after switching to dark")
	}
	if luminance(a.theme.Background) > 0.2 {
		t.Fatalf("the dark theme paints %v, which is not a dark surface", a.theme.Background)
	}

	// The page holds a pointer to the model's theme, so the next paint reads the
	// new value without rebuilding the window.
	page, _ := a.Page(appConfigForTest())
	a.config.Theme = "light"
	a.applyTheme()
	if luminance(a.theme.Background) < 0.9 {
		t.Fatal("switching back to light left a dark background")
	}
	_ = page
}

// The brand colors are the icon's, and they are set for both appearances.
func TestTheBrandColorsApplyToBothThemes(t *testing.T) {
	a := headlessModel(t)
	a.config.Theme = "light"
	a.applyTheme()
	if equalColor(a.theme.Primary, gfx.LightTheme().Primary) {
		t.Fatal("the light theme still has the framework's primary color")
	}
	// Every color the page paints text on has to keep its contrast.
	if contrast(a.theme.Foreground, a.theme.Background) < 4.5 {
		t.Fatalf("light body text on its background has contrast %.2f",
			contrast(a.theme.Foreground, a.theme.Background))
	}
	lightPrimary := a.theme.Primary
	a.config.Theme = "dark"
	a.applyTheme()
	if equalColor(a.theme.Primary, lightPrimary) {
		t.Fatal("both appearances use the same primary color")
	}
	if equalColor(a.theme.Primary, gfx.DarkTheme().Primary) {
		t.Fatal("the dark theme still has the framework's primary color")
	}
	if contrast(a.theme.Foreground, a.theme.Background) < 4.5 {
		t.Fatalf("dark body text on its background has contrast %.2f",
			contrast(a.theme.Foreground, a.theme.Background))
	}
	if contrast(a.theme.MutedForeground, a.theme.Background) < 3.0 {
		t.Fatalf("dark secondary text has contrast %.2f",
			contrast(a.theme.MutedForeground, a.theme.Background))
	}
	// The ocean palette must stay legible in both appearances: a card is a
	// surface text sits on, and a selected row is a surface colored text sits on.
	for _, appearance := range []struct {
		name               string
		theme              gfx.Theme
		card, cardText     gfx.Color
		accent, accentText gfx.Color
	}{
		{"light", a.decorate(gfx.LightTheme(), false), a.theme.Card, a.theme.CardForeground, a.theme.Accent, a.theme.AccentForeground},
		{"dark", a.decorate(gfx.DarkTheme(), true), a.theme.Card, a.theme.CardForeground, a.theme.Accent, a.theme.AccentForeground},
	} {
		if contrast(appearance.cardText, appearance.card) < 4.5 {
			t.Errorf("%s: text on a card has contrast %.2f", appearance.name,
				contrast(appearance.cardText, appearance.card))
		}
		if contrast(appearance.accentText, appearance.accent) < 4.5 {
			t.Errorf("%s: text on a selected row has contrast %.2f", appearance.name,
				contrast(appearance.accentText, appearance.accent))
		}
	}
	if contrast(a.theme.PrimaryForeground, a.theme.Primary) < 4.5 {
		t.Fatalf("text on the primary color has contrast %.2f",
			contrast(a.theme.PrimaryForeground, a.theme.Primary))
	}
}

func equalColor(a, b gfx.Color) bool { return a == b }
