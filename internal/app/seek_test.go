package app

// Seeking is the transport's most direct promise: the bar shows where the song
// is, and moving it moves the song. It goes through the slider so a drag, a click
// on the track and the arrow keys all work.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/player"
)

func TestSeekingMovesTheTransport(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeTestWAV(path, 8000, 20); err != nil {
		t.Fatal(err)
	}
	if err := a.engine.Play(context.Background(), player.Source{
		Title: "tone", File: path, Duration: 20 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Stop)

	// What the bar's release does.
	a.seekTo(12 * time.Second)
	if got := a.engine.Snapshot().Position; got < 11*time.Second || got > 13*time.Second {
		t.Fatalf("after seeking to 12s the transport reports %v", got)
	}

	// And the seek a stream cannot serve to the byte must not report an error.
	a.engine.Seek(0)
	if got := a.engine.Snapshot().Position; got > time.Second {
		t.Fatalf("seeking to the start left the transport at %v", got)
	}
}
