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

// Dragging the bar is the gesture a listener actually uses. It is pressed, moved
// in several steps and released: the song must end up where the pointer left it.
func TestDraggingTheBarMovesTheSong(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(path, 8000, 30); err != nil {
		t.Fatal(err)
	}
	a.engine.NewSink = func() player.Sink { return &noDeviceSink{} }
	song := ncm.Song{ID: 8101, Name: "tone", Duration: 30 * time.Second}
	a.queue = []ncm.Song{song}
	a.playAt(0)
	if err := a.engine.Play(context.Background(), player.Source{
		Title: "tone", File: path, Duration: 30 * time.Second,
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
	mid := bar.Y + bar.Height/2
	start := bar.X + bar.Width*0.10
	steps := []float32{0.30, 0.50, 0.70, 0.80}

	if err := driver.Press(start, mid, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	for _, at := range steps {
		x := bar.X + bar.Width*at
		if err := driver.Move(x, mid); err != nil {
			t.Fatal(err)
		}
		if err := driver.Paint(); err != nil {
			t.Fatal(err)
		}
		t.Logf("pointer at %.0f%%: slider=%.1f pending=%v dragging=%v position=%v",
			at*100, a.seekDrag.Value, a.seekPending, a.seekDrag.Dragging(),
			a.engine.Snapshot().Position.Round(time.Millisecond))
	}
	last := bar.X + bar.Width*steps[len(steps)-1]
	if err := driver.Release(last, mid, gfx.PointerButtonPrimary); err != nil {
		t.Fatal(err)
	}
	if err := driver.Paint(); err != nil {
		t.Fatal(err)
	}
	got := a.engine.Snapshot().Position
	t.Logf("after release: slider=%.1f pending=%v dragging=%v position=%v",
		a.seekDrag.Value, a.seekPending, a.seekDrag.Dragging(), got.Round(time.Millisecond))
	// 80% of 30s is 24s.
	if got < 22*time.Second || got > 26*time.Second {
		t.Fatalf("the drag left the song at %v, want about 24s", got)
	}
}
