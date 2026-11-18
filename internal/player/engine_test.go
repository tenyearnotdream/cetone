package player

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeWAV writes a sine-free test tone: a constant sample that makes the
// volume scaling easy to check, at the given rate and channels.
func writeWAV(t *testing.T, path string, rate, channels int, seconds float64) {
	t.Helper()
	frames := int(float64(rate) * seconds)
	data := make([]byte, frames*channels*2)
	for i := 0; i+1 < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], uint16(int16(1000)))
	}
	if err := os.WriteFile(path, wavHeader(len(data), rate, channels), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
}

func wavHeader(dataLen, rate, channels int) []byte {
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+dataLen))
	copy(header[8:], "WAVE")
	copy(header[12:], "fmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataLen))
	return header
}

// fakeSink records what the engine played and paces the writer in real time,
// so a session behaves like one on a device without needing a sound card.
type fakeSink struct {
	mu      sync.Mutex
	format  Format
	written int
	closed  bool
	openErr error
}

func (s *fakeSink) Open(format Format) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return s.openErr
	}
	s.format = format
	// A reopened sink takes sound again, as a real device does.
	s.closed = false
	return nil
}

func (s *fakeSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrNoDevice
	}
	format := s.format
	s.written += len(pcm)
	s.mu.Unlock()
	// Sleep as a device would consume the frames, so the clock and the frame
	// count stay in step.
	if format.SampleRate > 0 && format.Channels > 0 {
		time.Sleep(time.Duration(float64(len(pcm)) / float64(format.SampleRate*format.Channels*2) * float64(time.Second)))
	}
	return len(pcm), nil
}

func (s *fakeSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeSink) bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

func newTestEngine(t *testing.T) (*Engine, *fakeSink) {
	t.Helper()
	sink := &fakeSink{}
	e := NewEngine()
	e.NewSink = func() Sink { return sink }
	t.Cleanup(e.Close)
	return e, sink
}

func TestPlaysAndReportsItsLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 0.6)

	e, sink := newTestEngine(t)
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	if got := e.Snapshot().Duration; got < 550*time.Millisecond || got > 650*time.Millisecond {
		t.Fatalf("duration is %v, want about 600ms", got)
	}
	// The engine reports progress while it plays, without a caller's clock.
	time.Sleep(250 * time.Millisecond)
	snap := e.Snapshot()
	if snap.Status != StatusPlaying {
		t.Fatalf("status is %v while playing", snap.Status)
	}
	if snap.Position < 80*time.Millisecond || snap.Position > 400*time.Millisecond {
		t.Fatalf("position after 250ms is %v", snap.Position)
	}
	// The end of the source stops the session and reports it.
	finished := make(chan struct{}, 1)
	e.OnFinish = func() { finished <- struct{}{} }
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the session did not report its end")
	}
	if sink.bytes() == 0 {
		t.Fatal("nothing reached the sink")
	}
	if got := e.Snapshot().Status; got != StatusStopped {
		t.Fatalf("status after the end is %v", got)
	}
}

func TestPauseFreezesThePositionAndResumeContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 2)

	e, _ := newTestEngine(t)
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	e.Pause()
	paused := e.Snapshot()
	if paused.Status != StatusPaused {
		t.Fatalf("status is %v after Pause", paused.Status)
	}
	time.Sleep(150 * time.Millisecond)
	again := e.Snapshot()
	if again.Position != paused.Position {
		t.Fatalf("the position moved while paused: %v then %v", paused.Position, again.Position)
	}
	if err := e.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	resumed := e.Snapshot()
	if resumed.Status != StatusPlaying {
		t.Fatalf("status is %v after Resume", resumed.Status)
	}
	if resumed.Position <= paused.Position {
		t.Fatalf("the position did not continue: %v then %v", paused.Position, resumed.Position)
	}
}

func TestSeekMovesTheDecoder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 4)

	e, sink := newTestEngine(t)
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := e.Seek(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	snap := e.Snapshot()
	if snap.Position < 3*time.Second || snap.Position > 3700*time.Millisecond {
		t.Fatalf("position after seeking to 3s is %v", snap.Position)
	}
	if sink.bytes() == 0 {
		t.Fatal("the sink got nothing after the seek")
	}
}

func TestSeekIsClampedToTheLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 1)

	e, _ := newTestEngine(t)
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	if err := e.Seek(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := e.Snapshot().Position; got > time.Second+50*time.Millisecond {
		t.Fatalf("a seek past the end landed at %v", got)
	}
}

func TestVolumeScalesTheSamples(t *testing.T) {
	e := NewEngine()
	pcm := make([]byte, 8)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(1000)))
	}
	e.SetVolume(0.5)
	e.scaleVolume(pcm)
	for i := 0; i < len(pcm); i += 2 {
		got := int16(binary.LittleEndian.Uint16(pcm[i:]))
		if got != 500 {
			t.Fatalf("sample %d is %d at half volume, want 500", i/2, got)
		}
	}
	e.SetMuted(true)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(1000)))
	}
	e.scaleVolume(pcm)
	for i := 0; i < len(pcm); i += 2 {
		if got := int16(binary.LittleEndian.Uint16(pcm[i:])); got != 0 {
			t.Fatalf("a muted sample is %d, want 0", got)
		}
	}
}

func TestNoDeviceStillRunsTheClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	writeWAV(t, path, 8000, 2, 1)

	e := NewEngine()
	e.NewSink = func() Sink { return &fakeSink{openErr: ErrNoDevice} }
	t.Cleanup(e.Close)
	if err := e.Play(context.Background(), Source{Title: "tone", File: path}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	snap := e.Snapshot()
	if snap.Device {
		t.Fatal("the engine claims a device it could not open")
	}
	if snap.Position < 100*time.Millisecond {
		t.Fatalf("position without a device is %v after 250ms", snap.Position)
	}
}

func TestUnsupportedFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-audio.bin")
	if err := os.WriteFile(path, []byte("this is not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	t.Cleanup(e.Close)
	err := e.Play(context.Background(), Source{Title: "bad", File: path})
	if err == nil {
		t.Fatal("playing a text file did not fail")
	}
	snap := e.Snapshot()
	if snap.Status != StatusFailed || snap.Error == "" {
		t.Fatalf("state after a failure is %v %q", snap.Status, snap.Error)
	}
}
