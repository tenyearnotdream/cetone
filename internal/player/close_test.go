package player

// A pause must not look like a song ending. The regression: closing a session
// made its read loop return "end of source", which reported OnFinish and made
// the application start the next track when the user pressed pause.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPauseDoesNotReportTheEndOfTheSong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 5)
	e, _ := newTestEngine(t)
	ended := make(chan struct{}, 4)
	e.OnFinish = func() { ended <- struct{}{} }

	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	e.Pause()

	select {
	case <-ended:
		t.Fatal("pausing reported the end of the song")
	case <-time.After(500 * time.Millisecond):
	}
	if status := e.Snapshot().Status; status != StatusPaused {
		t.Fatalf("the status is %v after Pause", status)
	}
}

func TestSkippingDoesNotReportTheEndOfTheSong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 5)
	e, _ := newTestEngine(t)
	ended := make(chan struct{}, 4)
	e.OnFinish = func() { ended <- struct{}{} }

	if err := e.Play(context.Background(), Source{Title: "one", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	// Replacing the source is what the application's next/previous does.
	if err := e.Play(context.Background(), Source{Title: "two", File: path}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
		t.Fatal("replacing a source reported the end of the previous one")
	case <-time.After(500 * time.Millisecond):
	}
	if status := e.Snapshot().Status; status != StatusPlaying {
		t.Fatalf("the status is %v after replacing the source", status)
	}
}

func TestTheEndOfASongIsStillReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 0.3)
	e, _ := newTestEngine(t)
	ended := make(chan struct{}, 4)
	e.OnFinish = func() { ended <- struct{}{} }
	if err := e.Play(context.Background(), Source{Title: "short", File: path}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("a song that reached its end reported nothing")
	}
	if status := e.Snapshot().Status; status != StatusStopped {
		t.Fatalf("the status after the end is %v", status)
	}
}

func TestPauseThenResumeContinuesTheSameTrack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 4)
	e, sink := newTestEngine(t)
	ended := make(chan struct{}, 4)
	e.OnFinish = func() { ended <- struct{}{} }
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	e.Pause()
	paused := e.Snapshot().Position
	time.Sleep(100 * time.Millisecond)
	if err := e.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if after := e.Snapshot().Position; after <= paused {
		t.Fatalf("the position did not continue: %v then %v", paused, after)
	}
	select {
	case <-ended:
		t.Fatal("pause/resume reported an end")
	default:
	}
	if sink.bytes() == 0 {
		t.Fatal("nothing reached the sink")
	}
}
