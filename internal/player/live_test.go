//go:build live

package player

// A live check of the real device and a real stream. It needs the network and
// an audio device, so it runs only under the "live" tag:
//
//	go test ./internal/player -tags live -run Live -v

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveStreamPlays(t *testing.T) {
	const url = "https://music.163.com/song/media/outer/url?id=1330348068.mp3"
	e := NewEngine()
	t.Cleanup(e.Close)
	finished := make(chan struct{}, 1)
	e.OnFinish = func() { finished <- struct{}{} }
	err := e.Play(context.Background(), Source{Title: "live", Stream: url})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	snap := e.Snapshot()
	t.Logf("after 3s: %+v", snap)
	if snap.Position < 1500*time.Millisecond {
		t.Fatalf("the stream did not advance: %v", snap.Position)
	}
	e.Pause()
	if got := e.Snapshot(); got.Status != StatusPaused {
		t.Fatalf("pause did not take: %v", got.Status)
	}
}

func TestLiveMP3Decodes(t *testing.T) {
	resp, err := http.Get("https://music.163.com/song/media/outer/url?id=1330348068.mp3")
	if err != nil {
		t.Skip(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) < 100000 {
		t.Skipf("no audio: %v, %d bytes", err, len(data))
	}
	path := t.TempDir() + "/song.mp3"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dec, err := openDecoder(mustOpen(t, path))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("format %+v", dec.Format())
	buf := make([]byte, 8192)
	n, err := dec.Read(buf)
	if n == 0 || err != nil {
		t.Fatalf("decoded %d bytes: %v", n, err)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}
