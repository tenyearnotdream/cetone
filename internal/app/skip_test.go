package app

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/ncm"
)

// A queue played to its ends must advance one song per end: every song heard in
// turn and none skipped.
func TestTheQueueAdvancesOneSongPerEnd(t *testing.T) {
	a := testApp(t)
	const songs = 3
	for i := 0; i < songs; i++ {
		id := int64(900 + i)
		song := ncm.Song{ID: id, Name: "song"}
		song.Duration = 700 * time.Millisecond
		a.queue = append(a.queue, song)
		// A cached file per song, under the name the player looks up, so the run
		// exercises playback and not the failure path.
		quality := a.songQuality(song)
		path := a.cache.Path(id, quality)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeTestWAV(path, 8000, 0.7); err != nil {
			t.Fatal(err)
		}
		if _, _, ok := a.cache.Lookup(id, quality); !ok {
			t.Fatalf("song %d is not found in the cache at %v (%s)", id, quality, path)
		}
	}

	var played []int
	start := time.Now()
	deadline := start.Add(15 * time.Second)
	a.playAt(0)
	last := -1
	for time.Now().Before(deadline) {
		a.sync()
		if a.queueAt != last {
			last = a.queueAt
			played = append(played, last)
			t.Logf("index %d after %v", last, time.Since(start).Round(10*time.Millisecond))
		}
		// One past the queue: the end of the last song is the third advance, and
		// that is what proves the tail of every song was played.
		if len(played) > songs {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Three songs of 700 ms take about two seconds of real time. A run that
	// finishes sooner played nothing: it fell through to the skip-on-failure path.
	elapsed := time.Since(start)
	if elapsed < 2100*time.Millisecond {
		t.Fatalf("the queue played %d songs in %v, too fast for 3 x 700ms", len(played)-1, elapsed)
	}
	if len(played) <= songs {
		t.Fatalf("the queue only reached %v", played)
	}
	for i, got := range played {
		if got != i%songs {
			t.Fatalf("the queue played %v, want 0,1,2 in order", played)
		}
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// writeTestWAV writes a silent PCM wave of the given length.
func writeTestWAV(path string, rate int, seconds float64) error {
	frames := int(float64(rate) * seconds)
	data := make([]byte, frames*4) // 16-bit stereo
	var header []byte
	header = append(header, "RIFF"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(36+len(data)))
	header = append(header, "WAVEfmt "...)
	header = binary.LittleEndian.AppendUint32(header, 16)
	header = binary.LittleEndian.AppendUint16(header, 1)
	header = binary.LittleEndian.AppendUint16(header, 2)
	header = binary.LittleEndian.AppendUint32(header, uint32(rate))
	header = binary.LittleEndian.AppendUint32(header, uint32(rate*4))
	header = binary.LittleEndian.AppendUint16(header, 4)
	header = binary.LittleEndian.AppendUint16(header, 16)
	header = append(header, "data"...)
	header = binary.LittleEndian.AppendUint32(header, uint32(len(data)))
	return os.WriteFile(path, append(header, data...), 0o644)
}
