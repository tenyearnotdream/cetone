package app

// The tier the server answered with has to be visible, not merely recorded: the
// user reads the transport to know what they are hearing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cetone/internal/ncm"
)

func TestTheTransportShowsTheTierOnScreen(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 9101, Name: "海阔天空", Duration: 3 * time.Second}
	path := a.cache.Path(song.ID, ncm.QualityHiRes)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestWAV(path, 8000, 3); err != nil {
		t.Fatal(err)
	}
	a.queue = []ncm.Song{song}
	a.playAt(0)
	t.Cleanup(a.engine.Stop)

	driver := open(t, a)
	pump(t, driver, a)
	if err := driver.Paint(); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, label := range driver.Snapshot().Labels {
		texts = append(texts, label.Value)
	}
	joined := strings.Join(texts, " | ")
	t.Logf("on screen: %s", joined)
	if !strings.Contains(joined, ncm.QualityHiRes.String()) {
		t.Fatalf("the transport does not show the tier %q", ncm.QualityHiRes.String())
	}
}
