package player

// Playing a stream writes its bytes into the cache while it plays. The
// regression this guards: a fresh install has no cache directory, so opening the
// ".part" file failed and every streamed song reported "cannot play".

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// wavServer serves a wav fixture at any path, as fast as the loopback allows.
func wavServer(t *testing.T, seconds float64) *httptest.Server {
	t.Helper()
	return wavServerSlow(t, seconds, 0)
}

// wavServerSlow serves the fixture in chunks with a pause between them, so a
// test can act while the transfer is still running.
func wavServerSlow(t *testing.T, seconds float64, pause time.Duration) *httptest.Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.wav")
	writeWAV(t, path, 8000, 2, seconds)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", itoa(len(data)))
		if pause == 0 {
			w.Write(data)
			return
		}
		// A chunk a second, flushed, so the client sees a byte rate rather than
		// one write.
		const chunks = 20
		step := len(data)/chunks + 1
		for offset := 0; offset < len(data); offset += step {
			end := min(offset+step, len(data))
			if _, err := w.Write(data[offset:end]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(pause)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buffer [20]byte
	i := len(buffer)
	for n > 0 {
		i--
		buffer[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buffer[i:])
}

func TestStreamingCreatesItsCacheDirectory(t *testing.T) {
	server := wavServer(t, 1.5)
	e, sink := newTestEngine(t)
	// The cache directory does not exist, as on a fresh install.
	cachePath := filepath.Join(t.TempDir(), "cache", "music", "music_123_128000")

	if err := e.Play(context.Background(), Source{
		Title:     "streamed",
		Stream:    server.URL,
		CachePath: cachePath,
		Duration:  1500 * time.Millisecond,
	}); err != nil {
		t.Fatalf("playing a stream into a missing cache directory: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	snapshot := e.Snapshot()
	if snapshot.Status.String() == "failed" {
		t.Fatalf("the stream failed: %s", snapshot.Error)
	}
	if snapshot.Position < 150*time.Millisecond {
		t.Fatalf("the stream played to %v only", snapshot.Position)
	}
	if sink.bytes() == 0 {
		t.Fatal("nothing reached the sink")
	}

	// When it finishes, the file lands at the cache path, ready for the next
	// play to come from disk.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(cachePath); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the finished stream was not moved to %s", cachePath)
}

func TestAStreamThatCannotOpenItsFileSaysSo(t *testing.T) {
	// A cache path under a file (not a directory) cannot be created; the engine
	// reports that rather than starting a silent session.
	root := t.TempDir()
	blocker := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := newTestEngine(t)
	err := e.Play(context.Background(), Source{
		Title:     "blocked",
		Stream:    "http://127.0.0.1:1/never",
		CachePath: filepath.Join(blocker, "music", "music_1_128000"),
	})
	if err == nil {
		t.Fatal("a cache path that cannot be created was accepted")
	}
	if snapshot := e.Snapshot(); snapshot.Status.String() != "failed" {
		t.Fatalf("the state after the failure is %v", snapshot.Status)
	}
}

// Cancelling a session's context ends it, which is the engine's contract: the
// caller owns the session's lifetime. What a cancellation must never do is leave
// a partial file at the cache path or report the song as finished.
//
// The bug this guards belonged to the caller (it handed a request-scoped context
// to Play); the engine's own guarantees are what is checked here, and the
// caller's mistake has its own test in package app.
func TestACancelledSessionLeavesNoCacheEntry(t *testing.T) {
	// The transfer is slow enough that the cancellation lands while it is still
	// arriving.
	server := wavServerSlow(t, 3, 40*time.Millisecond)
	e, sink := newTestEngine(t)
	finished := make(chan struct{}, 1)
	e.OnFinish = func() { finished <- struct{}{} }
	cachePath := filepath.Join(t.TempDir(), "cache", "music", "music_9_128000")

	sessionCtx, cancel := context.WithCancel(context.Background())
	if err := e.Play(sessionCtx, Source{Title: "long", Stream: server.URL, CachePath: cachePath, Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if sink.bytes() == 0 {
		t.Fatal("nothing played before the cancellation")
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := e.Snapshot().Status; status == StatusStopped || status == StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A cancelled transfer must not be left at the cache path, under either name.
	for _, path := range []string{cachePath, cachePath + ".part"} {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			t.Fatalf("a cancelled transfer left %d bytes at %s", info.Size(), path)
		}
	}
	select {
	case <-finished:
		t.Fatal("a cancelled transfer was reported as the end of a song")
	default:
	}
}

// A truncated answer is not the song: it is neither played to its end nor
// promoted into the cache.
func TestATruncatedDownloadIsNotPromoted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.wav")
	writeWAV(t, path, 8000, 2, 3)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The server promises the whole file and delivers a third of it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", itoa(len(data)))
		w.Write(data[:len(data)/3])
	}))
	defer server.Close()

	e, _ := newTestEngine(t)
	cachePath := filepath.Join(t.TempDir(), "cache", "music_1_128000")
	if err := e.Play(context.Background(), Source{Title: "cut", Stream: server.URL, CachePath: cachePath, Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := e.Snapshot().Status; status == StatusStopped || status == StatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if info, err := os.Stat(cachePath); err == nil && info.Size() > 0 {
		t.Fatalf("a truncated download was promoted to the cache: %d bytes", info.Size())
	}
}
