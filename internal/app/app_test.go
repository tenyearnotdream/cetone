package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/app"

	"ncmviva/internal/ncm"
	"ncmviva/internal/store"
)

// A test drives the real page, painter, input router and focus rules with no
// window: app.NewDriver is the same code path a window runs.

// testApp builds an application over a temporary directory tree, with the
// network off by default (a test that needs a request serves it from a fake).
func testApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	paths := store.Paths{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
	}
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	// No test reaches the network: every request fails at once, so a page
	// settles into its error state instead of waiting for a timeout.
	a.client.SetTransport(offlineTransport{})
	t.Cleanup(a.engine.Close)
	return a
}

// offlineTransport refuses every request without a connection.
type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("test: the network is off")
}

// open runs the window's page in a driver.
func open(t *testing.T, a *App) *app.Driver {
	t.Helper()
	_, config := a.Page(app.Config{Title: "test", Width: 1160, Height: 740})
	driver, err := app.NewDriver(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(driver.Close)
	return driver
}

// pump runs the worker closures a page published and repaints until none is
// left. A driver has no clock of its own, so this is how a test waits for a
// request the page made.
func pump(t *testing.T, driver *app.Driver, a *App) {
	t.Helper()
	// A request starts on a worker, so "nothing queued" is only settled after it
	// has had a chance to run: quiet must hold for a few rounds.
	quiet := 0
	for i := 0; i < 200; i++ {
		a.sync()
		// Paint explicitly: a driver only repaints when it is asked to, and a
		// worker's wake is consumed by Pump.
		if err := driver.Pump(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		if a.work.quiet() {
			quiet++
			if quiet >= 4 {
				return
			}
			continue
		}
		quiet = 0
	}
}

// click presses the middle of the control with the given name.
func click(t *testing.T, driver *app.Driver, name string) {
	t.Helper()
	for _, hit := range driver.Snapshot().Hits {
		if hit.Label == name && !hit.IsLabel {
			b := hit.Bounds
			if err := driver.Click(b.X+b.Width/2, b.Y+b.Height/2); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no control named %q on screen", name)
}

// shows reports whether some label contains the text.
func shows(driver *app.Driver, want string) bool {
	for _, label := range driver.Snapshot().Labels {
		if strings.Contains(label.Value, want) {
			return true
		}
	}
	return false
}

func TestTheWindowOpensOnDiscover(t *testing.T) {
	a := testApp(t)
	driver := open(t, a)
	if !shows(driver, "发现") {
		t.Fatal("the sidebar does not name the discover page")
	}
	// Before the request settles the page says it is loading, rather than
	// showing an empty page.
	if !shows(driver, "正在加载推荐内容") {
		t.Fatal("the discover page shows no loading state")
	}
	pump(t, driver, a)
	if !shows(driver, "今日想听点什么") && !shows(driver, "登录后同步你的音乐") {
		t.Fatal("the discover page shows neither the greeting nor the login invitation")
	}
}

func TestSidebarSwitchesSections(t *testing.T) {
	a := testApp(t)
	driver := open(t, a)
	click(t, driver, "排行榜")
	pump(t, driver, a)
	if !shows(driver, "排行榜") {
		t.Fatal("the toplist page did not open")
	}
	click(t, driver, "设置")
	pump(t, driver, a)
	if !shows(driver, "缓存") {
		t.Fatal("the settings page has no cache section")
	}
}

func TestSearchBeforeAKeywordShowsItsLanding(t *testing.T) {
	a := testApp(t)
	a.config.SearchHistory = []string{"海阔天空"}
	driver := open(t, a)
	click(t, driver, "搜索")
	pump(t, driver, a)
	if !shows(driver, "搜索音乐") {
		t.Fatal("the search landing did not show")
	}
	if !shows(driver, "海阔天空") {
		t.Fatal("the search history is not on screen")
	}
}

// The cache is the feature the application exists for: a song whose rights
// lapsed plays from the disk. This test writes a cache file the GTK client
// would have written and checks the player plays it without asking for a URL.
func TestACachedSongPlaysWithoutAskingTheServer(t *testing.T) {
	a := testApp(t)
	// A one-second wav under the cache name of song 347230 at 320 kb/s: the
	// name is the GTK client's, and the player sniffs the container, so the
	// file plays whatever its extension.
	path := a.cache.Path(347230, ncm.QualityExtreme)
	writeTestWav(t, path, 8000, 2, 0.4)

	song := ncm.Song{
		ID:       347230,
		Name:     "海阔天空",
		Artists:  []ncm.Artist{{ID: 11127, Name: "Beyond"}},
		Album:    ncm.Album{ID: 1, Name: "乐与怒"},
		Duration: 400 * 1000 * 1000,
		Fee:      1,
	}
	// The account's rights say it is not streamable any more; the cache still
	// plays, which is exactly what Copyright.Playable does not decide.
	song.Privilege = ncm.Privilege{Status: -1}
	if song.Copyright().Playable() {
		t.Fatal("the fixture is meant to be an unplayable song")
	}

	a.engine.NewSink = func() playerSink { return &countingSink{} }
	a.playSongs([]ncm.Song{song}, 0)
	waitFor(t, a, func() bool {
		snapshot := a.engine.Snapshot()
		return snapshot.Status.String() == "failed" || snapshot.Position >= 150*time.Millisecond
	})
	snapshot := a.engine.Snapshot()
	if snapshot.Status.String() == "failed" {
		t.Fatalf("the cached song failed to play: %s", snapshot.Error)
	}
	if snapshot.Position < 150*time.Millisecond {
		t.Fatalf("the cached song played to %v only", snapshot.Position)
	}
}

// A song whose rights lapsed is dropped when the setting says to skip
// unplayable songs, but kept when it is cached: that exception is the client's
// whole point.
func TestUnplayableButCachedSongsSurviveFiltering(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 42, Name: "gone", Fee: 1, Privilege: ncm.Privilege{Status: -1}}
	if song.Copyright().Playable() {
		t.Fatal("the fixture is meant to be unplayable")
	}
	a.config.IgnoreUnplayable = true
	a.cached.setDirty()
	if kept := a.playable([]ncm.Song{song}); len(kept) != 0 {
		t.Fatal("an unplayable song that is not cached survived the filter")
	}

	writeTestWav(t, a.cache.Path(42, ncm.QualityExtreme), 8000, 2, 0.2)
	a.cached.setDirty()
	if kept := a.playable([]ncm.Song{song}); len(kept) != 1 {
		t.Fatal("the cached copy of an unplayable song was filtered out")
	}
}

// The list's rows are named by the songs' own ids, so a reorder cannot show one
// song's heart on another.
func TestQueueRowsAreNamedBySong(t *testing.T) {
	a := testApp(t)
	a.queue = []ncm.Song{{ID: 11, Name: "a"}, {ID: 22, Name: "b"}, {ID: 33, Name: "c"}}
	a.initQueueList()
	if got := a.queueList.Key(1); got != 22 {
		t.Fatalf("row 1 is keyed %d, want 22", got)
	}
	if got := a.queueList.Label(2); got != "c" {
		t.Fatalf("row 2 is named %q", got)
	}
}

// Dragging a row in the queue keeps the song that is playing playing.
func TestReorderingTheQueueKeepsTheCurrentSong(t *testing.T) {
	a := testApp(t)
	a.queue = []ncm.Song{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}}
	a.queueAt = 1
	a.reorderQueue([]int{2}, 0)
	if len(a.queue) != 3 || a.queue[0].ID != 3 || a.queue[1].ID != 1 || a.queue[2].ID != 2 {
		t.Fatalf("the order is %v", a.queue)
	}
	if a.queueAt != 2 {
		t.Fatalf("the cursor is %d, want the song that was playing", a.queueAt)
	}
}

// The cache index is what makes a thousand-row list cheap: it reads the
// directories once and reports the best cached tier of each song.
func TestCacheIndexReadsTheDirectoriesOnce(t *testing.T) {
	a := testApp(t)
	writeTestWav(t, a.cache.Path(1, ncm.QualityStandard), 8000, 2, 0.1)
	writeTestWav(t, a.cache.Path(1, ncm.QualityLossless), 8000, 2, 0.1)
	writeTestWav(t, a.cache.Path(2, ncm.QualityExtreme), 8000, 2, 0.1)
	a.cached.setDirty()
	if quality, ok := a.cached.quality(1); !ok || quality != ncm.QualityLossless {
		t.Fatalf("song 1 is reported at %v (%v)", quality, ok)
	}
	if !a.cached.has(2) {
		t.Fatal("song 2 was not found in the cache")
	}
	if a.cached.has(3) {
		t.Fatal("a song that was never cached was reported")
	}
	// A file that arrives later is picked up once the index is told.
	writeTestWav(t, a.cache.Path(3, ncm.QualityExtreme), 8000, 2, 0.1)
	if a.cached.has(3) {
		t.Fatal("the index picked up a file without being told")
	}
	a.cached.setDirty()
	if !a.cached.has(3) {
		t.Fatal("the index did not rescan after being told")
	}
}

// A cache file that will not decode falls back to the network: an interrupted
// download from an older client must not stop the queue, and the file stays
// where it is because it is the user's.
func TestABrokenCacheFileFallsBackToTheNetwork(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 7, Name: "not audio", Duration: time.Minute}
	path := a.cache.Path(7, a.config.PreferredQuality())
	// Bytes in neither mp3 nor flac: an HTML error page saved under the cache
	// name, which is what a failed download used to leave behind.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<html><body>502 Bad Gateway</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cached.setDirty()
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	a.resolveAndPlay(song)
	// The network is off in this test, so the fall-back is visible as the
	// player's failure mentioning the cache and not as a silent stop.
	waitFor(t, a, func() bool {
		return a.engine.Snapshot().Status.String() == "failed" || a.toaster.Len() > 0
	})
	if a.toaster.Len() == 0 {
		t.Fatal("a broken cache file produced no message")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal("the broken cache file was deleted")
	}
}

// Playing from the cache never asks the API for a URL: the transport advances
// with the network off, which is what makes the offline library work.
func TestACachedSongPlaysWithTheNetworkOff(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 99, Name: "offline", Duration: 2 * time.Second, Fee: 1}
	song.Privilege = ncm.Privilege{Status: -1}
	writeTestWav(t, a.cache.Path(99, a.config.PreferredQuality()), 8000, 2, 1.2)
	a.cached.setDirty()
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	a.resolveAndPlay(song)
	waitFor(t, a, func() bool { return a.engine.Snapshot().Position >= 200*time.Millisecond })
	if status := a.engine.Snapshot().Status.String(); status == "failed" {
		t.Fatalf("the cached song failed with the network off: %s", a.engine.Snapshot().Error)
	}
}

// The search page's sweep: a keyword typed into the header field, Enter, and
// the kinds' tabs switch which result is shown. The network is off, so the
// search fails, which is exactly the state the page must render honestly.
func TestSearchFromTheHeaderRendersItsFailure(t *testing.T) {
	a := testApp(t)
	driver := open(t, a)
	a.search.field = "test"
	a.openSearch("test")
	pump(t, driver, a)
	if !shows(driver, "加载失败") {
		t.Fatal("a search that failed did not say so")
	}
	if !shows(driver, "重试") {
		t.Fatal("a failed search offers no retry")
	}
	// The history still records the keyword, so the landing page offers it
	// again after a restart.
	found := false
	for _, keyword := range a.config.SearchHistory {
		if keyword == "test" {
			found = true
		}
	}
	if !found {
		t.Fatal("the keyword was not added to the search history")
	}
}

// Settings are stored as they change, so a restart keeps them.
func TestSettingsAreStoredWhenChanged(t *testing.T) {
	a := testApp(t)
	a.config.Quality = int(ncm.QualityLossless)
	a.saveConfig()
	loaded, err := store.NewSettings(a.paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Quality != int(ncm.QualityLossless) {
		t.Fatalf("the saved quality is %d", loaded.Quality)
	}
}

// The repeat button cycles the three modes and stores the one it lands on.
func TestRepeatCyclesThroughThreeModes(t *testing.T) {
	a := testApp(t)
	a.config.Repeat = store.RepeatList
	want := []string{store.RepeatOne, store.RepeatOff, store.RepeatList}
	for _, expected := range want {
		a.cycleRepeat()
		if a.config.RepeatMode() != expected {
			t.Fatalf("the repeat mode is %q, want %q", a.config.RepeatMode(), expected)
		}
	}
	loaded, err := store.NewSettings(a.paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RepeatMode() != store.RepeatList {
		t.Fatalf("the stored repeat mode is %q", loaded.RepeatMode())
	}
}

// The player bar's seek slider writes the engine's position: dragging it to the
// middle of a one-second song seeks to half a second.
func TestTheSeekSliderMovesTheTransport(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 5, Name: "seek me", Duration: 2 * time.Second}
	writeTestWav(t, a.cache.Path(5, a.config.PreferredQuality()), 8000, 2, 2)
	a.cached.setDirty()
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	a.playAt(0)
	waitFor(t, a, func() bool { return a.engine.Snapshot().Position > 100*time.Millisecond })
	if err := a.engine.Seek(time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	position := a.engine.Snapshot().Position
	if position < 900*time.Millisecond || position > 1400*time.Millisecond {
		t.Fatalf("after seeking to 1s the position is %v", position)
	}
}

// A trial fragment must never be written to the cache: it would replace the song
// on disk with a thirty-second clip, and every later play would come from it.
func TestATrialFragmentIsNotCached(t *testing.T) {
	song := ncm.Song{ID: 1, Name: "VIP song", Duration: 4 * time.Minute}
	// What the API answers an account that may not stream the song: a short
	// preview with FreeTrial set.
	trial := ncm.SongURL{ID: 1, URL: "https://example.invalid/a.mp3", Size: 481115, FreeTrial: true, Quality: ncm.QualityStandard}
	if cacheableStream(song, trial) {
		t.Fatal("a trial fragment was accepted for the cache")
	}
	// A full answer for the same song is cached.
	full := ncm.SongURL{ID: 1, URL: "https://example.invalid/a.mp3", Size: 6400000, Quality: ncm.QualityStandard}
	if !cacheableStream(song, full) {
		t.Fatal("a full song was refused the cache")
	}
	// A size that cannot be the whole song is a fragment even without the flag.
	truncated := ncm.SongURL{ID: 1, URL: "https://example.invalid/a.mp3", Size: 100000, Quality: ncm.QualityStandard}
	if cacheableStream(song, truncated) {
		t.Fatal("a 100 kB answer for a four-minute song was cached")
	}
	// A very short song is not worth a cache entry, as the GTK client decided.
	short := ncm.Song{ID: 2, Name: "jingle", Duration: 12 * time.Second}
	if cacheableStream(short, ncm.SongURL{Size: 100000}) {
		t.Fatal("a twelve-second song was cached")
	}
}

// A real trial answer from the API does not leave a file behind, and the window
// says why.
func TestPlayingATrialLeavesTheCacheEmpty(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 77, Name: "VIP song", Duration: 4 * time.Minute, Fee: 1}
	resolved := ncm.SongURL{ID: 77, URL: "https://example.invalid/a.mp3", Size: 481115, FreeTrial: true, Quality: ncm.QualityExtreme}
	if cacheableStream(song, resolved) {
		t.Fatal("the fixture is meant to be a trial")
	}
	// Nothing was written, so a lookup finds nothing and the next play streams
	// rather than serving a clip.
	if _, _, ok := a.cache.Lookup(song.ID, a.config.PreferredQuality()); ok {
		t.Fatal("a trial fragment was found in the cache")
	}
}

// slowStream serves a wav fixture in throttled chunks.
func slowStream(t *testing.T, seconds float64) *httptest.Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.wav")
	writeTestWav(t, path, 8000, 2, seconds)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		const chunks = 30
		step := len(data)/chunks + 1
		for offset := 0; offset < len(data); offset += step {
			end := min(offset+step, len(data))
			if _, err := w.Write(data[offset:end]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(25 * time.Millisecond)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// A song resolved from the API keeps playing after the resolution returns. The
// regression: the request's own context reached the playback session, so the
// deferred cancel ended the download, the decoder read that as the end of the
// track, and the partial file became the cache entry.
func TestPlaybackOutlivesTheResolutionRequest(t *testing.T) {
	a := testApp(t)
	server := slowStream(t, 4)
	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		return []ncm.SongURL{{
			ID:      5,
			URL:     server.URL,
			Size:    900_000,
			Quality: ncm.QualityExtreme,
		}}, nil
	}
	song := ncm.Song{ID: 5, Name: "streamed", Duration: 4 * time.Second}
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	a.resolveAndPlay(song)

	waitFor(t, a, func() bool { return a.engine.Snapshot().Position > 500*time.Millisecond })
	snapshot := a.engine.Snapshot()
	if snapshot.Status.String() != "playing" {
		t.Fatalf("the song stopped after its request returned: %v (%s)", snapshot.Status, snapshot.Error)
	}
	if got := a.engine.Snapshot().Position; got < 500*time.Millisecond {
		t.Fatalf("the song advanced to %v only", got)
	}
}
