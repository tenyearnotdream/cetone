package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Playback timing constants.
//
// chunkDuration (25 ms) keeps pause/seek/volume updates crisp and minimizes
// blocking latency. maxLead (250 ms) keeps Viva's 200 ms ring buffer and the
// platform sound server comfortably fed so ALSA/PipeWire never underruns
// (xrun -> audible clicks/crackling), while staying short enough that position
// reporting tracks real-time audio closely.
const (
	chunkDuration = 25 * time.Millisecond
	maxLead       = 250 * time.Millisecond
	// gainRampDuration smooths track start/seek fade-in and volume slider steps
	// over ~8 ms to eliminate DC pop clicks and zipper noise.
	gainRampDuration = 8 * time.Millisecond
)

// track owns one playback session: its source reader, decoder, sink, and pump
// goroutine.
type track struct {
	engine *Engine
	source Source

	reader io.ReadSeeker
	closer io.Closer
	dl     *download

	ctx context.Context

	mu      sync.Mutex
	dec     decoder
	sink    Sink
	format  Format
	at      time.Duration
	seeking bool

	submitted atomic.Int64
	decoded   atomic.Int64

	startedAt   time.Time
	currentGain float64

	seekCh   chan time.Duration
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	release  sync.Once
	promoted atomic.Bool
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// newTrack opens a source and sniffs/initializes its decoder. It returns the
// resolved track duration (preferring the container's exact duration when known).
func newTrack(ctx context.Context, e *Engine, source Source, at time.Duration) (*track, time.Duration, error) {
	t := &track{
		engine:  e,
		source:  source,
		ctx:     ctx,
		at:      at,
		seeking: at > 0,
		seekCh:  make(chan time.Duration, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	if source.File != "" && source.Stream == "" {
		file, err := os.Open(source.File)
		if err != nil {
			return nil, 0, fmt.Errorf("player: open %s: %w", source.File, err)
		}
		t.reader, t.closer = file, file
	} else {
		if e.HTTP == nil {
			return nil, 0, fmt.Errorf("player: no http client for a stream")
		}
		part := source.CachePath + ".part"
		if source.CachePath == "" {
			part = filepath.Join(os.TempDir(), fmt.Sprintf("cetone-%d.part", time.Now().UnixNano()))
		}
		dl, err := startDownload(ctx, e.HTTP, source.Stream, part, source.Header)
		if err != nil {
			return nil, 0, err
		}
		tail, err := dl.openTail()
		if err != nil {
			_ = dl.Close()
			return nil, 0, err
		}
		t.dl = dl
		t.reader, t.closer = tail, closerFunc(func() error {
			_ = tail.Close()
			return dl.Close()
		})
	}
	// Promote finished stream to its cache path as soon as the download completes
	// (without waiting for the song to play to the very end).
	if t.dl != nil && source.CachePath != "" {
		go t.promoteCache()
	}
	dec, err := openDecoder(t.reader)
	if err != nil {
		t.close()
		return nil, 0, err
	}
	t.dec = dec
	t.format = dec.Format()
	if !t.format.valid() {
		t.close()
		return nil, 0, fmt.Errorf("player: %s: unusable format %d Hz %d channels", source.Title, t.format.SampleRate, t.format.Channels)
	}
	dur := dec.Duration()
	if dur <= 0 {
		dur = source.Duration
	}
	return t, dur, nil
}

func (t *track) openSink() Sink {
	sink := t.engine.newSink()
	if err := sink.Open(t.format); err != nil {
		t.engine.mu.Lock()
		t.engine.device = false
		t.engine.mu.Unlock()
		return nil
	}
	t.mu.Lock()
	t.sink = sink
	t.mu.Unlock()
	t.engine.mu.Lock()
	t.engine.device = true
	t.engine.mu.Unlock()
	return sink
}

// run drives decoding, seeking, and audio output until EOF, stop, or error.
func (t *track) run() {
	defer close(t.done)
	defer t.close()

	t.mu.Lock()
	dec := t.dec
	t.mu.Unlock()

	var sink Sink

	for {
		// Apply any pending seek before opening/writing to the audio sink.
		if t.takeSeeking() {
			var err error
			dec, sink, err = t.applyPendingSeeks(dec, sink)
			if err != nil {
				if t.isStopped() || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
					return
				}
				t.engine.fail(t, err)
				return
			}
			if t.isStopped() {
				return
			}
		}

		// Lazily open the sink on first start (after any initial at > 0 seek,
		// so PlayAt never opens/flushes/reopens the device needlessly).
		if sink == nil && t.engine.device {
			sink = t.openSink()
			t.startedAt = time.Now()
			t.currentGain = 0 // gentle 8 ms fade-in avoids DC pop on start
		} else if t.startedAt.IsZero() {
			t.startedAt = time.Now()
			t.currentGain = 0
		}

		t.engine.mu.Lock()
		t.engine.serveLocked(StatusPlaying, t)
		t.engine.mu.Unlock()
		t.engine.notify()

		outcome, err := t.pump(dec, sink)
		if err != nil {
			if t.isStopped() || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
				return
			}
			t.engine.fail(t, err)
			return
		}
		switch outcome {
		case pumpStopped:
			return
		case pumpEnded:
			t.promoteCache()
			t.engine.finished(t)
			return
		case pumpRestart:
			// Loop back to apply the new seek target.
		}
	}
}

// applyPendingSeeks positions dec at t.at, collapsing any newer seeks that
// arrived on seekCh while seeking was in flight, flushes the audio device once,
// and lands the new position cleanly.
func (t *track) applyPendingSeeks(dec decoder, sink Sink) (decoder, Sink, error) {
	for {
		t.mu.Lock()
		at := t.at
		t.mu.Unlock()

		want := samplesAt(at, t.format)
		next, have, err := seekSamples(dec, want, t.decoded.Load(), t.format)
		if errors.Is(err, errNoBackwardSeek) {
			if _, seekErr := t.reader.Seek(0, io.SeekStart); seekErr != nil {
				return dec, sink, seekErr
			}
			reopened, openErr := openDecoder(t.reader)
			if openErr != nil {
				return dec, sink, openErr
			}
			t.mu.Lock()
			t.dec = reopened
			t.mu.Unlock()
			next, have, err = seekSamples(reopened, want, 0, t.format)
		}
		if err != nil {
			return dec, sink, err
		}
		dec = next
		t.decoded.Store(have)

		// If a newer seek arrived while decoding/seeking, immediately jump to
		// the latest target before flushing or playing a single stale frame.
		select {
		case newer := <-t.seekCh:
			t.mu.Lock()
			t.at = newer
			t.mu.Unlock()
			continue
		default:
		}

		// Discard any buffered audio from the previous position if the sink is
		// already open (on initial PlayAt, sink is still nil and opens below).
		if sink != nil {
			sink = t.flush(sink)
		} else {
			sink = t.openSink()
		}
		t.currentGain = 0 // 8 ms fade-in eliminates seek discontinuity click
		t.land(at)
		t.mu.Lock()
		t.at = 0
		t.mu.Unlock()
		return dec, sink, nil
	}
}

type pumpOutcome int

const (
	pumpEnded pumpOutcome = iota
	pumpRestart
	pumpStopped
)

func (t *track) pump(dec decoder, sink Sink) (pumpOutcome, error) {
	frameSize := t.format.FrameSize()
	chunk := int(int64(t.format.SampleRate)*int64(chunkDuration)/int64(time.Second)) * frameSize
	if chunk < 2048 {
		chunk = 2048
	}
	if chunk > 16384 {
		chunk = 16384
	}
	chunk -= chunk % frameSize
	buf := make([]byte, chunk)

	for {
		select {
		case at := <-t.seekCh:
			t.scheduleSeek(at)
			return pumpRestart, nil
		case <-t.stop:
			return pumpStopped, nil
		case <-t.ctx.Done():
			return pumpStopped, nil
		default:
		}

		n, err := dec.Read(buf)
		if n > 0 {
			n -= n % frameSize
			if n > 0 {
				frames := int64(n / frameSize)
				t.decoded.Add(frames)
				pcm := buf[:n]
				t.applyGain(pcm)

				if sink != nil {
					if _, werr := sink.Write(pcm); werr != nil {
						if errors.Is(werr, os.ErrClosed) || t.isStopped() {
							return pumpStopped, nil
						}
						return pumpStopped, werr
					}
				}

				submitted := t.submitted.Add(frames)

				// Guard against clock drift after a network stall: if real time
				// outran everything previously written (buffer ran dry), re-anchor
				// startedAt and engine's clock to the frames actually played so
				// we never burst audio or leap the progress bar forward.
				writtenDur := framesToDuration(submitted, t.format.SampleRate)
				prevWritten := framesToDuration(submitted-frames, t.format.SampleRate)
				if elapsed := time.Since(t.startedAt); elapsed > prevWritten+50*time.Millisecond {
					t.startedAt = time.Now().Add(-prevWritten)
					t.engine.syncClock(t, prevWritten)
				}

				if wait := t.paceWait(writtenDur); wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case at := <-t.seekCh:
						timer.Stop()
						t.scheduleSeek(at)
						return pumpRestart, nil
					case <-t.stop:
						timer.Stop()
						return pumpStopped, nil
					case <-t.ctx.Done():
						timer.Stop()
						return pumpStopped, nil
					case <-timer.C:
					}
				}
			}
			if t.dl != nil {
				if written, total := t.dl.progress(); total > 0 {
					t.engine.setBuffered(float64(written) / float64(total))
				}
			}
		}

		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				return t.awaitPlayedOut()
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrClosed):
				return pumpStopped, nil
			}
			return pumpStopped, err
		}
	}
}

// applyGain scales signed 16-bit LE PCM frames in place, smoothly ramping from
// t.currentGain to the engine's target gain over gainRampDuration to prevent
// clicks on track start, seeks, mute toggles, and volume drags.
func (t *track) applyGain(pcm []byte) {
	target := t.engine.targetGain()
	cur := t.currentGain
	if cur == 1 && target == 1 {
		return
	}
	channels := t.format.Channels
	if channels < 1 {
		channels = 2
	}
	frameSize := channels * 2
	frames := len(pcm) / frameSize
	if frames == 0 {
		return
	}
	rampFrames := int(int64(t.format.SampleRate) * int64(gainRampDuration) / int64(time.Second))
	if rampFrames < 1 {
		rampFrames = 1
	}

	if math.Abs(target-cur) < 1e-6 {
		t.currentGain = target
		if target == 0 {
			clear(pcm)
			return
		}
		for i := 0; i+1 < len(pcm); i += 2 {
			sample := int32(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
			scaled := clampInt64ToInt16(int64(math.Round(float64(sample) * target)))
			pcm[i] = byte(scaled)
			pcm[i+1] = byte(scaled >> 8)
		}
		return
	}

	step := (target - cur) / float64(rampFrames)
	for f := 0; f < frames; f++ {
		if (step > 0 && cur < target) || (step < 0 && cur > target) {
			cur += step
			if (step > 0 && cur > target) || (step < 0 && cur < target) {
				cur = target
			}
		}
		base := f * frameSize
		for ch := 0; ch < channels; ch++ {
			off := base + ch*2
			sample := int32(int16(uint16(pcm[off]) | uint16(pcm[off+1])<<8))
			scaled := clampInt64ToInt16(int64(math.Round(float64(sample) * cur)))
			pcm[off] = byte(scaled)
			pcm[off+1] = byte(scaled >> 8)
		}
	}
	t.currentGain = cur
}

func (t *track) scheduleSeek(at time.Duration) {
	t.mu.Lock()
	t.at = at
	t.seeking = true
	t.mu.Unlock()
}

func (t *track) seek(at time.Duration) {
	select {
	case t.seekCh <- at:
	default:
		select {
		case <-t.seekCh:
		default:
		}
		select {
		case t.seekCh <- at:
		default:
		}
	}
}

func (t *track) paceWait(written time.Duration) time.Duration {
	if t.format.SampleRate <= 0 {
		return 0
	}
	lead := written - time.Since(t.startedAt)
	if lead <= maxLead {
		return 0
	}
	return lead - maxLead
}

// awaitPlayedOut waits until the buffered audio at the end of a track finishes
// playing before signalling OnFinish, while staying immediately interruptible.
func (t *track) awaitPlayedOut() (pumpOutcome, error) {
	if t.format.SampleRate <= 0 {
		return pumpEnded, nil
	}
	total := framesToDuration(t.submitted.Load(), t.format.SampleRate)
	for {
		remaining := total - time.Since(t.startedAt)
		if remaining <= 0 {
			return pumpEnded, nil
		}
		if remaining > 20*time.Millisecond {
			remaining = 20 * time.Millisecond
		}
		timer := time.NewTimer(remaining)
		select {
		case at := <-t.seekCh:
			timer.Stop()
			t.scheduleSeek(at)
			return pumpRestart, nil
		case <-t.stop:
			timer.Stop()
			return pumpStopped, nil
		case <-t.ctx.Done():
			timer.Stop()
			return pumpStopped, nil
		case <-timer.C:
		}
	}
}

// promoteCache moves a completed stream download to its permanent cache path.
func (t *track) promoteCache() {
	if t.dl == nil || t.source.CachePath == "" {
		return
	}
	if !t.dl.waitDone(30 * time.Minute) {
		return
	}
	if !t.dl.complete() {
		return
	}
	if t.promoted.Swap(true) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.source.CachePath), 0o755); err != nil {
		return
	}
	if err := os.Rename(t.dl.path, t.source.CachePath); err != nil {
		return
	}
	t.dl.keep.Store(true)
	if t.engine.OnCacheWritten != nil {
		t.engine.OnCacheWritten(t.source.CachePath)
	}
}

func (t *track) isStopped() bool {
	select {
	case <-t.stop:
		return true
	case <-t.ctx.Done():
		return true
	default:
		return false
	}
}

func (t *track) takeSeeking() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.seeking
	t.seeking = false
	return pending
}

// flush closes the current audio device to drop stale buffered audio after a
// seek, then opens a fresh device at the same format.
func (t *track) flush(sink Sink) Sink {
	if sink != nil {
		_ = sink.Close()
	}
	next := t.engine.newSink()
	if err := next.Open(t.format); err != nil {
		t.engine.mu.Lock()
		t.engine.device = false
		t.engine.mu.Unlock()
		t.mu.Lock()
		t.sink = nil
		t.mu.Unlock()
		return nil
	}
	t.engine.mu.Lock()
	t.engine.device = true
	t.engine.mu.Unlock()
	t.mu.Lock()
	t.sink = next
	t.mu.Unlock()
	return next
}

func (t *track) land(at time.Duration) {
	t.submitted.Store(0)
	t.decoded.Store(samplesAt(at, t.format))
	t.startedAt = time.Now()
	t.engine.landSeek(t, at)
}

// Close stops the session, unblocks any blocked sink write or download read,
// and waits for the session goroutine to exit cleanly.
func (t *track) Close() {
	t.stopOnce.Do(func() { close(t.stop) })
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	if sink != nil {
		_ = sink.Close()
	}
	// Also wake any tailFile.Read blocked waiting for slow network bytes.
	if tf, ok := t.reader.(*tailFile); ok {
		_ = tf.Close()
	}
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
}

func (t *track) close() {
	t.release.Do(func() {
		t.mu.Lock()
		sink := t.sink
		t.sink = nil
		closer := t.closer
		t.closer = nil
		t.mu.Unlock()
		if sink != nil {
			_ = sink.Close()
		}
		if closer != nil {
			_ = closer.Close()
		}
	})
}
