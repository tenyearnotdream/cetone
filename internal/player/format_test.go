package player

// The tiers above lossless are served at 96 kHz. The output path must accept that
// rate rather than reject the file as an unsupported format.

import "testing"

func TestTheDeviceFormatAcceptsHighSampleRates(t *testing.T) {
	for _, format := range []Format{
		{SampleRate: 44100, Channels: 2},
		{SampleRate: 48000, Channels: 2},
		{SampleRate: 96000, Channels: 2}, // the surround tier is served at this rate
		{SampleRate: 192000, Channels: 2},
	} {
		if !format.valid() {
			t.Errorf("%d Hz %d ch is rejected by the output", format.SampleRate, format.Channels)
		}
	}
	for _, format := range []Format{
		{SampleRate: 0, Channels: 2},
		{SampleRate: 44100, Channels: 0},
		{SampleRate: 44100, Channels: 6}, // a layout this client does not downmix
	} {
		if format.valid() {
			t.Errorf("%d Hz %d ch is accepted but cannot be played", format.SampleRate, format.Channels)
		}
	}
}
