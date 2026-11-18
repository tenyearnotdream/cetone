package player

import (
	"fmt"

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
type deviceSink struct {
	out *audio.Output
}

func (s *deviceSink) Open(format Format) error {
	out, err := audio.Open(audio.Format{SampleRate: format.SampleRate, Channels: format.Channels})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	s.out = out
	return nil
}

func (s *deviceSink) Write(pcm []byte) (int, error) {
	if s.out == nil {
		return 0, ErrNoDevice
	}
	return s.out.Write(pcm)
}

func (s *deviceSink) Close() error {
	if s.out == nil {
		return nil
	}
	out := s.out
	s.out = nil
	return out.Close()
}
