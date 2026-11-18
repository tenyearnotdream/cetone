package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnsupportedFormat reports a file the application cannot decode.
var ErrUnsupportedFormat = errors.New("player: unsupported audio format")

// ErrNoDevice reports that the machine has no audio output to play on. The
// engine keeps tracking the position so the window, the lyrics and MPRIS work.
var ErrNoDevice = errors.New("player: no audio output device")

// download fetches a URL into a file, appending as it goes so a decoder can
// start on the first bytes instead of the last. Partial files land under the
// caller's path with a ".part" suffix, so an interrupted transfer never looks
// like a finished cache entry.
type download struct {
	path   string
	file   *os.File
	http   *http.Client
	url    string
	header http.Header

	written atomic.Int64
	size    atomic.Int64

	mu     sync.Mutex
	done   bool
	err    error
	cancel context.CancelFunc
	closed atomic.Bool
}

// startDownload opens the .part file and begins fetching. It returns before
// any byte has arrived; tailFile and waitDone observe progress.
func startDownload(parent context.Context, client *http.Client, rawURL, path string, header http.Header) (*download, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	d := &download{path: path, file: file, http: client, url: rawURL, header: header, cancel: cancel}
	go d.run(ctx)
	return d, nil
}

func (d *download) run(ctx context.Context) {
	fail := func(err error) {
		d.mu.Lock()
		d.err = err
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
		if ctx.Err() == nil {
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
		}
		if readErr != nil {
			if readErr != io.EOF && ctx.Err() == nil {
				fail(readErr)
			}
			d.mu.Lock()
			d.done = true
			d.mu.Unlock()
			return
		}
	}
}

// waitDone waits for the transfer to finish, reporting whether it completed.
func (d *download) waitDone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		d.mu.Lock()
		done, err := d.done, d.err
		d.mu.Unlock()
		if err != nil {
			return false
		}
		if done {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// complete reports a finished, successful transfer.
func (d *download) complete() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.done && d.err == nil
}

// progress reports bytes written and the expected total (0 when unknown).
func (d *download) progress() (written, total int64) {
	return d.written.Load(), d.size.Load()
}

// err returns the transfer's error, if any.
func (d *download) errLocked() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Close stops the transfer and closes the file. The partial file is removed
// unless the whole thing arrived.
func (d *download) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.cancel()
	err := d.file.Close()
	if !d.complete() {
		os.Remove(d.path)
	}
	return err
}

// tailFile reads a file that may still be growing. It is an io.ReadSeeker, as
// the decoders expect; a read at the current end waits for the download rather
// than reporting a premature end of file.
type tailFile struct {
	file     *os.File
	download *download
	offset   int64
}

// openTail opens the growing file for reading.
func (d *download) openTail() (*tailFile, error) {
	file, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	return &tailFile{file: file, download: d}, nil
}

func (t *tailFile) Read(p []byte) (int, error) {
	for {
		n, err := t.file.Read(p)
		if n > 0 {
			t.offset += int64(n)
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if t.download.complete() {
			return 0, io.EOF
		}
		if derr := t.download.errLocked(); derr != nil {
			return 0, derr
		}
		// No more bytes yet and the transfer is still running: wait for the
		// writer to append, then try again.
		time.Sleep(15 * time.Millisecond)
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
	}
	if target < 0 {
		return 0, fmt.Errorf("player: negative seek offset")
	}
	if t.download.complete() && t.download.errLocked() == nil {
		// A finished file seeks anywhere inside it.
		pos, err := t.file.Seek(target, io.SeekStart)
		t.offset = pos
		return pos, err
	}
	if target > t.download.written.Load() {
		return 0, fmt.Errorf("player: seek to %d past the %d bytes downloaded", target, t.download.written.Load())
	}
	pos, err := t.file.Seek(target, io.SeekStart)
	t.offset = pos
	return pos, err
}

// Close closes the read handle; the download keeps running.
func (t *tailFile) Close() error { return t.file.Close() }
