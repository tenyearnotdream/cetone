package player

// A session owns exactly one output device at a time. Two failures are possible
// and both are audible: a device left open after the session ends keeps playing
// what it holds, and a device opened for a seek that nothing ever closes plays a
// second copy of the song.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// registry records every sink the engine opened and whether it is still open.
type registry struct {
	mu    sync.Mutex
	sinks []*trackedSink
}

func (r *registry) newSink() Sink {
	s := &trackedSink{registry: r, stop: make(chan struct{}), pace: true}
	r.mu.Lock()
	r.sinks = append(r.sinks, s)
	r.mu.Unlock()
	return s
}

func (r *registry) openCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sinks {
		if s.isOpen() {
			n++
		}
	}
	return n
}

func (r *registry) opened() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sinks)
}

type trackedSink struct {
	registry *registry
	mu       sync.Mutex
	open     bool
	stop     chan struct{}
	format   Format
	start    time.Time
	played   int64
	pace     bool
}

func (s *trackedSink) Open(f Format) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open {
		return nil
	}
	s.open = true
	s.format, s.start, s.played = f, time.Now(), 0
	s.stop = make(chan struct{})
	return nil
}

func (s *trackedSink) Close() error {
	s.mu.Lock()
	if !s.open {
		s.mu.Unlock()
		return nil
	}
	s.open = false
	close(s.stop)
	s.mu.Unlock()
	return nil
}

func (s *trackedSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	if !s.open {
		s.mu.Unlock()
		return 0, nil
	}
	if s.pace {
		s.played += int64(len(pcm) / 4)
		due := s.start.Add(time.Duration(float64(s.played) / float64(s.format.SampleRate) * float64(time.Second)))
		stop := s.stop
		s.mu.Unlock()
		if wait := time.Until(due); wait > 0 {
			select {
			case <-stop:
			case <-time.After(wait):
			}
		}
		return len(pcm), nil
	}
	s.mu.Unlock()
	return len(pcm), nil
}

func (s *trackedSink) isOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

func TestASeekLeavesOneDeviceOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeLongWAV(path, 8000, 30); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink
	if err := e.Play(context.Background(), Source{Title: "t", File: path, Duration: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := r.openCount(); got != 1 {
		t.Fatalf("playing opens %d devices, want 1", got)
	}

	e.Seek(15 * time.Second)
	time.Sleep(250 * time.Millisecond)
	if got := r.openCount(); got != 1 {
		t.Fatalf("after a seek %d devices are open, want 1", got)
	}
	if got := r.opened(); got < 2 {
		t.Fatalf("the seek reopened nothing (%d devices ever opened)", got)
	}

	e.Stop()
	time.Sleep(250 * time.Millisecond)
	if got := r.openCount(); got != 0 {
		t.Fatalf("after the session stopped %d devices are still open", got)
	}
}

func TestSwitchingSongsClosesTheOldDevice(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.wav")
	second := filepath.Join(dir, "b.wav")
	if err := writeLongWAV(first, 8000, 30); err != nil {
		t.Fatal(err)
	}
	if err := writeLongWAV(second, 8000, 30); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink

	if err := e.Play(context.Background(), Source{Title: "a", File: first, Duration: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)

	// The next song replaces the session.
	if err := e.Play(context.Background(), Source{Title: "b", File: second, Duration: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := r.openCount(); got != 1 {
		t.Fatalf("after switching songs %d devices are open; the old one keeps playing", got)
	}

	e.Stop()
	time.Sleep(250 * time.Millisecond)
	if got := r.openCount(); got != 0 {
		t.Fatalf("after stopping %d devices are still open", got)
	}
}
