package store

import (
	"os"
	"path/filepath"
	"testing"

	"ncmviva/internal/ncm"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	paths := Paths{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestSettingsRoundTrip(t *testing.T) {
	paths := testPaths(t)
	settings := NewSettings(paths)
	config, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.PreferredQuality() != ncm.QualityExtreme {
		t.Fatalf("default quality is %v", config.PreferredQuality())
	}
	config.Quality = int(ncm.QualityLossless)
	config.Volume = 0.42
	config.Repeat = RepeatOne
	if err := settings.Save(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewSettings(paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Quality != int(ncm.QualityLossless) || loaded.Volume != 0.42 || loaded.RepeatMode() != RepeatOne {
		t.Fatalf("reloaded %+v", loaded)
	}
}

func TestSettingsSurviveABrokenFile(t *testing.T) {
	paths := testPaths(t)
	if err := os.WriteFile(filepath.Join(paths.Config, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := NewSettings(paths).Load()
	if err == nil {
		t.Fatal("a broken config file did not report an error")
	}
	if config.PreferredQuality() != ncm.QualityExtreme {
		t.Fatalf("after a broken file the quality is %v", config.PreferredQuality())
	}
}

func TestCachePrefersTheExactQualityThenAnyOther(t *testing.T) {
	paths := testPaths(t)
	extra := filepath.Join(t.TempDir(), "old-cache")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := &Cache{Primary: filepath.Join(paths.Cache, "music"), Extra: []string{extra}}
	if err := os.MkdirAll(cache.Primary, 0o755); err != nil {
		t.Fatal(err)
	}
	const song = 347230

	// A file at the requested tier is found at that tier.
	if err := os.WriteFile(cache.Path(song, ncm.QualityExtreme), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, quality, ok := cache.Lookup(song, ncm.QualityExtreme)
	if !ok || quality != ncm.QualityExtreme {
		t.Fatalf("lookup at extreme: %v %v %v", path, quality, ok)
	}

	// A missing tier falls back to the one that is cached, so a lost VIP
	// subscription still plays the lossless file bought then.
	path, quality, ok = cache.Lookup(song, ncm.QualityLossless)
	if !ok || quality != ncm.QualityExtreme {
		t.Fatalf("lookup at lossless found %v %v", path, quality)
	}

	// An extra directory is searched too: this is the GTK client's cache.
	legacySong := int64(999)
	if err := os.WriteFile(filepath.Join(extra, Name(legacySong, ncm.QualityStandard)), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, quality, ok := cache.Lookup(legacySong, ncm.QualityStandard); !ok || quality != ncm.QualityStandard {
		t.Fatalf("the extra directory was not searched: %v", path)
	}

	// A file of no bytes is not a cache hit.
	if err := os.WriteFile(cache.Path(7, ncm.QualityExtreme), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.Lookup(7, ncm.QualityExtreme); ok {
		t.Fatal("an empty cache file was reported as playable")
	}
}

func TestCacheNameMatchesTheGTKClient(t *testing.T) {
	// The GTK client writes music_<id>_<rate>; a file it wrote must be found by
	// the name this cache builds, or the offline VIP library is lost.
	if got := Name(347230, ncm.QualityExtreme); got != "music_347230_320000" {
		t.Fatalf("cache name is %q", got)
	}
	if got := Name(347230, ncm.QualityLossless); got != "music_347230_999000" {
		t.Fatalf("lossless cache name is %q", got)
	}
	id, rate, ok := parseCacheName("music_347230_320000")
	if !ok || id != 347230 || rate != 320000 {
		t.Fatalf("parsed %d %d %v", id, rate, ok)
	}
}

func TestCacheOwnsOnlyItsDirectories(t *testing.T) {
	paths := testPaths(t)
	cache := &Cache{Primary: filepath.Join(paths.Cache, "music")}
	if err := os.MkdirAll(cache.Primary, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(cache.Primary, "music_1_320000")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cache.Owns(inside) {
		t.Fatal("a file inside the cache is not owned")
	}
	outside := filepath.Join(paths.Config, "config.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cache.Owns(outside) {
		t.Fatal("a file outside the cache is owned")
	}
	if err := cache.Remove(outside); err == nil {
		t.Fatal("Remove deleted a file outside the cache")
	}
	if err := cache.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatal("Remove left the file behind")
	}
}

func TestQueueRoundTrip(t *testing.T) {
	paths := testPaths(t)
	queue := Queue{Songs: []ncm.Song{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}, Index: 1}
	if err := NewQueueStore(paths).Save(queue); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := NewQueueStore(paths).Load()
	if err != nil || !ok {
		t.Fatalf("load: %v %v", ok, err)
	}
	if len(loaded.Songs) != 2 || loaded.Index != 1 || loaded.Songs[1].Name != "two" {
		t.Fatalf("reloaded %+v", loaded)
	}
}

func TestSessionClear(t *testing.T) {
	paths := testPaths(t)
	sessions := NewSessions(paths)
	if err := sessions.Save(Session{Cookies: map[string]string{"MUSIC_U": "token"}}); err != nil {
		t.Fatal(err)
	}
	session, ok, err := sessions.Load()
	if err != nil || !ok || session.Cookies["MUSIC_U"] != "token" {
		t.Fatalf("load: %+v %v %v", session, ok, err)
	}
	if err := sessions.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sessions.Load(); ok {
		t.Fatal("the session survived Clear")
	}
}

func TestLyricStoreAcceptsTheGTKLayout(t *testing.T) {
	paths := testPaths(t)
	store := NewLyricStore(paths)
	song := ncm.Song{ID: 42, Name: "a/b", Album: ncm.Album{Name: "album"}, Artists: []ncm.Artist{{Name: "singer"}}}

	if err := store.Save(song, "[00:01.00]hi", "[00:01.00]translated"); err != nil {
		t.Fatal(err)
	}
	raw, trans, ok := store.Load(song)
	if !ok || raw != "[00:01.00]hi" || trans != "[00:01.00]translated" {
		t.Fatalf("load: %q %q %v", raw, trans, ok)
	}

	// The legacy directory is read when the new one has nothing.
	legacyDir := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := &LyricStore{dir: filepath.Join(paths.Cache, "empty"), legacyDir: legacyDir}
	name := legacyName(song)
	if err := os.WriteFile(filepath.Join(legacyDir, name+".lrc"), []byte("[00:02.00]old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if raw, _, ok := legacy.Load(song); !ok || raw != "[00:02.00]old" {
		t.Fatalf("the legacy lyric was not read: %q %v (looked for %q)", raw, ok, name)
	}
}

func TestCoverStorePathsAreDistinctPerURLAndSize(t *testing.T) {
	paths := testPaths(t)
	covers := NewCoverStore(paths)
	a := covers.Path("https://p1.music.126.net/a.jpg", 200)
	b := covers.Path("https://p1.music.126.net/b.jpg", 200)
	c := covers.Path("https://p1.music.126.net/a.jpg", 400)
	if a == b || a == c {
		t.Fatal("two covers share a path")
	}
	if err := covers.Save("https://p1.music.126.net/a.jpg", 200, []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	if data, ok := covers.Load("https://p1.music.126.net/a.jpg", 200); !ok || string(data) != "jpeg" {
		t.Fatalf("cover load: %q %v", data, ok)
	}
	if _, ok := covers.Load("https://p1.music.126.net/a.jpg", 400); ok {
		t.Fatal("a size that was never saved was loaded")
	}
}
