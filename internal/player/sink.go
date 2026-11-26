package player

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/vivaui/viva/audio"
)

// deviceSink plays on the system's default output through Viva's audio package,
// which has a backend on each desktop platform: ALSA on Linux (through the sound
// server), WASAPI shared mode on Windows, an AudioUnit on macOS. It converts the
// stream to what the device takes, so one implementation covers all three.
//
// A machine with no device, or a backend that is not present for this build,
// reports ErrNoDevice: the engine keeps running without sound and its own
// position keeps moving, so the window, the lyrics and MPRIS still work.
//
// The sink is shared between the session goroutine (which writes) and the window
// thread (which closes it when the user pauses or skips), so every call takes the
// lock: closing twice is normal here, and a write after the close must return the
// closed error rather than reach a released device.
type deviceSink struct {
	mu  sync.Mutex
	out *audio.Output
}

func (s *deviceSink) Open(format Format) error {
	// Open is called before the session starts, with no other user yet, but it
	// takes the lock for the same reason Close does: the sink is one value.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out != nil {
		return errors.New("player: the sink is already open")
	}
	out, err := audio.Open(audio.Format{SampleRate: format.SampleRate, Channels: format.Channels})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	s.out = out
	return nil
}

func (s *deviceSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if out == nil {
		return 0, ErrNoDevice
	}
	// The device is written without holding the lock: a Write blocks while the
	// device holds enough sound, and holding the lock there would stop a pause
	// from closing the sink until the write returned.
	n, err := out.Write(pcm)
	if errors.Is(err, audio.ErrClosed) {
		return n, os.ErrClosed
	}
	return n, err
}

func (s *deviceSink) Close() error {
	s.mu.Lock()
	out := s.out
	s.out = nil
	s.mu.Unlock()
	if out == nil {
		return nil
	}
	return out.Close()
}
