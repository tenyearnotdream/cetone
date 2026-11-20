package player

// A song still arriving is the hardest case: it cannot seek past the bytes it has,
// so the only way forward is to decode and wait. What must not happen is the
// position disagreeing with what is heard.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestSeekingAStreamThatIsStillArriving(t *testing.T) {
	// Serve a slowly delivered WAV: the whole file, but trickled out.
	var payload []byte
	{
		path := t.TempDir() + "/s.wav"
		if err := writeLongWAV(path, 8000, 20); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		payload = data
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		chunk := len(payload) / 40
		for off := 0; off < len(payload); off += chunk {
			end := off + chunk
			if end > len(payload) {
				end = len(payload)
			}
			if _, err := w.Write(payload[off:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer slow.Close()

	e := NewEngine()
	defer e.Close()
	r := &registry{}
	e.NewSink = r.newSink
	if err := e.Play(context.Background(), Source{
		Title: "stream", Stream: slow.URL, Duration: 20 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)

	// Seek well past what has arrived: it has to wait for the transfer, and then
	// the position it reports must be the target.
	e.Seek(15 * time.Second)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if s := e.Snapshot(); s.Position > 16*time.Second {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	pos := e.Snapshot().Position
	t.Logf("after seeking to 15s on a live stream: position %v", pos.Round(time.Millisecond))
	if pos < 15*time.Second || pos > 19*time.Second {
		t.Fatalf("the position is %v, want about 15s", pos.Round(time.Millisecond))
	}
	if open := r.openCount(); open != 1 {
		t.Fatalf("%d devices are open", open)
	}
}
