package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivaui/viva/gfx"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

// Clicking the track and the arrow keys go through the slider the bar uses. Only
// a drag waits for its release, so these must still move the song at once.
func TestTheBarSeeksByClickAndArrow(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(path, 8000, 20); err != nil {
		t.Fatal(err)
	}
	song := ncm.Song{ID: 8001, Name: "tone", Duration: 20 * time.Second}
	a.queue = []ncm.Song{song}
	a.playAt(0)
	if err := a.engine.Play(context.Background(), player.Source{
		Title: "tone", File: path, Duration: 20 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Stop)
	a.sync()

	driver := open(t, a)
	pump(t, driver, a)

	var bar gfx.Rect
	found := false
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == "播放进度" {
			bar, found = hit.Bounds, true
			break
		}
	}
	if !found {
		t.Fatal("the progress bar is not on screen")
	}
	// A click three quarters along the track.
	if err := driver.Click(bar.X+bar.Width*0.75, bar.Y+bar.Height/2); err != nil {
		t.Fatal(err)
	}
	pump(t, driver, a)
	if got := a.engine.Snapshot().Position; got < 12*time.Second {
		t.Fatalf("a click at three quarters left the transport at %v", got)
	}
}
