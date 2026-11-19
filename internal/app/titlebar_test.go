package app

import (
	"testing"

	"github.com/vivaui/viva/app"
)

// The title bar exists in the page and the window mode is the custom one: the
// desktop's own grey strip is what the user asked to have blended in.
func TestTheWindowPaintsItsOwnTitlebar(t *testing.T) {
	a := headlessModel(t)
	page, _ := a.Page(app.Config{Title: "cetone", Width: 1200, Height: 800})
	if page == nil {
		t.Fatal("no page")
	}
	// The config Run builds sets these; a test that only used Page would miss
	// them, so the values are checked where they are decided.
	cfg := a.WindowConfig(app.Config{Title: "cetone", Width: 1200, Height: 800})
	if cfg.WindowMode != app.WindowCustomTitlebar {
		t.Fatalf("window mode is %v", cfg.WindowMode)
	}
	if cfg.TitlebarHeight <= 0 {
		t.Fatal("the title bar has no height")
	}
	if cfg.WindowControls == nil {
		t.Fatal("the window buttons are not styled")
	}
	style := cfg.WindowControls()
	if style.Foreground.Alpha == 0 {
		t.Fatal("the window buttons have no colour")
	}
	if style.CloseName == "" || style.Minimize == "" {
		t.Fatal("the window buttons have no accessible names")
	}
}

// The buttons follow the appearance, because the config calls the style at every
// paint.
func TestTheWindowControlsFollowTheAppearance(t *testing.T) {
	a := headlessModel(t)
	a.config.Theme = "dark"
	a.applyTheme()
	dark := a.windowControlsStyle()
	a.config.Theme = "light"
	a.applyTheme()
	light := a.windowControlsStyle()
	if dark.Foreground == light.Foreground {
		t.Fatal("the buttons use one colour in both appearances")
	}
	if a.clearColor() == (app.Color{}) {
		t.Fatal("the window has no clear colour")
	}
}
