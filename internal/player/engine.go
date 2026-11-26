// Package player plays audio files and streams on the system's default output.
//
// It owns at most one decode goroutine and one output device at a time; the UI
// reads a Snapshot and never touches a decoder or sink directly. Nothing runs
// while the engine is stopped, and an idle window holds no device and no
// goroutine.
package player

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Status describes what the playback engine is currently doing.
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

// Source describes one audio source: a complete local file, or an HTTP stream
// that can optionally be promoted to CachePath once fully downloaded.
type Source struct {
	// SongID links this source to the application's track identifier so the UI
	// never mistakes a previous track's progress for the newly selected track.
	SongID int64
	// Title names the source in errors and snapshots.
	Title string
	// File is a complete local file.
	File string
	// Stream is a URL downloaded while playing. When CachePath is set and the
	// transfer completes cleanly, the .part file is renamed to CachePath.
	Stream    string
	CachePath string
	Header    http.Header
	// Duration is the length reported by metadata; the container's own exact
	// duration wins whenever available.
	Duration time.Duration
}

// Sink is the audio output device for a single playback session.
type Sink interface {
	Open(Format) error
	Write([]byte) (int, error)
	Close() error
}

// Snapshot is an immutable point-in-time view of the engine for the UI.
type Snapshot struct {
	SongID   int64
	Status   Status
	Position time.Duration
	Duration time.Duration
	Volume   float64
	Muted    bool
	Buffered float64
	Title    string
	Error    string
	// Device is false when no output device could be opened; the playback clock
	// still advances so lyrics, progress and MPRIS keep working.
	Device bool
}

// Engine plays one source at a time.
type Engine struct {
	// HTTP fetches audio streams. Defaults to http.DefaultClient.
	HTTP *http.Client
	// NewSink creates the output sink for a session. Tests override this.
	NewSink func() Sink
	// OnChange is invoked when playback state changes (typically to wake the UI).
	// Set callbacks before calling Play.
	OnChange func()
	// OnFinish is invoked when a track naturally reaches its end.
	OnFinish func()
	// OnCacheWritten is invoked on the session goroutine when a completed stream
	// is promoted to its cache path.
	OnCacheWritten func(path string)

	mu       sync.Mutex
	track    *track
	last     Source
	songID   int64
	status   Status
	pos      time.Duration
	duration time.Duration
	title    string
	errMsg   string
	buffered float64
	volume   float64
	muted    bool
	device   bool

	// anchorAt and anchorFrame anchor the real-time playback clock against
	// frames handed to the sink so position moves smoothly without ever running
	// ahead of decoded audio.
	anchorAt    time.Time
	anchorFrame int64

	// seekTo / seeking hold the target position while an asynchronous seek is
	// settling in the session goroutine, keeping the progress bar pinned to the
	// user's target until audio resumes there.
	seekTo  time.Duration
	seeking bool
}

// NewEngine returns an idle engine initialized at 70% volume.
func NewEngine() *Engine {
	return &Engine{
		HTTP:     http.DefaultClient,
		volume:   0.7,
		buffered: 1,
		device:   true,
	}
}

// Prepare immediately stops the current track and resets the engine's metadata
// and progress for an upcoming track that is still resolving over the network.
// This ensures the old song stops at once and the progress bar never shows the
// previous track's time on the new track.
func (e *Engine) Prepare(songID int64, title string, duration, at time.Duration) {
	if at < 0 {
		at = 0
	}
	if duration > 0 && at > duration {
		at = duration
	}
	e.mu.Lock()
	old := e.track
	e.track = nil
	e.songID = songID
	e.title = title
	e.duration = duration
	e.pos = at
	e.errMsg = ""
	e.buffered = 0
	e.seeking = false
	e.seekTo = 0
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	e.status = StatusBuffering
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	e.notify()
}

// Play starts source from the beginning, replacing whatever was playing.
func (e *Engine) Play(ctx context.Context, source Source) error {
	return e.PlayAt(ctx, source, 0)
}

// PlayAt starts source directly at position at, replacing whatever was playing.
// Seeking happens before the audio sink opens so switching quality or resuming
// never emits a blip from 0:00 or flashes 0:00 on the progress bar.
func (e *Engine) PlayAt(ctx context.Context, source Source, at time.Duration) error {
	if at < 0 {
		at = 0
	}
	t, dur, err := newTrack(ctx, e, source, at)
	if err != nil {
		e.mu.Lock()
		// Only record the failure if no newer track replaced this one while
		// opening (for example, rapid skipping).
		if source.SongID == 0 || e.songID == 0 || e.songID == source.SongID {
			e.status = StatusFailed
			e.errMsg = err.Error()
			e.title = source.Title
			if source.SongID != 0 {
				e.songID = source.SongID
			}
		}
		e.mu.Unlock()
		e.notify()
		return err
	}
	if dur > 0 && at > dur {
		at = dur
		t.at = at
	}

	e.mu.Lock()
	old := e.track
	e.track = t
	e.last = source
	e.songID = source.SongID
	e.title = source.Title
	e.duration = dur
	e.pos = at
	e.errMsg = ""
	e.buffered = 1
	if t.dl != nil {
		e.buffered = 0
	}
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	// Clear any previous session's pending seek state. When starting at > 0,
	// hold seekTo=at until t finishes positioning so the bar stays steady at at.
	if at > 0 {
		e.seeking = true
		e.seekTo = at
	} else {
		e.seeking = false
		e.seekTo = 0
	}
	e.status = StatusPlaying
	e.mu.Unlock()

	if old != nil {
		old.Close()
	}
	go t.run()
	e.notify()
	return nil
}

// Resume restarts the last source from where it was paused.
func (e *Engine) Resume(ctx context.Context) error {
	e.mu.Lock()
	if e.status != StatusPaused {
		e.mu.Unlock()
		return nil
	}
	source, at := e.last, e.pos
	e.mu.Unlock()
	return e.PlayAt(ctx, source, at)
}

// Pause freezes the current playback position and releases the audio device.
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
	e.seeking = false
	e.seekTo = 0
	e.mu.Unlock()
	if t != nil {
		t.Close()
	}
	e.notify()
}

// Toggle pauses when playing/buffering, or resumes when paused.
func (e *Engine) Toggle(ctx context.Context) {
	if e.Snapshot().Status == StatusPaused {
		_ = e.Resume(ctx)
		return
	}
	e.Pause()
}

// Stop ends playback, releases the audio device, and resets the position to 0.
func (e *Engine) Stop() {
	e.mu.Lock()
	t := e.track
	e.track = nil
	e.status = StatusStopped
	e.pos = 0
	e.seeking = false
	e.seekTo = 0
	e.mu.Unlock()
	if t != nil {
		t.Close()
	}
	e.notify()
}

// Seek moves playback to at, whether playing or paused.
func (e *Engine) Seek(at time.Duration) error {
	if at < 0 {
		at = 0
	}
	e.mu.Lock()
	if e.duration > 0 && at > e.duration {
		at = e.duration
	}
	t := e.track
	if t == nil {
		e.pos = at
		e.mu.Unlock()
		e.notify()
		return nil
	}
	e.seekTo = at
	e.seeking = true
	e.mu.Unlock()
	t.seek(at)
	e.notify()
	return nil
}

// SetVolume sets the playback gain in [0, 1] and unmutes when v > 0.
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

// Volume returns the configured gain (independent of mute).
func (e *Engine) Volume() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.volume
}

// SetMuted mutes or unmutes playback without altering the stored volume level.
func (e *Engine) SetMuted(muted bool) {
	e.mu.Lock()
	e.muted = muted
	e.mu.Unlock()
	e.notify()
}

// Muted reports whether playback is muted.
func (e *Engine) Muted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.muted
}

// targetGain returns the effective gain (0 when muted).
func (e *Engine) targetGain() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.muted {
		return 0
	}
	if e.volume < 0 {
		return 0
	}
	if e.volume > 1 {
		return 1
	}
	return e.volume
}

// Snapshot returns a consistent snapshot of playback state for the UI.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := Snapshot{
		SongID:   e.songID,
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

// Close stops playback and releases the audio device.
func (e *Engine) Close() { e.Stop() }

// ----- internal state helpers -----

func (e *Engine) positionLocked() time.Duration {
	if e.seeking {
		return e.seekTo
	}
	t := e.track
	if t == nil || e.status == StatusPaused || e.status == StatusStopped || e.status == StatusFailed || e.status == StatusBuffering {
		return e.pos
	}
	rate := t.format.SampleRate
	if rate <= 0 {
		return e.pos
	}
	written := framesToDuration(t.submitted.Load()-e.anchorFrame, rate)
	estimate := time.Since(e.anchorAt)
	if estimate > written {
		estimate = written
	}
	if estimate < 0 {
		estimate = 0
	}
	position := e.pos + estimate
	if e.duration > 0 && position > e.duration {
		position = e.duration
	}
	return position
}

func (e *Engine) landSeek(t *track, at time.Duration) {
	e.mu.Lock()
	if e.track != t {
		e.mu.Unlock()
		return
	}
	if e.seeking && e.seekTo != at {
		e.mu.Unlock()
		e.notify()
		return
	}
	e.pos = at
	e.anchorAt = time.Now()
	e.anchorFrame = 0
	e.seeking = false
	e.seekTo = 0
	e.mu.Unlock()
	e.notify()
}

// syncClock re-anchors the engine clock when a stream resumes after a stall
// so the reported position never leaps ahead of what was actually heard.
func (e *Engine) syncClock(t *track, playedSinceAnchor time.Duration) {
	e.mu.Lock()
	if e.track != t || e.seeking {
		e.mu.Unlock()
		return
	}
	e.anchorAt = time.Now().Add(-playedSinceAnchor)
	e.mu.Unlock()
}

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
	if v < 0 {
		v = 0
	}
	e.mu.Lock()
	changed := int(e.buffered*20) != int(v*20)
	e.buffered = v
	e.mu.Unlock()
	if changed {
		e.notify()
	}
}

// scaleVolume applies the current gain to signed 16-bit LE PCM samples in place.
func (e *Engine) scaleVolume(pcm []byte) {
	gain := e.targetGain()
	if gain == 1 {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int32(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		scaled := clampInt64ToInt16(int64(float64(sample) * gain))
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
		e.seeking = false
		e.seekTo = 0
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
		e.seeking = false
		e.seekTo = 0
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

func framesToDuration(frames int64, rate int) time.Duration {
	if rate <= 0 || frames <= 0 {
		return 0
	}
	// Use 128-bit-safe integer split (seconds + remainder) to avoid float drift
	// on long high-sample-rate tracks.
	sec := frames / int64(rate)
	rem := frames % int64(rate)
	return time.Duration(sec)*time.Second + time.Duration(rem*int64(time.Second)/int64(rate))
}

func samplesAt(at time.Duration, format Format) int64 {
	if format.SampleRate <= 0 || at <= 0 {
		return 0
	}
	sec := int64(at / time.Second)
	rem := int64(at % time.Second)
	return sec*int64(format.SampleRate) + rem*int64(format.SampleRate)/int64(time.Second)
}
