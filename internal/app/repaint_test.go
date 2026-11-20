package app

// A player's window changes while nothing happens: the transport clock moves, the
// lyric line advances. Nothing in Viva paints without being asked, so the page has
// to ask for the next frame for as long as a song plays — and to stop asking once
// it does not, so an idle window costs nothing.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/player"
)

// asksForAnotherFrame paints the page once and reports whether it asked for the
// next frame.
func asksForAnotherFrame(t *testing.T, a *App) bool {
	t.Helper()
	var asked bool
	theme := gfx.LightTheme()
	page := ui.NewPage(ui.PageConfig{
		View: func(c ui.C) {
			a.page(c)
			_, asked = c.Canvas().NextFrame()
		},
		Theme: &theme,
	})
	config := page.Config(app.Config{Title: "test", Width: 1000, Height: 700})
	config.OnWake = a.sync
	driver, err := app.NewDriver(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	if err := driver.Paint(); err != nil {
		t.Fatal(err)
	}
	return asked
}

func TestTheWindowAsksForFramesWhilePlaying(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(path, 8000, 3); err != nil {
		t.Fatal(err)
	}
	if err := a.engine.Play(context.Background(), player.Source{
		Title: "tone", File: path, Duration: 3 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Stop)

	if !asksForAnotherFrame(t, a) {
		t.Fatal("the window never asks for another frame, so the clock and the lyric line would freeze")
	}

	// Paused, and stopped, the window goes back to sleep: a music player that is
	// not playing must not keep repainting.
	a.engine.Pause()
	if asksForAnotherFrame(t, a) {
		t.Fatal("the window still asks for frames while paused")
	}
	a.engine.Stop()
	if asksForAnotherFrame(t, a) {
		t.Fatal("the window still asks for frames after the song stopped")
	}
}
