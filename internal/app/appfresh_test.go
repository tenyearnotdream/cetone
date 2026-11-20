package app

// The listener's path: play, drag the bar, then skip. The next song must start at
// its beginning; the position the bar was dragged to belongs to the song it was
// dragged on.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

func TestTheNextSongStartsAtTheBeginningAfterADrag(t *testing.T) {
	a := testApp(t)
	dir := filepath.Join(a.paths.Cache, "music")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	songs := make([]ncm.Song, 2)
	for i := range songs {
		id := int64(9700 + i)
		songs[i] = ncm.Song{ID: id, Name: "song-" + string(rune('a'+i)), Duration: 60 * time.Second}
		if err := writeTestWAV(a.cache.Path(id, a.songQuality(songs[i])), 8000, 60); err != nil {
			t.Fatal(err)
		}
	}
	a.queue = songs
	a.queueAt = 0
	a.playAt(0)

	// Wait for the first song.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if s := a.engine.Snapshot(); s.Title == songs[0].Name && s.Position > 200*time.Millisecond {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Drag far into the song, as the bar does.
	a.engine.Seek(45 * time.Second)
	time.Sleep(150 * time.Millisecond)
	t.Logf("after dragging: %v", a.engine.Snapshot().Position.Round(time.Millisecond))

	// Skip to the next song, which is what the listener does next.
	a.next()
	deadline = time.Now().Add(5 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		a.sync()
		if s := a.engine.Snapshot(); s.Title == songs[1].Name && s.Status == player.StatusPlaying {
			started = true
			pos := s.Position
			t.Logf("the next song reports %v at once", pos.Round(time.Millisecond))
			if pos > 2*time.Second {
				t.Fatalf("the next song starts at %v, not at its beginning", pos.Round(time.Millisecond))
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started {
		t.Fatalf("the next song never started (engine says %q)", a.engine.Snapshot().Title)
	}
}

// The same after a pause and a play: pausing keeps the position, and starting
// another song must not inherit it.
func TestASongAfterAPauseStartsAtTheBeginning(t *testing.T) {
	a := testApp(t)
	dir := filepath.Join(a.paths.Cache, "music")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	songs := make([]ncm.Song, 2)
	for i := range songs {
		id := int64(9800 + i)
		songs[i] = ncm.Song{ID: id, Name: "song-" + string(rune('a'+i)), Duration: 60 * time.Second}
		if err := writeTestWAV(a.cache.Path(id, a.songQuality(songs[i])), 8000, 60); err != nil {
			t.Fatal(err)
		}
	}
	a.queue = songs
	a.queueAt = 0
	a.playAt(0)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if a.engine.Snapshot().Position > 200*time.Millisecond {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.engine.Seek(30 * time.Second)
	time.Sleep(100 * time.Millisecond)
	a.engine.Pause()
	a.sync()

	a.playAt(1)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if s := a.engine.Snapshot(); s.Title == songs[1].Name && s.Status == player.StatusPlaying {
			if s.Position > 2*time.Second {
				t.Fatalf("the song after a pause starts at %v", s.Position.Round(time.Millisecond))
			}
			t.Logf("after a pause, the next song reports %v", s.Position.Round(time.Millisecond))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the song never started")
}
