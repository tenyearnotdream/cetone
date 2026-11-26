//go:build live

package app

// End-to-end checks against the real API and the real audio device. They are
// behind the "live" tag because they need the network (and, for the playback
// one, a sound card):
//
//	go test ./internal/app -tags live -run Live -v

import (
	"testing"
	"time"

	"github.com/vivaui/viva/app"

	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

func liveApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	a, err := New(store.Paths{
		Config: root + "/config",
		Data:   root + "/data",
		Cache:  root + "/cache",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Close)
	return a
}

// TestLiveDiscoverLoads checks that the real discover page fills with the real
// API's content.
func TestLiveDiscoverLoads(t *testing.T) {
	a := liveApp(t)
	_, config := a.Page(app.Config{Title: "live", Width: 1160, Height: 740})
	driver, err := app.NewDriver(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	pumpUntil(t, driver, a, func() bool {
		return a.data.discover.loaded || a.data.discover.err != ""
	})
	data := a.data.discover
	if !data.loaded {
		t.Fatalf("the discover page did not load: %v %q", data.busy, data.err)
	}
	t.Logf("%d banners, %d playlists, %d new albums (err %q)", len(data.banners), len(data.playlists), len(data.newAlbums), data.err)
	if len(data.playlists) == 0 && len(data.newAlbums) == 0 {
		t.Fatal("the discover page has no content at all")
	}
}

// TestLivePlaylistAndCacheKey loads a real chart and checks the playlist page
// renders its tracks.
func TestLivePlaylistAndCacheKey(t *testing.T) {
	a := liveApp(t)
	charts, err := a.client.ToplistIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) == 0 {
		t.Fatal("no charts")
	}
	a.push(route{kind: routePlaylist, id: charts[0].ID, title: charts[0].Name})
	_, config := a.Page(app.Config{Title: "live", Width: 1160, Height: 740})
	driver, err := app.NewDriver(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	pumpUntil(t, driver, a, func() bool {
		data := a.data.playlists[charts[0].ID]
		return data != nil && (data.loaded || data.err != "")
	})
	data := a.data.playlists[charts[0].ID]
	if data == nil || !data.loaded || len(data.playlist.Tracks) == 0 {
		message := ""
		if data != nil {
			message = data.err
		}
		t.Fatalf("the chart %q did not load its tracks: %s", charts[0].Name, message)
	}
	t.Logf("%q: %d tracks; the row list should name %q", data.playlist.Name, len(data.playlist.Tracks), data.playlist.Tracks[0].Name)
}

// TestLiveCachedSongPlaysFromDisk is the client's own claim, end to end: a song
// resolved from the API is downloaded into the cache, and a second play of the
// same song plays the file without asking for a URL.
func TestLiveCachedSongPlaysFromDisk(t *testing.T) {
	a := liveApp(t)
	result, err := a.client.Search(t.Context(), "Yesterday", ncm.SearchSongs, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var song ncm.Song
	for _, candidate := range result.Songs {
		if candidate.Fee == 0 {
			song = candidate
			break
		}
	}
	if song.ID == 0 {
		t.Skip("no free song in the search page")
	}
	// Resolve and download at standard quality, straight into the cache, which
	// is what the player does while it plays.
	urls, err := a.client.SongURLs(t.Context(), []int64{song.ID}, ncm.QualityStandard)
	if err != nil || len(urls) == 0 || urls[0].URL == "" {
		t.Skipf("the API returned no url: %v", err)
	}
	target := a.cache.Path(song.ID, urls[0].Quality)
	if err := player.FetchToCache(t.Context(), a.client.HTTPClient(), player.Source{
		Stream:    urls[0].URL,
		CachePath: target,
	}); err != nil {
		t.Fatalf("download: %v", err)
	}
	a.cached.SetDirty()

	// The cache has it, under the name the GTK client would have used.
	path, _, ok := a.cache.Lookup(song.ID, ncm.QualityStandard)
	if !ok {
		t.Fatalf("the downloaded song is not in the cache at %s", target)
	}
	t.Logf("cached %q at %s", song.Name, path)

	// Playing it needs no request: the transport advances from the file alone.
	a.config.IgnoreUnplayable = true
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	go a.resolveAndPlay(song)
	waitFor(t, a, func() bool {
		snapshot := a.engine.Snapshot()
		return snapshot.Status.String() == "failed" || snapshot.Position >= 900*time.Millisecond
	})
	snapshot := a.engine.Snapshot()
	if snapshot.Status.String() == "failed" {
		t.Fatalf("playing from the cache failed: %s", snapshot.Error)
	}
	t.Logf("played from disk to %v (device %v)", snapshot.Position, snapshot.Device)
}
