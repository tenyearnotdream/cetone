package player

// A seek must land where it was asked to, and the sound must come back at once.
//
// The old path rebuilt the decoder at the beginning of the source and read
// forward to the target: on a 58 MB FLAC at 96 kHz that went silent for 1.26 s,
// which is what made dragging the progress bar feel broken.

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// pacedSink behaves like an output device: it blocks until the sound it has been
// given could have been played. The engine relies on that for its pace, so a sink
// that accepted everything instantly would let the session run far ahead of the
// clock it reports.
type pacedSink struct {
	mu     sync.Mutex
	format Format
	start  time.Time
	played int64
	last   time.Time
	stop   chan struct{}
}

func (s *pacedSink) Open(format Format) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil {
		s.stop = make(chan struct{})
	}
	s.format = format
	// A reopened device starts its clock again: the frames counted before belong
	// to the buffer that was just thrown away, and a closed sink's stop channel is
	// replaced, or every later write would return at once and the session would
	// run at the speed of the decoder.
	s.start = time.Now()
	s.played = 0
	return nil
}

func (s *pacedSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	frames := int64(len(pcm) / s.format.FrameSize())
	s.played += frames
	due := s.start.Add(framesToDuration(s.played, s.format.SampleRate))
	s.mu.Unlock()
	if wait := time.Until(due); wait > 0 {
		select {
		case <-s.stop:
		case <-time.After(wait):
		}
	}
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()
	return len(pcm), nil
}

func (s *pacedSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	return nil
}

func (s *pacedSink) lastWrite() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func TestASeekLandsAndTheSoundReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeLongWAV(path, 44100, 30); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	defer e.Close()
	sink := &pacedSink{}
	e.NewSink = func() Sink { return sink }
	if err := e.Play(context.Background(), Source{Title: "tone", File: path, Duration: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)

	for _, at := range []time.Duration{5 * time.Second, 15 * time.Second, 25 * time.Second} {
		before := sink.lastWrite()
		start := time.Now()
		e.Seek(at)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if sink.lastWrite().After(before) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		gap := time.Since(start)
		if gap > 500*time.Millisecond {
			t.Errorf("a seek to %v left the device silent for %v", at, gap.Round(time.Millisecond))
		}
		pos := e.Snapshot().Position
		if pos < at-500*time.Millisecond || pos > at+2*time.Second {
			t.Errorf("a seek to %v reports %v", at, pos.Round(time.Millisecond))
		}
		t.Logf("seek to %-4v: sound back in %v, position %v", at,
			gap.Round(time.Millisecond), pos.Round(time.Millisecond))
	}
}

// The decoder itself lands on the sample it was given.
func TestWAVSeekSampleLands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := writeLongWAV(path, 44100, 10); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec, err := openDecoder(f)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := dec.(*wavDecoder)
	if !ok {
		t.Fatalf("not a wav decoder: %T", dec)
	}
	if err := d.SeekSample(5 * 44100); err != nil {
		t.Fatal(err)
	}
	pos, _ := f.Seek(0, 1)
	want := d.start + int64(5*44100*4)
	if pos != want {
		t.Fatalf("the reader is at %d, want %d", pos, want)
	}
	if n, err := dec.Read(make([]byte, 4096)); n == 0 {
		t.Fatalf("nothing read after the seek (err=%v)", err)
	}
}

// writeLongWAV writes silent 16-bit stereo PCM of the given length.
func writeLongWAV(path string, rate, seconds int) error {
	data := make([]byte, rate*seconds*4)
	var header []byte
	header = append(header, "RIFF"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(36+len(data)))
	header = append(header, "WAVEfmt "...)
	header = binary.LittleEndian.AppendUint32(header, 16)
	header = binary.LittleEndian.AppendUint16(header, 1)
	header = binary.LittleEndian.AppendUint16(header, 2)
	header = binary.LittleEndian.AppendUint32(header, uint32(rate))
	header = binary.LittleEndian.AppendUint32(header, uint32(rate*4))
	header = binary.LittleEndian.AppendUint16(header, 4)
	header = binary.LittleEndian.AppendUint16(header, 16)
	header = append(header, "data"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(len(data)))
	return os.WriteFile(path, append(header, data...), 0o644)
}
