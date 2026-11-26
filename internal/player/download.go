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

// ErrUnsupportedFormat reports a file the application cannot decode.
var ErrUnsupportedFormat = errors.New("player: unsupported audio format")

// ErrNoDevice reports that the machine has no audio output to play on. The
// engine keeps tracking the position so the window, the lyrics and MPRIS work.
var ErrNoDevice = errors.New("player: no audio output device")

// download fetches a URL into a .part file, appending as bytes arrive so a
// decoder can start immediately. A condition variable wakes readers as soon as
// new bytes land or the transfer ends, avoiding polling latency.
type download struct {
	path   string
	file   *os.File
	http   *http.Client
	url    string
	header http.Header

	written atomic.Int64
	size    atomic.Int64
	// keep reports that the transfer's bytes now live at their final cache name,
	// so Close must not delete them.
	keep atomic.Bool

	mu     sync.Mutex
	cond   *sync.Cond
	done   bool
	err    error
	cancel context.CancelFunc
	closed atomic.Bool
}

// startDownload opens the .part file and starts streaming into it in the
// background.
func startDownload(parent context.Context, client *http.Client, rawURL, path string, header http.Header) (*download, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	d := &download{
		path:   path,
		file:   file,
		http:   client,
		url:    rawURL,
		header: header,
		cancel: cancel,
	}
	d.cond = sync.NewCond(&d.mu)
	go d.run(ctx)
	return d, nil
}

func (d *download) run(ctx context.Context) {
	fail := func(err error) {
		d.mu.Lock()
		d.err = err
		d.cond.Broadcast()
		d.mu.Unlock()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		fail(err)
		return
	}
	for name, values := range d.header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	resp, err := d.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			fail(context.Canceled)
		} else {
			fail(err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(fmt.Errorf("player: download: HTTP %d", resp.StatusCode))
		return
	}
	if resp.ContentLength > 0 {
		d.size.Store(resp.ContentLength)
	}
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := d.file.WriteAt(buf[:n], d.written.Load()); err != nil {
				fail(err)
				return
			}
			d.written.Add(int64(n))
			d.mu.Lock()
			d.cond.Broadcast()
			d.mu.Unlock()
		}
		if readErr != nil {
			d.mu.Lock()
			switch {
			case readErr == io.EOF:
				// Only a transfer delivering all promised bytes is complete.
				if expected := d.size.Load(); expected > 0 && d.written.Load() < expected {
					d.err = fmt.Errorf("player: download ended after %d of %d bytes", d.written.Load(), expected)
				} else {
					d.done = true
				}
			case ctx.Err() != nil:
				d.err = context.Canceled
			default:
				d.err = readErr
			}
			d.cond.Broadcast()
			d.mu.Unlock()
			return
		}
	}
}

// waitDone blocks until the transfer finishes, fails, is closed, or times out.
func (d *download) waitDone(timeout time.Duration) bool {
	timer := time.AfterFunc(timeout, func() {
		d.mu.Lock()
		d.cond.Broadcast()
		d.mu.Unlock()
	})
	defer timer.Stop()
	deadline := time.Now().Add(timeout)

	d.mu.Lock()
	defer d.mu.Unlock()
	for {
		if d.err != nil {
			return false
		}
		if d.done {
			return true
		}
		if d.closed.Load() || time.Now().After(deadline) {
			return false
		}
		d.cond.Wait()
	}
}

// complete reports whether every promised byte arrived with no error.
func (d *download) complete() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.done || d.err != nil {
		return false
	}
	if expected := d.size.Load(); expected > 0 && d.written.Load() < expected {
		return false
	}
	return true
}

// progress reports bytes written and expected total (0 when unknown).
func (d *download) progress() (written, total int64) {
	return d.written.Load(), d.size.Load()
}

func (d *download) errLocked() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Close cancels the transfer, wakes any waiting readers, and deletes the
// partial file unless it finished or was already promoted to the cache.
func (d *download) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.cancel()
	d.mu.Lock()
	if !d.done && d.err == nil {
		d.err = context.Canceled
	}
	d.cond.Broadcast()
	d.mu.Unlock()
	err := d.file.Close()
	if !d.complete() && !d.keep.Load() {
		_ = os.Remove(d.path)
	}
	return err
}

// tailFile reads a file that may still be growing. A read at the current end
// waits on the download's condition variable until new bytes arrive or the
// transfer finishes.
type tailFile struct {
	file     *os.File
	download *download
	offset   int64
	closed   atomic.Bool
}

func (d *download) openTail() (*tailFile, error) {
	file, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	return &tailFile{file: file, download: d}, nil
}

func (t *tailFile) Read(p []byte) (int, error) {
	for {
		if t.closed.Load() {
			return 0, os.ErrClosed
		}
		n, err := t.file.Read(p)
		if n > 0 {
			t.offset += int64(n)
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		d := t.download
		d.mu.Lock()
		for !t.closed.Load() && !d.done && d.err == nil && t.offset >= d.written.Load() {
			d.cond.Wait()
		}
		done, derr := d.done, d.err
		d.mu.Unlock()

		if t.closed.Load() {
			return 0, os.ErrClosed
		}
		// Always try reading remaining bytes before declaring EOF.
		if t.offset < d.written.Load() {
			continue
		}
		if derr != nil {
			return 0, derr
		}
		if done {
			return 0, io.EOF
		}
	}
}

func (t *tailFile) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = t.offset + offset
	case io.SeekEnd:
		target = t.download.written.Load() + offset
	default:
		return 0, fmt.Errorf("player: invalid seek whence %d", whence)
	}
	if target < 0 {
		return 0, fmt.Errorf("player: negative seek offset")
	}
	if t.download.complete() {
		pos, err := t.file.Seek(target, io.SeekStart)
		if err == nil {
			t.offset = pos
		}
		return pos, err
	}
	if target > t.download.written.Load() {
		return 0, fmt.Errorf("player: seek to %d past the %d bytes downloaded", target, t.download.written.Load())
	}
	pos, err := t.file.Seek(target, io.SeekStart)
	if err == nil {
		t.offset = pos
	}
	return pos, err
}

// Close closes the read handle and wakes any blocked Read.
func (t *tailFile) Close() error {
	t.closed.Store(true)
	t.download.mu.Lock()
	t.download.cond.Broadcast()
	t.download.mu.Unlock()
	return t.file.Close()
}

// FetchToCache downloads a source straight into its final cache file without
// playing it, atomically renaming only after every promised byte has arrived.
func FetchToCache(ctx context.Context, client *http.Client, source Source) error {
	if source.Stream == "" || source.CachePath == "" {
		return errors.New("player: fetch: a stream and a cache path are needed")
	}
	if client == nil {
		client = http.DefaultClient
	}
	part := source.CachePath + ".part"
	d, err := startDownload(ctx, client, source.Stream, part, source.Header)
	if err != nil {
		return err
	}
	defer d.Close()
	if !d.waitDone(30 * time.Minute) {
		if err := d.errLocked(); err != nil {
			return err
		}
		return errors.New("player: fetch: the transfer did not finish")
	}
	if !d.complete() {
		return errors.New("player: fetch: the transfer was incomplete")
	}
	if err := os.MkdirAll(filepath.Dir(source.CachePath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(part, source.CachePath); err != nil {
		return err
	}
	d.keep.Store(true)
	return nil
}
