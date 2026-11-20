package player

// A new song starts at its beginning. The position a seek asked for belongs to the
// session that asked for it: carrying it into the next song makes every play after
// a seek start part-way in, which is what a listener sees as the bar jumping.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// afterSeeking reports the position a fresh song reports immediately.
func freshPositionAfter(t *testing.T, disruption func(*Engine)) time.Duration {
	t.Helper()
	dir := t.TempDir()
	first := filepath.Join(dir, "a.wav")
	second := filepath.Join(dir, "b.wav")
	if err := writeLongWAV(first, 8000, 60); err != nil {
		t.Fatal(err)
	}
	if err := writeLongWAV(second, 8000, 60); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink

	if err := e.Play(context.Background(), Source{Title: "a", File: first, Duration: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	// A seek the listener asked for: 42 seconds in.
	e.Seek(42 * time.Second)
	time.Sleep(80 * time.Millisecond)

	disruption(e)

	if err := e.Play(context.Background(), Source{Title: "b", File: second, Duration: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}
	// Read at once: the jump is visible in the first frame, before any audio plays.
	return e.Snapshot().Position
}

func TestANewSongAfterASeekStartsAtTheBeginning(t *testing.T) {
	pos := freshPositionAfter(t, func(e *Engine) {
		// The listener skips to the next song while the seek is settling.
	})
	t.Logf("a new song after a seek reports %v", pos.Round(time.Millisecond))
	if pos > 500*time.Millisecond {
		t.Fatalf("the new song starts at %v instead of 0", pos.Round(time.Millisecond))
	}
}

func TestANewSongAfterStoppingStartsAtTheBeginning(t *testing.T) {
	pos := freshPositionAfter(t, func(e *Engine) {
		e.Stop()
	})
	t.Logf("a new song after a stop reports %v", pos.Round(time.Millisecond))
	if pos > 500*time.Millisecond {
		t.Fatalf("the new song starts at %v instead of 0", pos.Round(time.Millisecond))
	}
}

// A seek that is still settling when the listener skips is landed by the session
// that asked for it. That session is no longer playing, and its landing must not
// touch the new song: this was the bug that made a new song start at the previous
// one's position.
func TestAStaleLandingCannotMoveTheNewSong(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.wav")
	second := filepath.Join(dir, "b.wav")
	if err := writeLongWAV(first, 8000, 60); err != nil {
		t.Fatal(err)
	}
	if err := writeLongWAV(second, 8000, 60); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink

	if err := e.Play(context.Background(), Source{Title: "a", File: first, Duration: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	e.Seek(42 * time.Second)

	// The seek has not landed yet: replace the session at once, the way skipping
	// while the bar is still moving does.
	if err := e.Play(context.Background(), Source{Title: "b", File: second, Duration: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}
	// Give the old session time to land its seek, which it must not.
	time.Sleep(400 * time.Millisecond)
	snap := e.Snapshot()
	t.Logf("the new song reports %v (title %q)", snap.Position.Round(time.Millisecond), snap.Title)
	if snap.Title != "b" {
		t.Fatalf("the engine says %q", snap.Title)
	}
	if snap.Position > 2*time.Second {
		t.Fatalf("the new song is at %v because a stale seek landed on it", snap.Position.Round(time.Millisecond))
	}
}
