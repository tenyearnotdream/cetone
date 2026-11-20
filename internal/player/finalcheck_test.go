package player

// The end-to-end case on a real 58 MB FLAC: drag, then switch to another song, and
// check the device count and position at every step. Any second device left open
// is a second copy of the sound.

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRealFileDragThenSwitch(t *testing.T) {
	if _, err := os.Stat("/tmp/flac-sample.flac"); err != nil {
		t.Skip("no sample")
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink

	if err := e.Play(context.Background(), Source{Title: "a", File: "/tmp/flac-sample.flac", Duration: 141 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if open := r.openCount(); open != 1 {
		t.Fatalf("after playing: %d devices open", open)
	}

	// A drag across the bar.
	for _, at := range []time.Duration{30 * time.Second, 100 * time.Second, 60 * time.Second, 130 * time.Second} {
		e.Seek(at)
		time.Sleep(120 * time.Millisecond)
		if open := r.openCount(); open != 1 {
			t.Fatalf("after dragging to %v: %d devices open", at, open)
		}
		pos := e.Snapshot().Position
		if pos < at || pos > at+2*time.Second {
			t.Fatalf("dragged to %v, position reads %v", at, pos.Round(time.Millisecond))
		}
		t.Logf("dragged to %-5v: position %v, %d device(s)", at, pos.Round(time.Millisecond), r.openCount())
	}

	// Now switch songs: the old device must go.
	if err := e.Play(context.Background(), Source{Title: "b", File: "/tmp/flac-sample.flac", Duration: 141 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if open := r.openCount(); open != 1 {
		t.Fatalf("after switching songs: %d devices open", open)
	}
	if title := e.Snapshot().Title; title != "b" {
		t.Fatalf("the engine says %q", title)
	}
	t.Logf("after switching: %d device(s), title %q", r.openCount(), e.Snapshot().Title)

	e.Stop()
	time.Sleep(400 * time.Millisecond)
	if open := r.openCount(); open != 0 {
		t.Fatalf("after stopping: %d devices still open — the last song keeps playing", open)
	}
	t.Logf("after stopping: 0 devices, %d opened in total", r.opened())
}
