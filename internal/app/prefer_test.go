package app

// What plays when a file on disk and a better stream are both available. The
// cache exists so a song outlives the subscription that downloaded it; it must
// not keep playing an old, lower tier after the account can stream a better one.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

// cacheSong puts a decodable file in the cache at a given tier.
func cacheSong(t *testing.T, a *App, song ncm.Song, quality ncm.Quality) string {
	t.Helper()
	path := a.cache.Path(song.ID, quality)
	if err := mkdirAllFor(path); err != nil {
		t.Fatal(err)
	}
	if err := writeTestWAV(path, 8000, 0.5); err != nil {
		t.Fatal(err)
	}
	if _, found, ok := a.cache.Lookup(song.ID, quality); !ok {
		t.Fatalf("the fixture is not in the cache: %q (looked for %v)", found, quality)
	}
	return path
}

// A better tier than the cached one is fetched, not skipped in favour of the
// file on disk.
func TestABetterTierIsFetchedRatherThanTheCachedOne(t *testing.T) {
	a := testApp(t)
	// The account asks for lossless; the cache holds a standard file.
	a.config.Quality = int(ncm.QualityLossless)
	song := ncm.Song{ID: 7001, Name: "song", Duration: 2 * time.Second}
	cacheSong(t, a, song, ncm.QualityStandard)

	var asked ncm.Quality
	a.resolveURL = func(_ context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		asked = quality
		return []ncm.SongURL{{ID: song.ID, URL: "http://example.invalid/song", Quality: ncm.QualityLossless}}, nil
	}
	// The stream itself is not fetched here: what matters is that the cache was
	// not chosen over a better answer.
	a.resolveAndPlayAt(song, 0)
	a.sync()
	if asked != ncm.QualityLossless {
		t.Fatalf("the network was asked for %v, want lossless", asked)
	}
	a.engine.Close()
}

// When the account can no longer stream the song — an expired VIP, or no
// connection — the cached file plays instead. This is the feature the cache is
// for, so it must survive the tier preference above.
func TestTheCachedFilePlaysWhenTheNetworkCannot(t *testing.T) {
	a := testApp(t)
	a.config.Quality = int(ncm.QualityLossless)
	song := ncm.Song{ID: 7002, Name: "song", Duration: 2 * time.Second}
	cacheSong(t, a, song, ncm.QualityStandard)

	// The account is refused: the API answers a trial, which is not the song.
	a.resolveURL = func(context.Context, []int64, ncm.Quality) ([]ncm.SongURL, error) {
		return []ncm.SongURL{{ID: song.ID, URL: "http://example.invalid/trial", Quality: ncm.QualityStandard, FreeTrial: true}}, nil
	}
	a.resolveAndPlayAt(song, 0)
	a.sync()
	if got := a.engine.Snapshot().Status; got != player.StatusPlaying {
		t.Fatalf("the cached file did not play: status %v", got)
	}
	if track := a.queueAt; track != 0 {
		t.Logf("queue at %d", track)
	}
	a.engine.Close()
}

// A failed resolve falls back to the cache rather than skipping the song.
func TestTheCachedFilePlaysWhenTheResolveFails(t *testing.T) {
	a := testApp(t) // its transport refuses every request
	song := ncm.Song{ID: 7003, Name: "song", Duration: 2 * time.Second}
	cacheSong(t, a, song, a.songQuality(song))

	a.resolveAndPlayAt(song, 0)
	a.sync()
	if got := a.engine.Snapshot().Status; got != player.StatusPlaying {
		t.Fatalf("the cached file did not play: status %v", got)
	}
	a.engine.Close()
}

func mkdirAllFor(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
