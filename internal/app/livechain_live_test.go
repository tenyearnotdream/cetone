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

// The whole path with a real account: play a song, see which tier was served, and
// see what the library recorded.
func TestPlayingRecordsTheTierInTheLibrary(t *testing.T) {
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
	a.queue = []ncm.Song{song}
	a.playAt(0)

	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if s := a.engine.Snapshot(); s.Status == player.StatusPlaying && s.Position > time.Second {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	served := a.queue[0].Actual
	t.Logf("served tier: %v", served)

	// The file arrives while the song plays: wait for the library to record it.
	var entry store.CacheEntry
	var ok bool
	wait := time.Now().Add(60 * time.Second)
	for time.Now().Before(wait) {
		a.sync()
		if entry, ok = a.library.Get(song.ID); ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("library entry: %+v (found %v)", entry, ok)
	if !ok {
		t.Fatal("the library has no entry after playing")
	}
	if entry.Quality != served {
		t.Errorf("the library says %v, the server served %v", entry.Quality, served)
	}
	// The file must be the one at that tier.
	want := store.Name(song.ID, served)
	path := filepath.Join(paths.Cache, "music", want)
	if info, err := os.Stat(path); err != nil {
		t.Logf("file %q not there yet (the song was not played to its end): %v", want, err)
	} else {
		t.Logf("file %q is %d bytes", want, info.Size())
	}
}
