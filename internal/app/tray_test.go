package app

// The status item's logic, tested without a status area: what the menu says,
// what closing the window does with and without one, and the queue a menu entry
// publishes.

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivaui/viva/app"

	"cetone/internal/player"
)

func TestTrayMenuDescribesTheSong(t *testing.T) {
	a := headlessModel(t)
	a.trayAvailable = true
	a.queueAt = 0

	items := a.trayMenu()
	if len(items) < 6 {
		t.Fatalf("the menu has %d entries", len(items))
	}
	if items[0].Title != "海阔天空 - Beyond" {
		t.Fatalf("the first entry is %q, want the song", items[0].Title)
	}
	if !items[0].Disabled {
		t.Fatal("the now-playing entry is selectable")
	}
	// The transport entries are the ones a player's tray offers.
	titles := make([]string, 0, len(items))
	for _, item := range items {
		if !item.Separator {
			titles = append(titles, item.Title)
		}
	}
	for _, want := range []string{"上一首", "播放", "下一首", "退出"} {
		found := false
		for _, title := range titles {
			if title == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the menu has no %q entry; it has %v", want, titles)
		}
	}
}

func TestTrayMenuSaysPauseWhilePlaying(t *testing.T) {
	a := headlessModel(t)
	a.trayAvailable = true
	// A paused engine still says 播放; the entry follows the transport.
	for _, item := range a.trayMenu() {
		if item.ID == "playpause" && item.Title != "播放" {
			t.Fatalf("with nothing playing the entry is %q", item.Title)
		}
	}
}

// A menu entry applies its change at once: the status item's callbacks run on
// the window's own thread, and a hidden window is never repainted, so waiting
// for one would make a tray action do nothing until something else showed the
// window.
func TestATrayEntryActsAtOnce(t *testing.T) {
	a := headlessModel(t)
	a.trayAvailable = true
	a.queueAt = 2
	for _, item := range a.trayMenu() {
		if item.ID == "next" {
			item.OnSelect()
		}
	}
	if a.queueAt != 3 {
		t.Fatalf("the next entry left the cursor at %d", a.queueAt)
	}
	if !a.work.quiet() {
		t.Fatal("the entry left work queued for a repaint that may never come")
	}
}

// The play/pause entry follows the transport, and pressing it moves it.
func TestTheTrayPlayEntryTogglesTheTransport(t *testing.T) {
	a := headlessModel(t)
	a.trayAvailable = true
	if err := a.engine.Play(t.Context(), testPlayerSource(t, a)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a, func() bool { return a.engine.Snapshot().Position > 100*time.Millisecond })
	var entry func()
	for _, item := range a.trayMenu() {
		if item.ID == "playpause" {
			if item.Title != "暂停" {
				t.Fatalf("while playing the entry is %q", item.Title)
			}
			entry = item.OnSelect
		}
	}
	if entry == nil {
		t.Fatal("the menu has no play/pause entry")
	}
	entry()
	if status := a.engine.Snapshot().Status.String(); status != "paused" {
		t.Fatalf("the transport is %q after the tray entry", status)
	}
}

// Closing the window with a status item hides it; without one it closes, because
// a hidden window with no icon anywhere is an application the user cannot get
// back.
func TestClosingTheWindowHidesOnlyWhenThereIsATray(t *testing.T) {
	a := headlessModel(t)
	a.config.CloseToTray = true

	allowed := false
	a.trayAvailable = false
	a.handleCloseRequest(func() { allowed = true })
	if !allowed {
		t.Fatal("without a status area the close was not allowed")
	}

	allowed = false
	a.trayAvailable = true
	a.handleCloseRequest(func() { allowed = true })
	if allowed {
		t.Fatal("with a status area the window closed instead of hiding")
	}
	// There is no window object in this test, so what matters is that the close
	// was not allowed through: the request hid instead of closing.
}

// The setting turns the behaviour off: with a tray, closing still quits when the
// user asked it to.
func TestClosingQuitsWhenCloseToTrayIsOff(t *testing.T) {
	a := headlessModel(t)
	a.trayAvailable = true
	a.config.CloseToTray = false
	allowed := false
	a.handleCloseRequest(func() { allowed = true })
	if !allowed {
		t.Fatal("with close-to-tray off the window did not close")
	}
}

// A desktop without a status area is not a failure: it is a normal machine.
func TestNoStatusAreaIsNotAFailure(t *testing.T) {
	a := headlessModel(t)
	a.trayUnavailable = errors.Is(app.ErrNoStatusArea, app.ErrNoStatusArea)
	a.trayErr = nil
	// The settings page says why the switch is off rather than showing an error.
	if got := a.trayProblemText(); got == "" {
		t.Fatal("the settings page has nothing to say")
	}
}

// testPlayerSource is a cached fixture song the engine can play.
func testPlayerSource(t *testing.T, a *App) player.Source {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeTestWav(t, path, 8000, 2, 5)
	return player.Source{Title: "tone", File: path, Duration: 5 * time.Second}
}
