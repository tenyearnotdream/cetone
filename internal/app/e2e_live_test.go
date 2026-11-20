//go:build live

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

// The whole path at every tier: set the quality, play a real song, and read what
// the application says it is playing.
func TestEachTierPlaysAndNamesItself(t *testing.T) {
	paths := store.DefaultPaths()
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer a.engine.Close()
	user, err := a.client.LoginStatus(t.Context())
	if err != nil {
		t.Skipf("no session: %v", err)
	}
	t.Logf("account %q vip=%d", user.Nickname, user.VIPType)

	songs, err := a.client.SongsDetail(t.Context(), []int64{1970303952})
	if err != nil || len(songs) == 0 {
		t.Fatal(err)
	}
	song := songs[0]

	// A clean cache, so each tier is really fetched rather than answered from a
	// file a previous tier left on disk.
	if err := os.RemoveAll(filepath.Join(paths.Cache, "music")); err != nil {
		t.Fatal(err)
	}

	for _, q := range []ncm.Quality{ncm.QualityExtreme, ncm.QualityLossless, ncm.QualityHiRes, ncm.QualitySurround} {
		a.config.Quality = int(q)
		a.queue = []ncm.Song{song}
		a.queueAt = 0
		a.playAt(0)

		deadline := time.Now().Add(40 * time.Second)
		played := false
		for time.Now().Before(deadline) {
			a.sync()
			snap := a.engine.Snapshot()
			if snap.Status == player.StatusPlaying && snap.Position > 700*time.Millisecond {
				played = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !played {
			t.Errorf("%v: never played (status %v)", q, a.engine.Snapshot().Status)
			continue
		}
		got := a.playingQuality(a.queue[0])
		t.Logf("set %-9s -> transport shows %-9s (position %v)", q, got,
			a.engine.Snapshot().Position.Round(time.Millisecond))
		if got == "" {
			t.Errorf("%v: the transport names no tier", q)
		}
		a.engine.Stop()
	}
}
