package player

// The reported position must not run ahead of what has been played. A bar that
// jumps forward while the sound lags is the same symptom as a bar that starts
// part-way in.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestThePositionDoesNotRunAhead(t *testing.T) {
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
	for i := 0; i < 6; i++ {
		time.Sleep(200 * time.Millisecond)
		pos := e.Snapshot().Position
		t.Logf("t+%vms: position %v", (i+1)*200, pos.Round(time.Millisecond))
		// After a second of real time, a fresh song must be near 1s, not 40.
		if pos > time.Duration(i+1)*200*time.Millisecond+2*time.Second {
			t.Fatalf("after %dms the position is %v", (i+1)*200, pos.Round(time.Millisecond))
		}
	}
}

func TestAPositionAfterASeekDoesNotRunAhead(t *testing.T) {
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
	time.Sleep(200 * time.Millisecond)
	e.Seek(30 * time.Second)
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		pos := e.Snapshot().Position
		t.Logf("after seeking to 30s, +%vms: %v", (i+1)*200, pos.Round(time.Millisecond))
		limit := 30*time.Second + time.Duration(i+1)*200*time.Millisecond + 2*time.Second
		if pos > limit {
			t.Fatalf("the position ran to %v, past %v", pos.Round(time.Millisecond), limit)
		}
	}
}
