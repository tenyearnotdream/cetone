// Package player plays the audio files and streams the application hands it.
//
// It owns at most one decode goroutine and one output device; the UI reads a
// Snapshot and never touches a decoder. Nothing runs while the engine is
// stopped, and nothing polls: an idle window keeps no device and no goroutine.
package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Status is what the transport is doing.
type Status int

const (
	StatusStopped Status = iota
	StatusPlaying
	StatusPaused
	StatusBuffering
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusPlaying:
		return "playing"
	case StatusPaused:
		return "paused"
	case StatusBuffering:
		return "buffering"
	case StatusFailed:
		return "failed"
	}
	return "stopped"
}

// Source is one thing to play: a finished local file, or a URL fetched while
// it plays.
type Source struct {
	// Title names the source in an error message and in the snapshot.
	Title string
	// File is a complete local file.
	File string
	// Stream is a URL downloaded while it plays. When CachePath is set the
	// finished transfer is moved there, so the next play is a File play.
	Stream    string
	CachePath string
	Header    http.Header
	// Duration is the length the API reported; the container's own value wins
	// when it has one.
	Duration time.Duration
}

// Sink is the audio output: one per play session, opened at the stream's
// format, so a sample-rate change between songs needs no special case.
type Sink interface {
	Open(Format) error
	Write([]byte) (int, error)
	Close() error
}

// Snapshot is the engine's state, safe to read from the UI goroutine.
type Snapshot struct {
	Status   Status
	Position time.Duration
	Duration time.Duration
	Volume   float64
	Muted    bool
	Buffered float64
	Title    string
	Error    string
	// Device is false when the machine has no output; the position still moves
	// so the window, the lyrics and MPRIS work.
	Device bool
}

// Engine plays one source at a time.
type Engine struct {
	// HTTP fetches streams. It defaults to http.DefaultClient.
	HTTP *http.Client
	// NewSink builds the output for a session. A test substitutes one that
	// records what it was handed.
	NewSink func() Sink
	// OnChange reports a visible state change, for the window's Invalidator.
	//
	// The three callbacks are read by the session goroutine while it runs, so set
	// them before Play and not again afterwards: assigning one mid-session races
	// with the goroutine that calls it.
	OnChange func()
	// OnFinish reports a session that reached its end.
	OnFinish func()
	// OnCacheWritten reports that a finished stream was moved into the cache.
	// It runs on the session's goroutine, so a receiver must be safe from
	// another thread.
	OnCacheWritten func()

	mu       sync.Mutex
	track    *track
	last     Source
	status   Status
	pos      time.Duration
	duration time.Duration
	title    string
	errMsg   string
	buffered float64
	volume   float64
	muted    bool
	device   bool
	// anchorAt and anchorFrame turn a session's frame count and the clock into
	// the position: the clock when the position was last set, and the frames
	// that had been handed to the device then.
	anchorAt    time.Time
	anchorFrame int64
}

// NewEngine returns an engine at 70% volume with nothing playing.
func NewEngine() *Engine {
	return &Engine{HTTP: http.DefaultClient, volume: 0.7, buffered: 1, device: true}
}

// ----- session -----

// track is one playback session: its decoder, its sink and its goroutine.
type track struct {
	engine *Engine
	source Source

	// reader is the byte source, kept across a seek so a stream is not
	// downloaded twice.
	reader io.ReadSeeker
	closer io.Closer
	dl     *download

	// ctx is the session's own context: its cancellation stops the session the
	// way Close does, without reporting an end of song.
	ctx context.Context

	mu        sync.Mutex
	dec       decoder
	sink      Sink
	format    Format
	at        time.Duration
	submitted atomic.Int64

	seekCh   chan time.Duration
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	release  sync.Once
	// promoted reports that the finished transfer already became the cache
	// entry, so the rename happens once.
	promoted atomic.Bool

	// finishedOnce guards the end-of-source report.
	finishedOnce sync.Once
}

// newTrack opens a source at a position.
func newTrack(ctx context.Context, e *Engine, source Source, at time.Duration) (*track, error) {
	t := &track{
		engine: e,
		source: source,
		ctx:    ctx,
		at:     at,
		seekCh: make(chan time.Duration, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	if source.File != "" && source.Stream == "" {
		file, err := os.Open(source.File)
		if err != nil {
			return nil, fmt.Errorf("player: open %s: %w", source.File, err)
		}
		t.reader, t.closer = file, file
	} else {
		if e.HTTP == nil {
			return nil, fmt.Errorf("player: no http client for a stream")
		}
		part := source.CachePath + ".part"
		if source.CachePath == "" {
			part = filepath.Join(os.TempDir(), fmt.Sprintf("cetone-%d.part", time.Now().UnixNano()))
		}
		dl, err := startDownload(ctx, e.HTTP, source.Stream, part, source.Header)
		if err != nil {
			return nil, err
		}
		tail, err := dl.openTail()
		if err != nil {
			dl.Close()
			return nil, err
		}
		t.dl = dl
		t.reader, t.closer = tail, closerFunc(func() error {
			tail.Close()
			return dl.Close()
		})
	}
	// A stream is promoted as soon as it has arrived, without waiting for the
	// song to end.
	if t.dl != nil && source.CachePath != "" {
		go t.promoteCache()
	}
	dec, err := openDecoder(t.reader)
	if err != nil {
		t.close()
		return nil, err
	}
	t.dec = dec
	t.format = dec.Format()
	if !t.format.valid() {
		t.close()
		return nil, fmt.Errorf("player: %s: unusable format %d Hz %d channels", source.Title, t.format.SampleRate, t.format.Channels)
	}
	if duration, total := trackDuration(t, source); duration > 0 {
		e.mu.Lock()
		e.duration = duration
		e.mu.Unlock()
		_ = total
	}
	return t, nil
}

// trackDuration derives the length from the container.
func trackDuration(t *track, source Source) (time.Duration, int64) {
	if w, ok := t.dec.(*wavDecoder); ok {
		return wavDuration(w), 0
	}
	if f, ok := t.dec.(*flacDecoder); ok {
		if f.stream.Info.NSamples > 0 && f.format.SampleRate > 0 {
			samples := int64(f.stream.Info.NSamples)
			return framesToDuration(samples, f.format.SampleRate), samples
		}
	}
	return source.Duration, 0
}

// openSink opens the output for the session's format, if the machine has one.
func (t *track) openSink() {
	sink := t.engine.newSink()
	if err := sink.Open(t.format); err != nil {
		t.engine.mu.Lock()
		t.engine.device = false
		t.engine.mu.Unlock()
		return
	}
	t.mu.Lock()
	t.sink = sink
	t.mu.Unlock()
	t.engine.mu.Lock()
	t.engine.device = true
	t.engine.mu.Unlock()
}

// run decodes and writes until the source ends, a seek restarts it, or the
// session is stopped.
func (t *track) run() {
	defer close(t.done)
	defer t.close()

	t.openSink()

	for {
		// Everything that settles the session before any byte is read: apply a
		// pending seek, then report that the device is playing.
		t.mu.Lock()
		at := t.at
		dec := t.dec
		sink := t.sink
		t.mu.Unlock()

		if at > 0 {
			if err := discard(dec, at, t.format); err != nil {
				t.engine.fail(t, err)
				return
			}
		}

		t.engine.mu.Lock()
		t.engine.serveLocked(StatusPlaying, t)
		t.engine.mu.Unlock()
		t.engine.notify()

		outcome, err := t.pump(dec, sink)
		if err != nil {
			t.engine.fail(t, err)
			return
		}
		switch outcome {
		case pumpStopped:
			// A stop leaves the source unfinished: its last frames were not
			// played, so it is neither promoted to the cache nor reported as
			// ended. Reporting an end here would advance the queue when the user
			// paused or skipped, which is the opposite of what they asked for.
			return
		case pumpEnded:
			// End of the source. A stream that finished arriving was already
			// promoted the moment it did; this covers the last few bytes.
			t.promoteCache()
			t.engine.finished(t)
			return
		}

		// A seek: rebuild the decoder at the same byte source, from its first
		// byte, so the discard below starts at the beginning of the song rather
		// than where the last read stopped. The sink stays open: one source has
		// one format, so no rate change can happen here.
		t.mu.Lock()
		pending := t.at
		t.mu.Unlock()
		if _, err := t.reader.Seek(0, io.SeekStart); err != nil {
			// A stream that has not finished downloading cannot always rewind;
			// the seek then applies from where the reader is, which is the
			// closest the engine can honestly get.
			_ = err
		}
		dec, err = openDecoder(t.reader)
		if err != nil {
			t.engine.fail(t, err)
			return
		}
		t.mu.Lock()
		t.dec = dec
		t.mu.Unlock()
		t.mu.Lock()
		t.at = pending
		t.mu.Unlock()
	}
}

// pumpOutcome is how one pass over the source ended.
type pumpOutcome int

const (
	// pumpEnded is the source reaching its last frame.
	pumpEnded pumpOutcome = iota
	// pumpRestart is a seek: the decoder starts again at its new position.
	pumpRestart
	// pumpStopped is the session being closed or its context cancelled. It is
	// not the end of the song, and must not move the queue on.
	pumpStopped
)

// pump reads, applies the volume, and writes until the source ends, the session
// stops, or a seek asks for a restart.
func (t *track) pump(dec decoder, sink Sink) (pumpOutcome, error) {
	frameSize := t.format.FrameSize()
	bytesPerSecond := t.format.SampleRate * frameSize
	// A tenth of a second per write keeps a pause, a seek and the position
	// report responsive without making the device starve.
	chunk := bytesPerSecond / 10
	if chunk < 2048 {
		chunk = 2048
	}
	if chunk > 1<<15 {
		chunk = 1 << 15
	}
	chunk -= chunk % frameSize
	buf := make([]byte, chunk)
	for {
		select {
		case at := <-t.seekCh:
			t.mu.Lock()
			t.at = at
			t.engine.mu.Lock()
			t.engine.pos = at
			t.engine.anchorAt = time.Now()
			t.engine.anchorFrame = t.submitted.Load()
			t.engine.mu.Unlock()
			t.mu.Unlock()
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
			pcm := buf[:n]
			t.engine.scaleVolume(pcm)
			if n > 0 && sink != nil {
				if _, werr := sink.Write(pcm); werr != nil {
					if errors.Is(werr, os.ErrClosed) {
						return pumpStopped, nil
					}
					return pumpStopped, werr
				}
			} else if n > 0 {
				// No device: keep real time so the position stays honest.
				wait := time.Duration(float64(n) / float64(bytesPerSecond) * float64(time.Second))
				select {
				case <-t.stop:
					return pumpStopped, nil
				case <-t.ctx.Done():
					return pumpStopped, nil
				case <-time.After(wait):
				}
			}
			t.submitted.Add(int64(n / frameSize))
			if t.dl != nil {
				if written, total := t.dl.progress(); total > 0 {
					t.engine.setBuffered(float64(written) / float64(total))
				}
			}
		}
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				return pumpEnded, nil
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// The session's context ended: the transfer was cancelled, which
				// is a stop, not a song that reached its last frame.
				return pumpStopped, nil
			}
			return pumpStopped, err
		}
	}
}

// seek asks the session to restart at a position.
func (t *track) seek(at time.Duration) {
	select {
	case t.seekCh <- at:
	default:
		// A seek already pending: replace it.
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

// promoteCache moves a finished transfer into its cache name while the session
// keeps reading it, so the song is in the cache seconds after it arrives rather
// than at the end of the track: a user who quits halfway keeps what was
// downloaded.
//
// Only a transfer that delivered exactly what the server promised is promoted. A
// partial file renamed into the cache would be played as the song from then on,
// which is worse than caching nothing.
//
// Renaming a file that is open for reading keeps the handle valid on the
// platforms this client targets, so the reader continues on the same bytes.
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
	// The finished file now lives at its cache name; the session's Close must not
	// think the transfer was abandoned and delete it.
	t.dl.keep.Store(true)
	if t.engine.OnCacheWritten != nil {
		t.engine.OnCacheWritten()
	}
}

// stopping reports that the session was closed rather than reaching its end.
func (t *track) stopping() bool {
	select {
	case <-t.stop:
		return true
	default:
		return false
	}
}

// Close stops the session and waits for its goroutine.
func (t *track) Close() {
	t.stopOnce.Do(func() { close(t.stop) })
	// Closing the sink unblocks a writer parked on a full device buffer.
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	if sink != nil {
		sink.Close()
	}
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
}

// close releases the files. It is safe to call from the goroutine and from
// Close.
func (t *track) close() {
	t.release.Do(func() {
		t.mu.Lock()
		sink := t.sink
		t.sink = nil
		closer := t.closer
		t.closer = nil
		t.mu.Unlock()
		if sink != nil {
			sink.Close()
		}
		if closer != nil {
			closer.Close()
		}
	})
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// discard decodes and throws away frames until at is reached. Scanning beats
// indexing for the sizes the site serves, and it works on a file that is still
// downloading.
func discard(dec decoder, at time.Duration, format Format) error {
	if at <= 0 || format.SampleRate == 0 {
		return nil
	}
	remaining := int64(at.Seconds() * float64(format.SampleRate*format.FrameSize()))
	buf := make([]byte, 32<<10)
	for remaining > 0 {
		n, err := dec.Read(buf)
		if n > 0 {
			remaining -= int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
	return nil
}

// ----- engine -----

// Play starts a source from its beginning, replacing whatever played.
func (e *Engine) Play(ctx context.Context, source Source) error {
	t, err := newTrack(ctx, e, source, 0)
	if err != nil {
		e.mu.Lock()
		e.status = StatusFailed
		e.errMsg = err.Error()
		e.title = source.Title
		e.mu.Unlock()
		e.notify()
		return err
	}
	e.mu.Lock()
	old := e.track
	e.track = t
	e.last = source
	e.pos = 0
	e.title = source.Title
	e.errMsg = ""
	e.buffered = 1
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	e.status = StatusPlaying
	if t.dl != nil {
		e.buffered = 0
	}
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go t.run()
	e.notify()
	return nil
}

// Resume plays the last source again from where it paused.
func (e *Engine) Resume(ctx context.Context) error {
	e.mu.Lock()
	if e.status != StatusPaused {
		e.mu.Unlock()
		return nil
	}
	source, at := e.last, e.pos
	e.mu.Unlock()
	t, err := newTrack(ctx, e, source, at)
	if err != nil {
		e.mu.Lock()
		e.errMsg = err.Error()
		e.status = StatusFailed
		e.mu.Unlock()
		e.notify()
		return err
	}
	e.mu.Lock()
	e.track = t
	e.status = StatusPlaying
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	e.mu.Unlock()
	go t.run()
	e.notify()
	return nil
}

// Pause freezes the position and releases the device.
func (e *Engine) Pause() {
	e.mu.Lock()
	if e.status != StatusPlaying && e.status != StatusBuffering {
		e.mu.Unlock()
		return
	}
	t := e.track
	e.pos = e.positionLocked()
	e.track = nil
	e.status = StatusPaused
	e.mu.Unlock()
	if t != nil {
		t.Close()
	}
	e.notify()
}

// Toggle pauses a playing source or resumes a paused one.
func (e *Engine) Toggle(ctx context.Context) {
	if e.Snapshot().Status == StatusPaused {
		_ = e.Resume(ctx)
		return
	}
	e.Pause()
}

// Stop ends playback and forgets the position.
func (e *Engine) Stop() {
	e.mu.Lock()
	t := e.track
	e.track = nil
	e.status = StatusStopped
	e.pos = 0
	e.mu.Unlock()
	if t != nil {
		t.Close()
	}
	e.notify()
}

// Seek moves to a position, playing or paused.
func (e *Engine) Seek(at time.Duration) error {
	if at < 0 {
		at = 0
	}
	e.mu.Lock()
	if e.duration > 0 && at > e.duration {
		at = e.duration
	}
	t := e.track
	e.pos = at
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	e.mu.Unlock()
	if t != nil {
		t.seek(at)
	}
	e.notify()
	return nil
}

// SetVolume sets the gain in 0..1, and unmutes.
func (e *Engine) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	e.mu.Lock()
	e.volume = v
	if v > 0 {
		e.muted = false
	}
	e.mu.Unlock()
	e.notify()
}

// Volume is the gain, muted or not.
func (e *Engine) Volume() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.volume
}

// SetMuted mutes without losing the volume.
func (e *Engine) SetMuted(muted bool) {
	e.mu.Lock()
	e.muted = muted
	e.mu.Unlock()
	e.notify()
}

// Muted reports the mute state.
func (e *Engine) Muted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.muted
}

// Snapshot reads the state the UI shows.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := Snapshot{
		Status:   e.status,
		Duration: e.duration,
		Volume:   e.volume,
		Muted:    e.muted,
		Buffered: e.buffered,
		Title:    e.title,
		Error:    e.errMsg,
		Device:   e.device,
	}
	snap.Position = e.positionLocked()
	if snap.Duration > 0 && snap.Position > snap.Duration {
		snap.Position = snap.Duration
	}
	return snap
}

// positionLocked derives the position from the frames handed to the device and
// the clock. The device holds a fifth of a second, so the clock alone can lead
// what is audible and the frame count alone trails it by that much; the
// estimate is the clock kept inside the frames' bounds.
func (e *Engine) positionLocked() time.Duration {
	t := e.track
	if t == nil || e.status == StatusPaused || e.status == StatusStopped || e.status == StatusFailed {
		return e.pos
	}
	rate := t.format.SampleRate
	if rate <= 0 {
		return e.pos
	}
	played := framesToDuration(t.submitted.Load()-e.anchorFrame, rate)
	estimate := time.Since(e.anchorAt)
	if estimate > played {
		estimate = played
	}
	if behind := played - 350*time.Millisecond; estimate < behind {
		estimate = behind
	}
	if estimate < 0 {
		estimate = 0
	}
	return e.pos + estimate
}

// serveLocked records that a track is now audible. A track the engine has let go
// of (Pause, Stop, a replacement) must not resurrect the status: its goroutine
// can still be between a seek's restart and the next read when Close reaches it,
// and a stale write there would leave the engine reporting playback with no
// device open and no way to pause it.
func (e *Engine) serveLocked(status Status, t *track) {
	if e.track != t {
		return
	}
	e.status = status
	e.anchorAt = time.Now()
	e.anchorFrame = t.submitted.Load()
}

func (e *Engine) newSink() Sink {
	if e.NewSink != nil {
		return e.NewSink()
	}
	return &deviceSink{}
}

func (e *Engine) setBuffered(v float64) {
	if v > 1 {
		v = 1
	}
	e.mu.Lock()
	changed := int(e.buffered*20) != int(v*20)
	e.buffered = v
	e.mu.Unlock()
	if changed {
		e.notify()
	}
}

// scaleVolume applies the gain to signed 16-bit samples in place.
func (e *Engine) scaleVolume(pcm []byte) {
	e.mu.Lock()
	gain := e.volume
	if e.muted {
		gain = 0
	}
	e.mu.Unlock()
	if gain == 1 {
		return
	}
	if gain < 0 {
		gain = 0
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int32(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		scaled := int32(float64(sample) * gain)
		pcm[i] = byte(scaled)
		pcm[i+1] = byte(scaled >> 8)
	}
}

func (e *Engine) fail(t *track, err error) {
	e.mu.Lock()
	if e.track == t {
		e.track = nil
		e.status = StatusFailed
		e.errMsg = err.Error()
	}
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) finished(t *track) {
	e.mu.Lock()
	owned := e.track == t
	if owned {
		e.track = nil
		e.status = StatusStopped
		if e.duration > 0 {
			e.pos = e.duration
		}
	}
	e.mu.Unlock()
	if !owned {
		return
	}
	e.notify()
	if e.OnFinish != nil {
		e.OnFinish()
	}
}

func (e *Engine) notify() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

// Close ends the session and releases the device.
func (e *Engine) Close() { e.Stop() }

func framesToDuration(frames int64, rate int) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(frames) / float64(rate) * float64(time.Second))
}
