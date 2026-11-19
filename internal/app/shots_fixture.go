//go:build vivashots

// This file is a development fixture: it builds the application against fixed
// data so its pages can be rendered without a network or an account. It is
// behind a build tag because nothing here ships.

package app

import (
	"fmt"
	"net/http"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/store"
)

// NewWithFixture builds an application whose model already holds a discover
// page, a playlist and a queue, with no request made.
func NewWithFixture(paths store.Paths) *App {
	a, err := New(paths)
	if err != nil {
		panic(err)
	}
	a.client.SetTransport(shotsTransport{})
	a.config.Theme = "light"
	a.user = ncm.UserInfo{UserID: 1, Nickname: "云端漫步", VIPType: 11, Follows: 42, Followeds: 128, Signature: "听歌是一件很私人的事"}

	songs := fixtureSongs()
	playlist := ncm.Playlist{
		ID:          24381616,
		Name:        "我喜欢的音乐",
		CoverImgURL: "",
		TrackCount:  len(songs),
		PlayCount:   284913,
		Description: "收藏的歌，从 2013 年开始。",
		Creator:     ncm.UserInfo{Nickname: "云端漫步"},
		Tracks:      songs,
		Subscribed:  true,
	}
	a.data.init()
	a.data.discover = discoverData{
		loading:   loading{loaded: true},
		playlists: fixturePlaylists(),
		newAlbums: fixtureAlbums(),
		banners: []ncm.Banner{
			{ImageURL: "", Title: "新歌首发 · 本周上新", TargetType: 1},
			{ImageURL: "", Title: "热歌推荐 · 今天大家都在听", TargetType: 1},
			{ImageURL: "", Title: "新碟首发 · 华语新专辑", TargetType: 10},
		},
	}
	a.data.toplist = toplistData{loading: loading{loaded: true}, charts: fixtureCharts()}
	a.data.playlists[playlist.ID] = &playlistData{loading: loading{loaded: true}, playlist: playlist, counts: ncm.PlaylistCounts{PlayCount: 284913, SubscribedCount: 1240, CommentCount: 88}}
	a.data.mine = mineData{loading: loading{loaded: true}, playlists: fixturePlaylists(), liked: songs, daily: songs}
	for _, song := range songs {
		a.likedSet[song.ID] = true
	}
	a.data.lyrics[songs[0].ID] = &lyricData{
		loading: loading{loaded: true},
		song:    songs[0],
		lyric:   fixtureLyric(),
	}
	a.queue = songs
	a.queueAt = 0
	a.nav = []route{{kind: routeDiscover, title: "发现"}}
	return a
}

// SetThemeForShots fixes the appearance for a screenshot pair. It applies it the
// way the settings page does, so a screenshot shows what a user would see.
func (a *App) SetThemeForShots(theme string) {
	a.config.Theme = theme
	a.applyTheme()
}

// SyncForShots runs the window-thread closures a worker published.
func (a *App) SyncForShots() { a.sync() }

// ShowForShots opens one page by name.
func (a *App) ShowForShots(page string) {
	switch page {
	case "discover":
		a.nav = []route{{kind: routeDiscover, title: "发现"}}
	case "toplist":
		a.nav = []route{{kind: routeToplist, title: "排行榜"}}
	case "playlist":
		a.nav = []route{
			{kind: routeDiscover, title: "发现"},
			{kind: routePlaylist, id: 24381616, title: "我喜欢的音乐"},
		}
	case "settings":
		a.nav = []route{{kind: routeSettings, title: "设置"}}
	case "lyrics":
		song := a.queue[0]
		a.nav = []route{{kind: routeLyrics, id: song.ID, title: song.Name}}
	case "search":
		a.search.field = "海阔天空"
		a.search.kind = ncm.SearchSongs
		songs := fixtureSongs()
		a.data.searches[searchKey("海阔天空", ncm.SearchSongs, 0)] = &searchData{
			loading: loading{loaded: true},
			result:  ncm.SearchResult{Kind: ncm.SearchSongs, Songs: songs, Total: 2841, HasMore: true},
		}
		a.nav = []route{{kind: routeSearch, title: "搜索"}}
	case "queue":
		a.queuePanel = true
		a.nav = []route{{kind: routeDiscover, title: "发现"}}
	case "login":
		a.login.open = true
		a.login.qr = fixtureQR()
		a.login.state = "已扫码，请在手机上确认"
	}
}

// fixtureQR is a real QR code for a placeholder login URL, so a screenshot
// shows the login panel as it looks with a code the server issued.
func fixtureQR() []qrModule {
	modules, err := qrModules("https://music.163.com/login?codekey=fixture-unikey-for-screenshots")
	if err != nil {
		return nil
	}
	return modules
}

// shotsTransport refuses every request: the fixture makes none, and a mistake
// that does should fail at once rather than wait.
type shotsTransport struct{}

func (shotsTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("shots: the network is off")
}

// fixtureSongs is a short queue with the states a row can be in.
func fixtureSongs() []ncm.Song {
	names := []struct {
		name   string
		singer string
		album  string
		length time.Duration
		fee    int
	}{
		{"海阔天空", "Beyond", "乐与怒", 5*time.Minute + 25*time.Second, 1},
		{"光年之外", "G.E.M. 邓紫棋", "光年之外", 3*time.Minute + 55*time.Second, 0},
		{"晴天", "周杰伦", "叶惠美", 4*time.Minute + 29*time.Second, 1},
		{"起风了", "买辣椒也用券", "起风了", 5*time.Minute + 25*time.Second, 0},
		{"句号", "G.E.M. 邓紫棋", "启示录", 4*time.Minute + 12*time.Second, 8},
		{"孤勇者", "陈奕迅", "孤勇者", 4*time.Minute + 16*time.Second, 0},
		{"这世界那么多人", "莫文蔚", "这世界那么多人", 4*time.Minute + 45*time.Second, 1},
		{"以父之名", "周杰伦", "叶惠美", 5*time.Minute + 42*time.Second, 1},
	}
	songs := make([]ncm.Song, 0, len(names))
	for index, entry := range names {
		song := ncm.Song{
			ID:       int64(347230 + index),
			Name:     entry.name,
			Artists:  []ncm.Artist{{ID: int64(100 + index), Name: entry.singer}},
			Album:    ncm.Album{ID: int64(200 + index), Name: entry.album},
			Duration: entry.length,
			Fee:      entry.fee,
		}
		song.Privilege = ncm.Privilege{Status: 0, MaxBr: 320000}
		songs = append(songs, song)
	}
	// The last one has lost its rights: the row shows the grey state and the
	// cache badge still offers it.
	songs[len(songs)-1].Privilege = ncm.Privilege{Status: -1}
	return songs
}

func fixturePlaylists() []ncm.Playlist {
	names := []struct {
		name  string
		count int
		plays int64
	}{
		{"华语流行 · 深夜电台", 50, 1289000},
		{"摇滚不死 · 经典现场", 42, 863000},
		{"轻音乐 · 专注工作", 60, 2310000},
		{"粤语金曲三十年", 38, 512000},
		{"民谣里的城市", 45, 341000},
		{"电子 · 深夜驾驶", 32, 780000},
	}
	out := make([]ncm.Playlist, 0, len(names))
	for i, entry := range names {
		out = append(out, ncm.Playlist{ID: int64(1000 + i), Name: entry.name, TrackCount: entry.count, PlayCount: entry.plays})
	}
	return out
}

func fixtureAlbums() []ncm.Album {
	names := []struct {
		name   string
		artist string
	}{
		{"叶惠美", "周杰伦"},
		{"乐与怒", "Beyond"},
		{"启示录", "G.E.M. 邓紫棋"},
		{"孤勇者", "陈奕迅"},
		{"这世界那么多人", "莫文蔚"},
	}
	out := make([]ncm.Album, 0, len(names))
	for i, entry := range names {
		out = append(out, ncm.Album{ID: int64(3000 + i), Name: entry.name, Artist: entry.artist, TrackCount: 10})
	}
	return out
}

func fixtureCharts() []ncm.TopList {
	names := []struct {
		name  string
		count int
	}{
		{"飙升榜", 100}, {"新歌榜", 100}, {"原创榜", 100}, {"热歌榜", 200},
		{"古典音乐榜", 100}, {"ACG 音乐榜", 100}, {"韩语榜", 100}, {"国电榜", 100},
	}
	out := make([]ncm.TopList, 0, len(names))
	for i, entry := range names {
		out = append(out, ncm.TopList{ID: int64(19723756 + i), Name: entry.name, TrackCount: entry.count, UpdateFrequency: "每周更新", PlayCount: int64(1000000 * (i + 1))})
	}
	return out
}

func fixtureLyric() ncm.Lyric {
	lines := []struct {
		at    time.Duration
		text  string
		trans string
	}{
		{2 * time.Second, "今天我 寒夜里看雪飘过", "Today I watch the snow drift by in the cold night"},
		{9 * time.Second, "怀着冷却了的心窝漂远方", "Carrying a cooled heart, drifting far away"},
		{16 * time.Second, "风雨里追赶 雾里分不清影踪", "Chasing through wind and rain, lost in the mist"},
		{23 * time.Second, "天空海阔你与我", "The sky is wide, the sea is vast, you and I"},
		{30 * time.Second, "可会变（谁没在变）", "Will we change? (Who hasn't changed?)"},
		{37 * time.Second, "多少次 迎着冷眼与嘲笑", "How many times, facing cold looks and laughter"},
		{44 * time.Second, "从没有放弃过心中的理想", "Never giving up the ideal in my heart"},
	}
	lyric := ncm.Lyric{}
	for _, line := range lines {
		lyric.Lines = append(lyric.Lines, ncm.LyricLine{At: line.at, Text: line.text, Translation: line.trans})
	}
	return lyric
}
