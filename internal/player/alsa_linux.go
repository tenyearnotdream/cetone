//go:build linux && (amd64 || arm64)

package player

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ALSA constants (alsa/pcm.h).
const (
	alsaPlayback      = 0
	alsaAccessRWInter = 3 // SND_PCM_ACCESS_RW_INTERLEAVED
	alsaFormatS16LE   = 2 // SND_PCM_FORMAT_S16_LE
	alsaRecoverRetry  = 4
)

// alsaRingBytes is how much sound is handed to the device at a time. The
// device holds it; a smaller buffer answers a pause sooner.
const (
	alsaBufferMicros = 120000
	alsaPeriodMicros = 20000
)

// alsaAPI is the subset of libasound this player calls.
type alsaAPI struct {
	open         func(pcm *uintptr, name *byte, stream int32, mode int32) int32
	close        func(pcm uintptr) int32
	drop         func(pcm uintptr) int32
	prepare      func(pcm uintptr) int32
	recover      func(pcm uintptr, err int32, silent int32) int32
	writei       func(pcm uintptr, buf unsafe.Pointer, frames uintptr) int64
	strerror     func(err int32) *byte
	paramsMalloc func(params *uintptr) int32
	paramsFree   func(params uintptr)
	paramsAny    func(pcm uintptr, params uintptr) int32
	setAccess    func(pcm uintptr, params uintptr, access int32) int32
	setFormat    func(pcm uintptr, params uintptr, format int32) int32
	setChannels  func(pcm uintptr, params uintptr, channels uint32) int32
	setRate      func(pcm uintptr, params uintptr, rate uint32, dir int32) int32
	setRateNear  func(pcm uintptr, params uintptr, rate *uint32, dir *int32) int32
	setBufNear   func(pcm uintptr, params uintptr, us *uint32, dir *int32) int32
	setPerNear   func(pcm uintptr, params uintptr, us *uint32, dir *int32) int32
	params       func(pcm uintptr, params uintptr) int32
}

var (
	alsaOnce    sync.Once
	alsaAPIv    *alsaAPI
	alsaLibrary uintptr
	alsaLoadErr error
)

func loadALSA() (*alsaAPI, uintptr, error) {
	alsaOnce.Do(func() {
		var library uintptr
		var err error
		for _, name := range []string{"libasound.so.2", "libasound.so"} {
			library, err = purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL)
			if err == nil {
				break
			}
		}
		if library == 0 {
			alsaLoadErr = fmt.Errorf("%w: load libasound", ErrNoDevice)
			return
		}
		api := &alsaAPI{}
		purego.RegisterLibFunc(&api.open, library, "snd_pcm_open")
		purego.RegisterLibFunc(&api.close, library, "snd_pcm_close")
		purego.RegisterLibFunc(&api.drop, library, "snd_pcm_drop")
		purego.RegisterLibFunc(&api.prepare, library, "snd_pcm_prepare")
		purego.RegisterLibFunc(&api.recover, library, "snd_pcm_recover")
		purego.RegisterLibFunc(&api.writei, library, "snd_pcm_writei")
		purego.RegisterLibFunc(&api.strerror, library, "snd_strerror")
		purego.RegisterLibFunc(&api.paramsMalloc, library, "snd_pcm_hw_params_malloc")
		purego.RegisterLibFunc(&api.paramsFree, library, "snd_pcm_hw_params_free")
		purego.RegisterLibFunc(&api.paramsAny, library, "snd_pcm_hw_params_any")
		purego.RegisterLibFunc(&api.setAccess, library, "snd_pcm_hw_params_set_access")
		purego.RegisterLibFunc(&api.setFormat, library, "snd_pcm_hw_params_set_format")
		purego.RegisterLibFunc(&api.setChannels, library, "snd_pcm_hw_params_set_channels")
		purego.RegisterLibFunc(&api.setRate, library, "snd_pcm_hw_params_set_rate")
		purego.RegisterLibFunc(&api.setRateNear, library, "snd_pcm_hw_params_set_rate_near")
		purego.RegisterLibFunc(&api.setBufNear, library, "snd_pcm_hw_params_set_buffer_time_near")
		purego.RegisterLibFunc(&api.setPerNear, library, "snd_pcm_hw_params_set_period_time_near")
		purego.RegisterLibFunc(&api.params, library, "snd_pcm_hw_params")
		alsaAPIv, alsaLibrary = api, library
	})
	return alsaAPIv, alsaLibrary, alsaLoadErr
}

// alsaSink plays through ALSA's "default" PCM, which on a desktop is the sound
// server (PipeWire or PulseAudio) or dmix/plug: the plugin converts the
// stream's format and rate to what the hardware takes.
//
// The write blocks, so the device itself paces the decoder: the engine never
// needs a ring buffer or a timer, and the reported position stays close to what
// the speakers play.
type alsaSink struct {
	api    *alsaAPI
	pcm    uintptr
	frame  int
	mu     sync.Mutex // serialises write and close
	closed bool
}

func (s *alsaSink) Open(format Format) error {
	api, _, err := loadALSA()
	if err != nil {
		return err
	}
	name := []byte("default\x00")
	var pcm uintptr
	if status := api.open(&pcm, &name[0], alsaPlayback, 0); status < 0 {
		return fmt.Errorf("%w: open the ALSA default device: %s", ErrNoDevice, alsaErrorString(api, status))
	}
	if err := configureALSA(api, pcm, format); err != nil {
		api.close(pcm)
		return err
	}
	s.api, s.pcm, s.frame = api, pcm, format.Channels*2
	return nil
}

func configureALSA(api *alsaAPI, pcm uintptr, format Format) error {
	var params uintptr
	if status := api.paramsMalloc(&params); status < 0 {
		return fmt.Errorf("player: allocate ALSA parameters: %s", alsaErrorString(api, status))
	}
	defer api.paramsFree(params)
	fail := func(step string, status int32) error {
		return fmt.Errorf("player: %s: %s", step, alsaErrorString(api, status))
	}
	if status := api.paramsAny(pcm, params); status < 0 {
		return fail("read the ALSA device parameters", status)
	}
	if status := api.setAccess(pcm, params, alsaAccessRWInter); status < 0 {
		return fail("set interleaved access", status)
	}
	if status := api.setFormat(pcm, params, alsaFormatS16LE); status < 0 {
		return fail("set 16-bit PCM", status)
	}
	if status := api.setChannels(pcm, params, uint32(format.Channels)); status < 0 {
		return fail("set the channel count", status)
	}
	if status := api.setRate(pcm, params, uint32(format.SampleRate), 0); status < 0 {
		rate := uint32(format.SampleRate)
		var dir int32
		if near := api.setRateNear(pcm, params, &rate, &dir); near < 0 || rate != uint32(format.SampleRate) {
			return fail("set the sample rate", status)
		}
	}
	// A small buffer keeps a pause and a seek responsive; a device that refuses
	// the size keeps its own, which is only a latency difference.
	if api.setBufNear != nil {
		buffer := uint32(alsaBufferMicros)
		var dir int32
		api.setBufNear(pcm, params, &buffer, &dir)
	}
	if api.setPerNear != nil {
		period := uint32(alsaPeriodMicros)
		var dir int32
		api.setPerNear(pcm, params, &period, &dir)
	}
	if status := api.params(pcm, params); status < 0 {
		return fail("apply the ALSA parameters", status)
	}
	if status := api.prepare(pcm); status < 0 {
		return fail("prepare the ALSA device", status)
	}
	return nil
}

func (s *alsaSink) Write(pcm []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pcm == 0 {
		return 0, ErrNoDevice
	}
	frame := s.frame
	frames := len(pcm) / frame
	offset := 0
	for offset < frames {
		written := s.api.writei(s.pcm, unsafe.Pointer(&pcm[offset*frame]), uintptr(frames-offset))
		if written > 0 {
			offset += int(written)
			continue
		}
		if written == 0 {
			runtime.Gosched()
			continue
		}
		recovered := false
		for range alsaRecoverRetry {
			if status := s.api.recover(s.pcm, int32(written), 1); status < 0 {
				if s.closed {
					return offset * frame, ErrNoDevice
				}
				return offset * frame, fmt.Errorf("player: play on the ALSA device: %s", alsaErrorString(s.api, status))
			}
			written = s.api.writei(s.pcm, unsafe.Pointer(&pcm[offset*frame]), uintptr(frames-offset))
			if written >= 0 {
				offset += int(written)
				recovered = true
				break
			}
		}
		if !recovered {
			if s.closed {
				return offset * frame, ErrNoDevice
			}
			return offset * frame, fmt.Errorf("player: the ALSA device stopped accepting sound: %s", alsaErrorString(s.api, int32(written)))
		}
	}
	return len(pcm), nil
}

func (s *alsaSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pcm != 0 {
		s.api.drop(s.pcm)
		s.api.close(s.pcm)
		s.pcm = 0
	}
	s.closed = true
	return nil
}

func alsaErrorString(api *alsaAPI, status int32) string {
	if message := api.strerror(status); message != nil {
		return goString(message)
	}
	return fmt.Sprintf("status %d", status)
}

func goString(text *byte) string {
	if text == nil {
		return ""
	}
	var length int
	for ptr := unsafe.Pointer(text); *(*byte)(unsafe.Add(ptr, length)) != 0; length++ {
	}
	return string(unsafe.Slice(text, length))
}
