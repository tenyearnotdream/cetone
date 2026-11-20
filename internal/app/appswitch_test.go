package app

// Switching songs is the path a listener uses most: the old song must stop when
// the new one starts, with no second copy of either left running.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

type openCounter struct {
	mu    sync.Mutex
	open  int
	total int
}

func (o *openCounter) newSink() player.Sink {
	s := &countedSink{counter: o, stop: make(chan struct{})}
	o.mu.Lock()
	o.open++
	o.total++
	o.mu.Unlock()
	return s
}

func (o *openCounter) state() (open, total int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.open, o.total
}

type countedSink struct {
	counter *openCounter
	mu      sync.Mutex
	closed  bool
	stop    chan struct{}
	format  player.Format
	start   time.Time
	played  int64
}

func (s *countedSink) Open(f player.Format) error {
	s.mu.Lock()
	if s.closed {
		s.closed = false
		s.stop = make(chan struct{})
	}
	s.format, s.start, s.played = f, time.Now(), 0
	s.mu.Unlock()
	return nil
}

func (s *countedSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.stop)
	s.counter.mu.Lock()
	s.counter.open--
	s.counter.mu.Unlock()
	return nil
}

func (s *countedSink) Write(pcm []byte) (int, error) {
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
	return len(pcm), nil
}

func TestSwitchingSongsLeavesOneDevice(t *testing.T) {
	a := testApp(t)
	dir := filepath.Join(a.paths.Cache, "music")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	songs := make([]ncm.Song, 3)
	for i := range songs {
		id := int64(9500 + i)
		songs[i] = ncm.Song{ID: id, Name: "song-" + string(rune('a'+i))}
		songs[i].Duration = 20 * time.Second
		quality := a.songQuality(songs[i])
		if err := writeTestWAV(a.cache.Path(id, quality), 8000, 20); err != nil {
			t.Fatal(err)
		}
	}
	counter := &openCounter{}
	a.engine.NewSink = counter.newSink
	a.queue = songs
	a.queueAt = 0

	for i := range songs {
		a.playAt(i)
		// The switch is not done when something plays: it is done when the engine
		// reports *this* song. Waiting for "playing" alone passed while the
		// previous song was still the one playing, which is the bug itself.
		deadline := time.Now().Add(5 * time.Second)
		switched := false
		for time.Now().Before(deadline) {
			a.sync()
			if s := a.engine.Snapshot(); s.Title == songs[i].Name && s.Position > 100*time.Millisecond {
				switched = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !switched {
			t.Fatalf("song %d never started playing (engine says %q)", i, a.engine.Snapshot().Title)
		}
		open, total := counter.state()
		t.Logf("playing %q: %d device(s) open, %d opened so far", songs[i].Name, open, total)
		if open != 1 {
			t.Fatalf("after switching to %q, %d devices are open", songs[i].Name, open)
		}
	}
	a.engine.Stop()
	time.Sleep(300 * time.Millisecond)
	if open, _ := counter.state(); open != 0 {
		t.Fatalf("%d devices are still open after stopping", open)
	}
}
