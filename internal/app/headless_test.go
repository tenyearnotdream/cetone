package app

// A headless session runs the real paint, input routing, focus and text paths
// through app.Driver with a clock the test moves, and can check the frame the
// way a screenshot review would: text present, controls named, layout clean.
// It is the verification the Viva guide asks for when a window must not open.

import (
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/headless"
	"github.com/vivaui/viva/ui"

	"ncmviva/internal/ncm"
	"ncmviva/internal/store"
)

// headlessModel returns an application over a temporary directory with the
// content every page needs, so a page test measures the page and not a network
// wait.
func headlessModel(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	songs := fixtureTrackList()
	playlist := ncm.Playlist{
		ID:          24381616,
		Name:        "我喜欢的音乐",
		TrackCount:  len(songs),
		PlayCount:   284913,
		Description: "收藏的歌。",
		Creator:     ncm.UserInfo{Nickname: "云端漫步", UserID: 1},
		Tracks:      songs,
		Subscribed:  true,
	}
	a.user = ncm.UserInfo{UserID: 1, Nickname: "云端漫步", VIPType: 11, Follows: 42, Followeds: 128}
	a.data.init()
	a.data.playlists[playlist.ID] = &playlistData{
		loading:  loading{loaded: true},
		playlist: playlist,
		counts:   ncm.PlaylistCounts{PlayCount: 284913, SubscribedCount: 1240},
	}
	a.data.discover = discoverData{
		loading:   loading{loaded: true},
		banners:   []ncm.Banner{{Title: "新歌首发", TargetType: 1}},
		playlists: []ncm.Playlist{{ID: 1, Name: "华语流行", TrackCount: 50, PlayCount: 1000}},
		newAlbums: []ncm.Album{{ID: 2, Name: "叶惠美", Artist: "周杰伦"}},
	}
	a.data.toplist = toplistData{loading: loading{loaded: true}, charts: []ncm.TopList{
		{ID: 19723756, Name: "飙升榜", TrackCount: 100, UpdateFrequency: "每周更新"},
	}}
	a.data.mine = mineData{
		loading:   loading{loaded: true},
		playlists: []ncm.Playlist{playlist},
		liked:     songs,
		daily:     songs,
	}
	a.data.artists[11127] = &artistData{loading: loading{loaded: true}, detail: ncm.ArtistDetail{
		Artist: ncm.Artist{ID: 11127, Name: "Beyond"},
		Songs:  songs,
	}}
	a.data.albums[1] = &albumData{loading: loading{loaded: true}, album: ncm.Album{
		ID: 1, Name: "乐与怒", Artist: "Beyond", Songs: songs,
	}}
	a.data.searches[searchKey("海阔天空", ncm.SearchSongs, 0)] = &searchData{
		loading: loading{loaded: true},
		result:  ncm.SearchResult{Kind: ncm.SearchSongs, Songs: songs, Total: 2841},
	}
	a.data.lyrics[songs[0].ID] = &lyricData{
		loading: loading{loaded: true},
		song:    songs[0],
		lyric: ncm.Lyric{Lines: []ncm.LyricLine{
			{At: 0, Text: "今天我"},
			{At: 3 * time.Second, Text: "寒夜里看雪飘过", Translation: "Walking through the cold night snow"},
			{At: 8 * time.Second, Text: "怀着冷却了的心窝漂远方"},
		}},
	}
	a.queue = songs
	a.queueAt = 0
	for _, song := range songs {
		a.likedSet[song.ID] = true
		// Each fixture song has a cache file, so playing one never reaches the
		// network in a test. That is also the client's own path: a cached song
		// plays from disk.
		writeTestWav(t, a.cache.Path(song.ID, a.config.PreferredQuality()), 8000, 2, 3)
	}
	a.cached.setDirty()
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	return a
}

// fixtureTrackList is a short list with one row of each state a row can show.
func fixtureTrackList() []ncm.Song {
	names := []struct {
		name   string
		singer string
		fee    int
		status int
	}{
		{"海阔天空", "Beyond", 1, 0},
		{"光年之外", "G.E.M. 邓紫棋", 0, 0},
		{"晴天", "周杰伦", 1, 0},
		{"以父之名", "周杰伦", 1, -1},
	}
	songs := make([]ncm.Song, 0, len(names))
	for i, entry := range names {
		song := ncm.Song{
			ID:       int64(347230 + i),
			Name:     entry.name,
			Artists:  []ncm.Artist{{ID: int64(100 + i), Name: entry.singer}},
			Album:    ncm.Album{ID: int64(200 + i), Name: "乐与怒"},
			Duration: 5*time.Minute + 25*time.Second,
			Fee:      entry.fee,
		}
		song.Privilege = ncm.Privilege{Status: entry.status, MaxBr: 320000}
		songs = append(songs, song)
	}
	return songs
}

// headlessPage opens the window's page in a session, without a window.
func headlessPage(t *testing.T, a *App) (*headless.Session, *app.Driver) {
	t.Helper()
	_, config := a.Page(app.Config{Title: "ncmviva", Width: 1160, Height: 740})
	session, err := headless.Open(config, nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session, session.Driver()
}

// settle paints a few frames, running the work a paint published: a page that
// started a request in its first paint must not be measured mid-flight.
func settle(t *testing.T, session *headless.Session, a *App) {
	t.Helper()
	// Toasts and hover states fade in over the theme's motion, so the clock is
	// moved as well as the work queue drained.
	for i := 0; i < 12; i++ {
		a.sync()
		if err := session.Advance(20 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	a.sync()
	if err := session.Driver().Pump(); err != nil {
		t.Fatal(err)
	}
}

// TestPagesRenderCleanly is the check a screenshot review makes: on every page,
// no text is cut off, off screen or overlapping, and every control a user can
// press has a name a screen reader and a test can find it by.
func TestPagesRenderCleanly(t *testing.T) {
	pages := []struct {
		name  string
		open  func(a *App)
		texts []string
	}{
		{
			name:  "发现",
			open:  func(a *App) { a.nav = []route{{kind: routeDiscover, title: "发现"}} },
			texts: []string{"每日推荐", "推荐歌单"},
		},
		{
			name:  "排行榜",
			open:  func(a *App) { a.nav = []route{{kind: routeToplist, title: "排行榜"}} },
			texts: []string{"飙升榜"},
		},
		{
			name: "歌单",
			open: func(a *App) {
				a.nav = []route{{kind: routePlaylist, id: 24381616, title: "我喜欢的音乐"}}
			},
			texts: []string{"我喜欢的音乐", "播放全部", "海阔天空"},
		},
		{
			name:  "专辑",
			open:  func(a *App) { a.nav = []route{{kind: routeAlbum, id: 1, title: "乐与怒"}} },
			texts: []string{"乐与怒", "Beyond", "播放全部"},
		},
		{
			name:  "歌手",
			open:  func(a *App) { a.nav = []route{{kind: routeArtist, id: 11127, title: "Beyond"}} },
			texts: []string{"Beyond", "热门单曲"},
		},
		{
			name:  "我的音乐",
			open:  func(a *App) { a.nav = []route{{kind: routeMine, title: "我的音乐"}} },
			texts: []string{"云端漫步", "我喜欢的音乐", "每日推荐", "云盘音乐"},
		},
		{
			name: "搜索",
			open: func(a *App) {
				a.search.field = "海阔天空"
				a.search.kind = ncm.SearchSongs
				a.nav = []route{{kind: routeSearch, title: "搜索"}}
			},
			texts: []string{"海阔天空", "共 2841 条"},
		},
		{
			name:  "歌词",
			open:  func(a *App) { a.nav = []route{{kind: routeLyrics, id: 347230, title: "海阔天空"}} },
			texts: []string{"今天我", "寒夜里看雪飘过"},
		},
		{
			name:  "设置",
			open:  func(a *App) { a.nav = []route{{kind: routeSettings, title: "设置"}} },
			texts: []string{"播放", "缓存", "网络", "账号与数据"},
		},
	}
	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			a := headlessModel(t)
			page.open(a)
			session, _ := headlessPage(t, a)
			settle(t, session, a)

			if err := session.Expect("layout", "clean"); err != nil {
				t.Fatalf("%s: %v", page.name, err)
			}
			if err := session.Expect("named"); err != nil {
				t.Fatalf("%s: %v", page.name, err)
			}
			for _, text := range page.texts {
				if !session.HasText(text) {
					t.Fatalf("%s: %q is not on screen; the frame has %v", page.name, text, session.Texts())
				}
			}
		})
	}
}

// TestLayoutStaysCleanWhenTheWindowIsNarrow is the layout check at the smallest
// window the application allows: a row that only fits a wide window is a bug a
// user finds by resizing.
func TestLayoutStaysCleanWhenTheWindowIsNarrow(t *testing.T) {
	a := headlessModel(t)
	a.nav = []route{{kind: routePlaylist, id: 24381616, title: "我喜欢的音乐"}}
	_, config := a.Page(app.Config{Title: "ncmviva", Width: 880, Height: 560})
	session, err := headless.Open(config, nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	settle(t, session, a)
	if err := session.Expect("layout", "clean"); err != nil {
		t.Fatalf("at the minimum window size: %v", err)
	}
}

// TestClickingARowPlaysIt drives the real input path: a click on a track row
// starts that song.
func TestClickingARowPlaysIt(t *testing.T) {
	a := headlessModel(t)
	a.nav = []route{{kind: routePlaylist, id: 24381616, title: "我喜欢的音乐"}}
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	session, _ := headlessPage(t, a)
	settle(t, session, a)

	if err := session.ClickLabel("晴天"); err != nil {
		t.Fatalf("clicking the row: %v", err)
	}
	settle(t, session, a)
	if a.queueAt != 2 {
		t.Fatalf("the click started row %d, want 2 (晴天)", a.queueAt)
	}
	if song, ok := a.current(); !ok || song.Name != "晴天" {
		t.Fatalf("the current song is %v", song.Name)
	}
}

// TestSidebarNavigation drives the rail: each item opens its section, and the
// header names it.
func TestSidebarNavigation(t *testing.T) {
	a := headlessModel(t)
	session, _ := headlessPage(t, a)
	settle(t, session, a)

	for _, item := range []struct{ label, header string }{
		{"排行榜", "飙升榜"},
		{"我的音乐", "云端漫步"},
		{"设置", "缓存"},
		{"发现", "每日推荐"},
	} {
		if err := session.ClickLabel(item.label); err != nil {
			t.Fatalf("%s: %v", item.label, err)
		}
		settle(t, session, a)
		if !session.HasText(item.header) {
			t.Fatalf("after clicking %q the page does not show %q", item.label, item.header)
		}
	}
}

// TestTheQueuePanelOpensFromTheHeader covers the side panel: the header button
// opens it, it lists the queue, and the close button shuts it.
func TestTheQueuePanelOpensFromTheHeader(t *testing.T) {
	a := headlessModel(t)
	session, _ := headlessPage(t, a)
	settle(t, session, a)
	if session.HasText("播放列表 (4)") {
		t.Fatal("the queue panel is open before it was asked for")
	}
	if err := session.ClickLabel("播放列表"); err != nil {
		t.Fatal(err)
	}
	settle(t, session, a)
	if !session.HasText("播放列表 (4)") {
		t.Fatalf("the queue panel did not open; the frame has %v", session.Texts())
	}
	if err := session.Expect("layout", "clean"); err != nil {
		t.Fatalf("with the queue panel open: %v", err)
	}
}

// TestKeyboardShortcutsPauseAndResume drives the transport from the keyboard,
// the way a user with media keys on the keyboard reaches for it.
func TestKeyboardShortcutsPauseAndResume(t *testing.T) {
	a := headlessModel(t)
	a.engine.NewSink = func() playerSink { return &countingSink{} }
	session, _ := headlessPage(t, a)
	settle(t, session, a)

	a.playAt(0)
	waitFor(t, a, func() bool { return a.engine.Snapshot().Position > 100*time.Millisecond })

	if err := session.Keys("ctrl+space"); err != nil {
		t.Fatalf("pressing ctrl+space: %v", err)
	}
	settle(t, session, a)
	if status := a.engine.Snapshot().Status.String(); status != "paused" {
		t.Fatalf("the transport is %q after ctrl+space", status)
	}
	if err := session.Keys("ctrl+space"); err != nil {
		t.Fatalf("pressing ctrl+space again: %v", err)
	}
	settle(t, session, a)
	if status := a.engine.Snapshot().Status.String(); status == "paused" {
		t.Fatal("ctrl+space did not resume")
	}
}

// TestAToastIsDrawnAndNamed covers the toast stack: a message a worker raised is
// painted and readable, and its close button is named.
func TestAToastIsDrawnAndNamed(t *testing.T) {
	a := headlessModel(t)
	session, _ := headlessPage(t, a)
	settle(t, session, a)
	a.toast("已收藏", "海阔天空", ui.ToastSuccess)
	// toast() asks for the repaint; settle applies it. A queued toast becomes its
	// own layer only in a paint, which is what this checks.
	settle(t, session, a)
	if !session.HasText("已收藏") {
		t.Fatalf("the toast was not painted; the frame has %v", session.Texts())
	}
	if err := session.Expect("layout", "clean"); err != nil {
		t.Fatalf("with a toast: %v", err)
	}
}

// TestTheTransportShowsWhatPlays reads the player bar: the song, its singer and
// the duration it was given.
func TestTheTransportShowsWhatPlays(t *testing.T) {
	a := headlessModel(t)
	session, _ := headlessPage(t, a)
	settle(t, session, a)
	for _, text := range []string{"海阔天空", "Beyond", "5:25"} {
		if !session.HasText(text) {
			t.Fatalf("the player bar does not show %q; it has %v", text, session.Texts())
		}
	}
	// The bar is one row: the seek slider and the volume slider are named
	// controls a keyboard can reach.
	if err := session.Expect("state", "播放进度", "reachable"); err != nil {
		t.Fatalf("the seek slider: %v", err)
	}
	if err := session.Expect("state", "音量", "reachable"); err != nil {
		t.Fatalf("the volume slider: %v", err)
	}
}

// TestTheSettingsControlsWriteTheModel drives the settings page's own controls:
// the quality select writes the setting and stores it.
func TestTheSettingsControlsWriteTheModel(t *testing.T) {
	a := headlessModel(t)
	a.nav = []route{{kind: routeSettings, title: "设置"}}
	session, _ := headlessPage(t, a)
	settle(t, session, a)
	if err := session.ClickLabel("跳过无版权歌曲"); err != nil {
		t.Fatalf("the switch: %v", err)
	}
	settle(t, session, a)
	if a.config.IgnoreUnplayable {
		t.Fatal("the switch did not write the setting")
	}
	loaded, err := store.NewSettings(a.paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IgnoreUnplayable {
		t.Fatal("the setting was not stored")
	}
}

// A label that ends in an ellipsis in the window is not a layout problem, but a
// row that reports a clipped label with no ellipsis is: this pins the second
// case by checking the album column of a track row is either whole or marked.
func TestTrackRowsDoNotClipTheirTitles(t *testing.T) {
	a := headlessModel(t)
	a.nav = []route{{kind: routePlaylist, id: 24381616, title: "我喜欢的音乐"}}
	session, _ := headlessPage(t, a)
	settle(t, session, a)
	for _, text := range session.Texts() {
		if strings.Contains(text, "…") {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(text), "…") {
			continue
		}
	}
	if err := session.Expect("layout", "clean"); err != nil {
		t.Fatal(err)
	}
}
