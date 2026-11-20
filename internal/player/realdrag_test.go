package player

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// What the listener experiences while dragging: every position asked for is
// reported, the sound follows, and no fragment of the old place is heard.
type probeSink struct {
	mu     sync.Mutex
	format Format
	start  time.Time
	played int64
	stop   chan struct{}
	// writes records the time of each accepted chunk, to see the silence between
	// a seek and the sound that follows it.
	writes []time.Time
}

func (s *probeSink) Open(f Format) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		s.stop = make(chan struct{})
	}
	s.format, s.start, s.played = f, time.Now(), 0
	return nil
}
func (s *probeSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	return nil
}
func (s *probeSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
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
	s.mu.Lock()
	s.writes = append(s.writes, time.Now())
	s.mu.Unlock()
	return len(pcm), nil
}

func TestDraggingARealFLAC(t *testing.T) {
	if _, err := os.Stat("/tmp/flac-sample.flac"); err != nil {
		t.Skip("no sample")
	}
	e := NewEngine()
	defer e.Close()
	sink := &probeSink{}
	e.NewSink = func() Sink { return sink }
	if err := e.Play(context.Background(), Source{Title: "s", File: "/tmp/flac-sample.flac", Duration: 141 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)

	// A drag: many positions in quick succession, as a pointer moving across the
	// bar produces them.
	positions := []time.Duration{20 * time.Second, 40 * time.Second, 60 * time.Second, 80 * time.Second, 100 * time.Second}
	for _, at := range positions {
		start := time.Now()
		e.Seek(at)
		reported := e.Snapshot().Position
		if gap := (reported - at).Abs(); gap > 50*time.Millisecond {
			t.Errorf("asked for %v, the bar says %v", at, reported)
		}
		t.Logf("drag to %-5v: bar says %-6v (asked in %v)", at, reported.Round(time.Millisecond),
			time.Since(start).Round(time.Millisecond))
		time.Sleep(120 * time.Millisecond)
	}

	// After the drag, the position keeps advancing from where it was left.
	at := e.Snapshot().Position
	// Give it a second: the check is that playback continued at all, not how fast
	// the clock moved, and a machine running tests under -race is slow.
	time.Sleep(time.Second)
	after := e.Snapshot().Position
	t.Logf("settled at %v, a second later %v", at.Round(time.Millisecond), after.Round(time.Millisecond))
	if after <= at {
		t.Errorf("playback did not continue: %v -> %v", at, after)
	}
	if after > 105*time.Second {
		t.Errorf("playback continued from the wrong place: %v", after)
	}
}

// The device holds up to a fifth of a second of sound. A seek that does not make
// it forget plays a fragment of the old place before the new one, which is heard
// as the song stuttering backwards for a moment after the bar is moved.
//
// The order is what is checked: after a seek there must be a close before the
// next write, because closing is the only thing that empties the buffer.
func TestASeekDiscardsWhatTheDeviceHeld(t *testing.T) {
	if _, err := os.Stat("/tmp/flac-sample.flac"); err != nil {
		t.Skip("no sample")
	}
	e := NewEngine()
	defer e.Close()
	sink := &orderedSink{stop: make(chan struct{})}
	e.NewSink = func() Sink { return sink }
	if err := e.Play(context.Background(), Source{Title: "s", File: "/tmp/flac-sample.flac", Duration: 141 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)

	mark := sink.mark()
	e.Seek(120 * time.Second)
	time.Sleep(500 * time.Millisecond)

	closed, wrote := sink.since(mark)
	t.Logf("after the seek: %d close(s), %d write(s)", closed, wrote)
	if closed == 0 {
		t.Fatal("the device was never closed, so it still held the old place")
	}
	if wrote == 0 {
		t.Fatal("no sound was written after the seek")
	}
	// And the close came before the writes: a close after them would have thrown
	// the new sound away instead.
	if first := sink.firstWriteAfterClose(mark); !first {
		t.Fatal("the device was written to before it was closed")
	}
}

// orderedSink records the order of closes and writes, and keeps the pace of the
// music so the session does not run ahead.
type orderedSink struct {
	mu     sync.Mutex
	format Format
	start  time.Time
	played int64
	stop   chan struct{}
	events []byte // 'c' for a close, 'w' for a write
}

func (s *orderedSink) Open(f Format) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		s.stop = make(chan struct{})
	}
	s.format, s.start, s.played = f, time.Now(), 0
	return nil
}
func (s *orderedSink) Close() error {
	s.mu.Lock()
	if s.stop != nil {
		s.events = append(s.events, 'c')
		close(s.stop)
		s.stop = nil
	}
	s.mu.Unlock()
	return nil
}
func (s *orderedSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	s.events = append(s.events, 'w')
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
func (s *orderedSink) mark() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}
func (s *orderedSink) since(mark int) (closed, wrote int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events[mark:] {
		switch e {
		case 'c':
			closed++
		case 'w':
			wrote++
		}
	}
	return closed, wrote
}

// firstWriteAfterClose reports whether the first event after the mark that is
// either a close or a write is a close.
func (s *orderedSink) firstWriteAfterClose(mark int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events[mark:] {
		switch e {
		case 'c':
			return true
		case 'w':
			return false
		}
	}
	return false
}
