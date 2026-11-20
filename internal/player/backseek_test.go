package player

// Seeking backwards is the case the two-position bug hid in: if the discard
// counts from zero while the decoder is already a minute in, a seek back to ten
// seconds either lands far past it or reads forever.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSeekingBackwardsLands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeLongWAV(path, 8000, 60); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink
	if err := e.Play(context.Background(), Source{Title: "t", File: path, Duration: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)

	// Forward, let it play, then back: the second seek counts from where the first
	// left the decoder, not from the start.
	e.Seek(40 * time.Second)
	time.Sleep(300 * time.Millisecond)
	if pos := e.Snapshot().Position; pos < 39*time.Second || pos > 42*time.Second {
		t.Fatalf("after seeking to 40s the position is %v", pos.Round(time.Millisecond))
	}
	e.Seek(10 * time.Second)
	time.Sleep(300 * time.Millisecond)
	pos := e.Snapshot().Position
	t.Logf("back to 10s: position %v", pos.Round(time.Millisecond))
	if pos < 9*time.Second || pos > 12*time.Second {
		t.Fatalf("after seeking back to 10s the position is %v", pos.Round(time.Millisecond))
	}
	if open := r.openCount(); open != 1 {
		t.Fatalf("%d devices are open after two seeks", open)
	}
}
