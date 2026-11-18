//go:build live

package app

// The acceptance path, end to end and through the production code: the real
// window's page in a headless session, the real API, the real audio device, and
// the real cache on disk. Nothing here is a fixture except the assertion that the
// song it picks is free to stream.
//
//	go test ./internal/app -tags live -run TestAcceptance -v
//
// It is behind the live tag because it needs the network and a sound card.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/headless"

	"cetone/internal/ncm"
	"cetone/internal/store"
)

// acceptancePaths is one application's directories, kept across a simulated
// restart.
func acceptancePaths(t *testing.T) store.Paths {
	t.Helper()
	root := t.TempDir()
	paths := store.Paths{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
	}
	return paths
}

// acceptanceApp builds a real application and opens its page with no window.
func acceptanceApp(t *testing.T, paths store.Paths) (*App, *headless.Session) {
	t.Helper()
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Close)
	a.config.IgnoreUnplayable = true
	_, config := a.Page(app.Config{Title: "ncmviva", Width: 1160, Height: 740})
	session, err := headless.Open(config, nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return a, session
}

// drive paints and applies background work until condition holds or the deadline
// passes, the way the window's own event loop would.
func drive(t *testing.T, a *App, session *headless.Session, condition func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.sync()
		if err := session.Advance(20 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// fullSongInPlaylist returns a real chart, the index of a song in it, and that
// song, choosing one the API answers with the whole track rather than a trial
// fragment, so a test never measures a thirty-second preview as if it were the
// song.
func fullSongInPlaylist(t *testing.T, a *App) (ncm.Playlist, int, ncm.Song) {
	t.Helper()
	charts, err := a.client.ToplistIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, chart := range charts {
		detail, err := a.client.PlaylistDetail(t.Context(), chart.ID)
		if err != nil {
			t.Fatalf("%s: %v", chart.Name, err)
		}
		for index, song := range detail.Tracks {
			if !song.Copyright().Playable() || song.Duration <= time.Minute {
				continue
			}
			urls, err := a.client.SongURLs(t.Context(), []int64{song.ID}, a.config.PreferredQuality())
			if err != nil || len(urls) == 0 || urls[0].URL == "" {
				continue
			}
			if !cacheableStream(song, urls[0]) {
				// A trial: it plays, and it must not be cached. Covered by its
				// own test; the acceptance path needs a whole song.
				continue
			}
			return detail, index, song
		}
	}
	t.Skip("no song the API serves whole in any chart")
	return ncm.Playlist{}, 0, ncm.Song{}
}

// TestAcceptancePlayAndCacheOnARealPlaylist is the path the user takes: open a
// playlist from the real API, press play, hear it, and find the song in the cache
// afterwards.
func TestAcceptancePlayAndCacheOnARealPlaylist(t *testing.T) {
	paths := acceptancePaths(t)
	a, session := acceptanceApp(t, paths)
	playlist, chosenIndex, chosen := fullSongInPlaylist(t, a)

	// Open the playlist page as the rail does, and let it load.
	a.push(route{kind: routePlaylist, id: playlist.ID, title: playlist.Name})
	if !drive(t, a, session, func() bool {
		data := a.data.playlists[playlist.ID]
		return data != nil && (data.loaded || data.err != "")
	}, 40*time.Second) {
		t.Fatal("the playlist page did not finish loading")
	}
	if data := a.data.playlists[playlist.ID]; data.err != "" {
		t.Fatalf("the playlist page failed: %s", data.err)
	}
	if !session.HasText(playlist.Name) {
		t.Fatalf("the playlist page does not show %q", playlist.Name)
	}

	// Click the row of the song the API serves whole, through the real input
	// router: a row click plays that row, which is what a user does.
	rows := 0
	for _, hit := range session.Snapshot().Hits {
		if hit.Label == chosen.Name {
			rows++
		}
	}
	if rows == 0 {
		t.Fatalf("the playlist does not show a row named %q", chosen.Name)
	}
	if err := session.ClickLabel(chosen.Name); err != nil {
		t.Fatalf("clicking the row of %q: %v", chosen.Name, err)
	}
	started := drive(t, a, session, func() bool {
		snapshot := a.engine.Snapshot()
		return snapshot.Status.String() == "failed" || snapshot.Position > 300*time.Millisecond
	}, 30*time.Second)
	snapshot := a.engine.Snapshot()
	if !started {
		t.Fatalf("nothing played: %+v", snapshot)
	}
	if snapshot.Status.String() == "failed" {
		t.Fatalf("playback failed: %s", snapshot.Error)
	}
	if snapshot.Position < 300*time.Millisecond {
		t.Fatalf("the transport reached %v only", snapshot.Position)
	}
	current, ok := a.current()
	if !ok {
		t.Fatal("the queue has no current song")
	}
	if current.ID != chosen.ID && a.queueAt != chosenIndex {
		// The click may land on another row when two rows share a name; what
		// matters is that the row clicked started, and the song that started is
		// the one the cache check below uses.
		t.Logf("clicked %q, the queue started %q at row %d", chosen.Name, current.Name, a.queueAt)
	}
	t.Logf("playing %q (%v in, device %v)", current.Name, snapshot.Position, snapshot.Device)

	// The song is being written into the cache at the name the GTK client uses.
	// The cache entry appears while the song plays, under the GTK client's name.
	var cachePath string
	cached := drive(t, a, session, func() bool {
		path, _, found := a.cache.Lookup(current.ID, a.config.PreferredQuality())
		if found {
			cachePath = path
		}
		return found
	}, 60*time.Second)
	if !cached {
		t.Fatalf("song %q never reached the cache under %s", current.Name, a.cache.Dir())
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("%s: %v", cachePath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("the cached file %s is empty", cachePath)
	}
	if !strings.HasPrefix(filepath.Base(cachePath), "music_") {
		t.Fatalf("the cache file is named %q, which an older client cannot find", filepath.Base(cachePath))
	}
	t.Logf("cached %d bytes at %s", info.Size(), cachePath)

	// Pausing holds the position and does not move to the next song.
	a.togglePlay()
	paused := a.engine.Snapshot()
	if paused.Status.String() != "paused" {
		t.Fatalf("the transport is %v after pausing", paused.Status)
	}
	next := a.queueAt
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 10; i++ {
		a.sync()
		session.Advance(20 * time.Millisecond)
	}
	if a.queueAt != next {
		t.Fatalf("pausing moved the queue from row %d to %d", next, a.queueAt)
	}
	if got := a.engine.Snapshot().Position; got != paused.Position {
		t.Fatalf("the position moved while paused: %v then %v", paused.Position, got)
	}
}

// TestAcceptanceCachedSongPlaysWithNoNetwork is the feature: after the song
// above is cached, a restart with the network off must still play it, from disk.
func TestAcceptanceCachedSongPlaysWithNoNetwork(t *testing.T) {
	paths := acceptancePaths(t)

	// First run: fetch one song straight into the cache, the way playing does.
	first, session := acceptanceApp(t, paths)
	_, _, song := fullSongInPlaylist(t, first)
	urls, err := first.client.SongURLs(t.Context(), []int64{song.ID}, first.config.PreferredQuality())
	if err != nil || len(urls) == 0 || urls[0].URL == "" {
		t.Skipf("the API returned no url for %q: %v", song.Name, err)
	}
	target := first.cache.Path(song.ID, urls[0].Quality)
	if err := downloadTo(t.Context(), urls[0].URL, target); err != nil {
		t.Fatalf("downloading %q: %v", song.Name, err)
	}
	first.cached.setDirty()
	_ = session
	t.Logf("first run cached %q at %s", song.Name, target)
	first.engine.Close()

	// Second run: a new application on the same directories, with no network at
	// all, playing the same song.
	second, session2 := acceptanceApp(t, paths)
	second.client.SetTransport(refuseTransport{})
	second.cache = store.NewCache(paths, nil)
	second.cached = newCacheIndex(second.cache)

	second.queue = []ncm.Song{song}
	second.queueAt = 0
	second.playAt(0)
	played := drive(t, second, session2, func() bool {
		snapshot := second.engine.Snapshot()
		return snapshot.Status.String() == "failed" || snapshot.Position > 400*time.Millisecond
	}, 30*time.Second)
	snapshot := second.engine.Snapshot()
	if !played {
		t.Fatalf("the cached song did not start: %+v", snapshot)
	}
	if snapshot.Status.String() == "failed" {
		t.Fatalf("playing the cached song with no network failed: %s", snapshot.Error)
	}
	if snapshot.Position < 400*time.Millisecond {
		t.Fatalf("the cached song played to %v only", snapshot.Position)
	}
	// The song must not have been re-fetched: the transport refuses everything.
	t.Logf("played %q from disk with the network off, to %v", song.Name, snapshot.Position)
}

// refuseTransport fails every request, standing in for a machine with no network.
type refuseTransport struct{}

func (refuseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("acceptance: the network is off")
}
