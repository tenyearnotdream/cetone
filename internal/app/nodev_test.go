package app

// The build machines and the containers have no sound card. Seeking must work
// there too, so this drives the bar with the device refused.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivaui/viva/gfx"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

func TestSeekingWorksWithoutASoundCard(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(path, 8000, 20); err != nil {
		t.Fatal(err)
	}
	// No device: the engine plays in real time with nothing to write to.
	a.engine.NewSink = func() player.Sink { return &noDeviceSink{} }
	song := ncm.Song{ID: 8002, Name: "tone", Duration: 20 * time.Second}
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
	if err := driver.Click(bar.X+bar.Width*0.75, bar.Y+bar.Height/2); err != nil {
		t.Fatal(err)
	}
	pump(t, driver, a)
	// The paint after the click applies the request.
	if err := driver.Paint(); err != nil {
		t.Fatal(err)
	}
	if got := a.engine.Snapshot().Position; got < 12*time.Second {
		t.Fatalf("a click at three quarters left the transport at %v", got)
	}
}

// noDeviceSink refuses to open, as a machine with no sound card does.
type noDeviceSink struct{}

func (s *noDeviceSink) Open(player.Format) error  { return player.ErrNoDevice }
func (s *noDeviceSink) Write([]byte) (int, error) { return 0, player.ErrNoDevice }
func (s *noDeviceSink) Close() error              { return nil }
