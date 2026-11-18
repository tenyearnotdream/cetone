package app

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vivaui/viva/app"

	"ncmviva/internal/player"
)

// The test support: a wav fixture, a sink that counts what it was handed, and a
// wait that fails rather than hanging.

type playerSink = player.Sink

// countingSink is an audio output that records the bytes it played and, unless
// told otherwise, consumes them at the stream's own rate, as a device does. A
// sink that returned immediately would decode a two-second file in a
// millisecond and make the position meaningless.
type countingSink struct {
	written  int
	opened   bool
	format   player.Format
	realtime bool
}

func (s *countingSink) Open(format player.Format) error {
	s.opened = true
	s.format = format
	s.realtime = true
	return nil
}

func (s *countingSink) Write(pcm []byte) (int, error) {
	s.written += len(pcm)
	if s.realtime && s.format.SampleRate > 0 {
		seconds := float64(len(pcm)) / float64(s.format.SampleRate*s.format.Channels*2)
		time.Sleep(time.Duration(seconds * float64(time.Second)))
	}
	return len(pcm), nil
}

func (s *countingSink) Close() error { return nil }

// writeTestWav writes a silent PCM wav, which the player sniffs by its RIFF
// header rather than by the file's name.
func writeTestWav(t *testing.T, path string, rate, channels int, seconds float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	frames := int(float64(rate) * seconds)
	data := make([]byte, frames*channels*2)
	for i := 0; i+1 < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], uint16(int16(600)))
	}
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(data)))
	copy(header[8:], "WAVE")
	copy(header[12:], "fmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(data)))
	if err := os.WriteFile(path, append(header, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pumpUntil repaints and runs the window-thread closures until condition holds.
// It is how a test waits for a request: a driver only repaints when it is asked
// to, and a worker's wake is consumed by Pump.
func pumpUntil(t *testing.T, driver *app.Driver, a *App, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if err := driver.Pump(); err != nil {
			t.Fatal(err)
		}
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the condition did not hold in time")
}

// waitFor waits until condition holds, failing after a few seconds. It runs the
// window-thread closures a worker published, since a test has no window to do it.
func waitFor(t *testing.T, a *App, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the condition did not hold in time")
}
