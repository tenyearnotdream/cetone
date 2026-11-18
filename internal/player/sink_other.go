//go:build !linux || (!amd64 && !arm64)

package player

import "fmt"

// alsaSink is absent on this platform: the player reports no device and the
// window still runs, with the position advancing and no sound.
type alsaSink struct{}

func (s *alsaSink) Open(Format) error {
	return fmt.Errorf("%w: this platform has no audio backend", ErrNoDevice)
}
func (s *alsaSink) Write([]byte) (int, error) {
	return 0, ErrNoDevice
}
func (s *alsaSink) Close() error { return nil }
