package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

func serveWAVBytes(t *testing.T, seconds float64) (*httptest.Server, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track.wav")
	writeTestWav(t, path, 8000, 2, seconds)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server, int64(len(data))
}

// TestRapidSongSwitchCancelsStaleResolveAndResetsProgress verifies that
// switching songs while the previous song is still resolving immediately resets
// the engine progress/metadata to the new song and ignores the stale resolve.
func TestRapidSongSwitchCancelsStaleResolveAndResetsProgress(t *testing.T) {
	a := testApp(t)
	a.engine.NewSink = func() playerSink { return &countingSink{} }

	songA := ncm.Song{ID: 8101, Name: "慢歌A", Duration: 4 * time.Minute}
	songB := ncm.Song{ID: 8102, Name: "本地歌B", Duration: 3 * time.Second}
	writeTestWav(t, a.cache.Path(songB.ID, ncm.QualityHiRes), 8000, 2, 3)
	a.recordCachedAsBest(songB.ID, a.cache.Path(songB.ID, ncm.QualityHiRes), ncm.QualityHiRes, 11)
	a.setUser(ncm.UserInfo{UserID: 1, VIPType: 11})
	a.cached.SetDirty()

	serverA, sizeA := serveWAVBytes(t, 3)
	unblockA := make(chan struct{})
	aStarted := make(chan struct{}, 1)
	aCancelled := make(chan struct{}, 1)

	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		if len(ids) > 0 && ids[0] == songA.ID {
			select {
			case aStarted <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				select {
				case aCancelled <- struct{}{}:
				default:
				}
				return nil, ctx.Err()
			case <-unblockA:
				return []ncm.SongURL{{ID: songA.ID, URL: serverA.URL, Size: sizeA, Quality: ncm.QualityLossless}}, nil
			}
		}
		return nil, nil
	}

	a.queue = []ncm.Song{songA, songB}
	a.playAt(0)
	// Prepare must immediately show song A at 0:00 buffering.
	if snap := a.engine.Snapshot(); snap.SongID != songA.ID || snap.Position != 0 {
		t.Fatalf("after playAt(0) snapshot = %+v, want song A at 0", snap)
	}
	<-aStarted

	// Immediately switch to song B before song A resolves.
	a.playAt(1)
	select {
	case <-aCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("switching to song B did not cancel song A's in-flight resolve")
	}
	close(unblockA)

	waitFor(t, a, func() bool {
		snap := a.engine.Snapshot()
		return snap.SongID == songB.ID && snap.Status == player.StatusPlaying && snap.Position >= 150*time.Millisecond
	})
	snap := a.engine.Snapshot()
	if snap.SongID != songB.ID {
		t.Fatalf("stale song A hijacked playback: snapshot = %+v", snap)
	}
}

// TestQualityBadgeMenuSwitchesQualityAtCurrentPosition drives the real player
// bar quality badge: clicking it opens the menu, picking 标准 switches at the
// current position without showing a fake trial toast, keeps 标准 visible, and
// preserves the 无损 ceiling so the user can switch back to 自动（最高音质）.
func TestQualityBadgeMenuSwitchesQualityAtCurrentPosition(t *testing.T) {
	a := testApp(t)
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	a.setUser(ncm.UserInfo{UserID: 1, Nickname: "VIP", VIPType: 11})

	song := ncm.Song{ID: 8201, Name: "音质切换测试", Duration: 4 * time.Second}
	losslessPath := a.cache.Path(song.ID, ncm.QualityLossless)
	writeTestWav(t, losslessPath, 8000, 2, 4)
	a.recordCachedAsBest(song.ID, losslessPath, ncm.QualityLossless, 11)
	a.cached.SetDirty()

	serverStd, sizeStd := serveWAVBytes(t, 4)
	var askedQuality atomic.Int64
	var resolveCalls atomic.Int32
	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		resolveCalls.Add(1)
		askedQuality.Store(int64(quality))
		return []ncm.SongURL{{
			ID:      song.ID,
			URL:     serverStd.URL,
			Size:    sizeStd,
			Quality: quality,
		}}, nil
	}

	driver := open(t, a)
	a.queue = []ncm.Song{song}
	a.playAt(0)
	waitFor(t, a, func() bool {
		snap := a.engine.Snapshot()
		return snap.Status == player.StatusPlaying && snap.Position >= 500*time.Millisecond
	})
	pump(t, driver, a)

	if !shows(driver, "无损") {
		t.Fatalf("player bar did not show active quality 无损")
	}
	if resolveCalls.Load() != 0 {
		t.Fatalf("cached best quality triggered %d network resolves, want 0", resolveCalls.Load())
	}

	// Open the quality menu from the player bar badge.
	click(t, driver, "音质")
	pump(t, driver, a)
	if !a.qualityMenuOpen {
		t.Fatal("clicking the quality badge did not open the menu")
	}
	if !shows(driver, "自动（最高音质）") || !shows(driver, "标准") {
		t.Fatal("quality menu is missing expected options")
	}

	// Switch down to 标准.
	posBefore := a.engine.Snapshot().Position
	click(t, driver, "标准")
	waitFor(t, a, func() bool {
		a.sync()
		return a.queue[0].ActualSet && a.queue[0].Actual == ncm.QualityStandard && a.engine.Snapshot().Status == player.StatusPlaying
	})
	pump(t, driver, a)

	if got := ncm.Quality(askedQuality.Load()); got != ncm.QualityStandard {
		t.Fatalf("asked server for %v, want QualityStandard", got)
	}
	if snap := a.engine.Snapshot(); snap.Position < posBefore-150*time.Millisecond {
		t.Fatalf("switching quality reset position from %v back to %v", posBefore, snap.Position)
	}
	if a.toaster.Len() != 0 {
		t.Fatalf("switching to a lower full-length tier emitted unexpected toast(s)")
	}
	if !shows(driver, "标准") {
		t.Fatal("player bar hid 标准 badge after switching to QualityStandard")
	}

	// Reopen the menu: the ceiling 无损 must still be offered!
	click(t, driver, "音质")
	pump(t, driver, a)
	if !shows(driver, "无损") || !shows(driver, "已本地缓存") {
		t.Fatal("quality menu lost the cached 无损 ceiling after stepping down to 标准")
	}

	// Switch back to 自动（最高音质）: must hit cached 无损 without extra network calls.
	beforeAuto := resolveCalls.Load()
	click(t, driver, "自动（最高音质）")
	waitFor(t, a, func() bool {
		a.sync()
		return !a.queue[0].SelectedSet && a.queue[0].Actual == ncm.QualityLossless && a.engine.Snapshot().Status == player.StatusPlaying
	})
	if resolveCalls.Load() != beforeAuto {
		t.Fatalf("switching back to auto cached best triggered %d network calls", resolveCalls.Load()-beforeAuto)
	}
}

// TestVIPHighestQualityCachePolicy verifies the core offline cache contract:
//   - VIP caches the highest available tier and records it in library.json.
//   - Subsequent plays hit cache with zero network requests.
//   - After VIP expires (VIPType drops to 0) and network is off, the cached
//     highest-quality file still plays directly.
//   - Upgrading VIP probes once to upgrade an older lower-tier cache file.
func TestVIPHighestQualityCachePolicy(t *testing.T) {
	a := testApp(t)
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	serverHiRes, sizeHiRes := serveWAVBytes(t, 2)

	song := ncm.Song{ID: 8301, Name: "VIP无损收藏", Duration: 45 * time.Second, Fee: 1}
	var resolves atomic.Int32
	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		resolves.Add(1)
		if quality != ncm.QualityMaster {
			t.Errorf("default probe asked for %v, want QualityMaster", quality)
		}
		return []ncm.SongURL{{
			ID:      song.ID,
			URL:     serverHiRes.URL,
			Size:    max(sizeHiRes, 1_000_000),
			Quality: ncm.QualityHiRes,
		}}, nil
	}

	// Phase 1: VIP active -> resolves & caches Hi-Res.
	a.setUser(ncm.UserInfo{UserID: 1, VIPType: 11})
	a.queue = []ncm.Song{song}
	a.playAt(0)
	waitFor(t, a, func() bool {
		_, q, ok := a.cache.LookupBest(song.ID)
		return ok && q == ncm.QualityHiRes
	})
	a.sync()
	if resolves.Load() != 1 {
		t.Fatalf("phase 1 resolves = %d, want 1", resolves.Load())
	}
	entry, ok := a.library.Get(song.ID)
	if !ok || entry.Best != ncm.QualityHiRes || entry.ProbedVIP != 11 {
		t.Fatalf("library entry = %+v (%v), want Best=HiRes ProbedVIP=11", entry, ok)
	}

	// Phase 2: Replay while still VIP -> hits disk with zero network requests.
	a.engine.Stop()
	a.playAt(0)
	waitFor(t, a, func() bool { return a.engine.Snapshot().Status == player.StatusPlaying })
	if resolves.Load() != 1 {
		t.Fatalf("phase 2 re-probed network (%d calls) despite best already cached", resolves.Load())
	}

	// Phase 3: VIP expired (VIPType = 0) & network offline -> still plays cached Hi-Res.
	a.engine.Stop()
	a.setUser(ncm.UserInfo{UserID: 1, VIPType: 0})
	a.resolveURL = func(context.Context, []int64, ncm.Quality) ([]ncm.SongURL, error) {
		resolves.Add(1)
		return nil, os.ErrPermission
	}
	a.playAt(0)
	waitFor(t, a, func() bool { return a.engine.Snapshot().Status == player.StatusPlaying })
	a.sync()
	if resolves.Load() != 1 {
		t.Fatalf("expired VIP hit network (%d calls) instead of playing cached Hi-Res", resolves.Load())
	}
	if a.queue[0].Actual != ncm.QualityHiRes {
		t.Fatalf("expired VIP played %v, want HiRes", a.queue[0].Actual)
	}

	// Phase 4: Song cached at Standard under VIP=0, then user upgrades to VIP=11.
	song2 := ncm.Song{ID: 8302, Name: "升级音质", Duration: 45 * time.Second}
	stdPath := a.cache.Path(song2.ID, ncm.QualityStandard)
	writeTestWav(t, stdPath, 8000, 2, 2)
	_ = a.library.Put(store.CacheEntry{
		SongID:    song2.ID,
		Quality:   ncm.QualityStandard,
		File:      filepath.Base(stdPath),
		Best:      ncm.QualityStandard,
		ProbedVIP: 0,
		SavedAt:   time.Now(),
	})
	a.cached.SetDirty()

	var upgraded atomic.Bool
	a.setUser(ncm.UserInfo{UserID: 1, VIPType: 11})
	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		upgraded.Store(true)
		return []ncm.SongURL{{
			ID:      song2.ID,
			URL:     serverHiRes.URL,
			Size:    max(sizeHiRes, 1_000_000),
			Quality: ncm.QualityHiRes,
		}}, nil
	}
	a.queue = []ncm.Song{song2}
	a.playAt(0)
	waitFor(t, a, func() bool {
		_, q, ok := a.cache.LookupBest(song2.ID)
		return ok && q == ncm.QualityHiRes
	})
	if !upgraded.Load() {
		t.Fatal("upgraded VIP did not probe to upgrade an older Standard cache file")
	}
}

// TestCachePlaylistRequestsHighestQualityAndSkipsTrials verifies bulk caching
// asks for QualityMaster, skips already-best songs and FreeTrial previews, and
// writes library metadata so cached tracks play offline immediately.
func TestCachePlaylistRequestsHighestQualityAndSkipsTrials(t *testing.T) {
	a := testApp(t)
	a.setUser(ncm.UserInfo{UserID: 1, VIPType: 11})
	server, size := serveWAVBytes(t, 1)

	alreadyCached := ncm.Song{ID: 8401, Name: "已缓存最高", Duration: 3 * time.Minute}
	toCache := ncm.Song{ID: 8402, Name: "可缓存无损", Duration: 3 * time.Minute}
	trialOnly := ncm.Song{ID: 8403, Name: "仅试听", Duration: 3 * time.Minute}

	p1 := a.cache.Path(alreadyCached.ID, ncm.QualityLossless)
	writeTestWav(t, p1, 8000, 2, 1)
	a.recordCachedAsBest(alreadyCached.ID, p1, ncm.QualityLossless, 11)
	a.cached.SetDirty()

	var askedIDs []int64
	a.resolveURL = func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error) {
		if quality != ncm.QualityMaster {
			t.Errorf("cachePlaylist asked for %v, want QualityMaster", quality)
		}
		askedIDs = append(askedIDs, ids...)
		switch ids[0] {
		case toCache.ID:
			return []ncm.SongURL{{
				ID:      toCache.ID,
				URL:     server.URL,
				Size:    max(size, 4_000_000),
				Quality: ncm.QualityHiRes,
			}}, nil
		case trialOnly.ID:
			return []ncm.SongURL{{
				ID:        trialOnly.ID,
				URL:       server.URL,
				Size:      480_000,
				Quality:   ncm.QualityStandard,
				FreeTrial: true,
			}}, nil
		default:
			return nil, nil
		}
	}

	a.cacheSongs([]ncm.Song{alreadyCached, toCache, trialOnly})
	waitFor(t, a, func() bool {
		a.sync()
		_, q, ok := a.cache.LookupBest(toCache.ID)
		return ok && q == ncm.QualityHiRes && a.toaster.Len() >= 2
	})

	for _, id := range askedIDs {
		if id == alreadyCached.ID {
			t.Fatal("cacheSongs re-requested a song whose highest quality was already cached")
		}
	}
	if _, _, ok := a.cache.LookupBest(trialOnly.ID); ok {
		t.Fatal("cacheSongs cached a FreeTrial preview clip")
	}
	entry, ok := a.library.Get(toCache.ID)
	if !ok || entry.Best != ncm.QualityHiRes || entry.ProbedVIP != 11 {
		t.Fatalf("cacheSongs did not record library metadata: %+v (%v)", entry, ok)
	}
}
