package app

import (
	"context"
	"strconv"
	"time"

	"cetone/internal/ncm"
)

// pageData is what the pages loaded, one struct per kind of page. It lives in
// the model; a worker fills it through the work queue, never directly.
type pageData struct {
	discover discoverData
	toplist  toplistData
	mine     mineData

	playlists map[int64]*playlistData
	albums    map[int64]*albumData
	artists   map[int64]*artistData
	searches  map[string]*searchData
	lyrics    map[int64]*lyricData
}

func (d *pageData) init() {
	if d.playlists == nil {
		d.playlists = make(map[int64]*playlistData)
	}
	if d.albums == nil {
		d.albums = make(map[int64]*albumData)
	}
	if d.artists == nil {
		d.artists = make(map[int64]*artistData)
	}
	if d.searches == nil {
		d.searches = make(map[string]*searchData)
	}
	if d.lyrics == nil {
		d.lyrics = make(map[int64]*lyricData)
	}
}

// loading is the shape every loaded page shares: whether a request is in
// flight and what failed.
type loading struct {
	busy   bool
	err    string
	loaded bool
}

func (l *loading) begin() { l.busy, l.err = true, "" }

func (l *loading) fail(err error) {
	l.busy = false
	l.err = errText(err)
}

// discoverData is the 发现 page: the carousel, the daily playlists, the new
// albums and the personal radio.
type discoverData struct {
	loading
	banners     []ncm.Banner
	playlists   []ncm.Playlist
	newAlbums   []ncm.Album
	freshSongs  []ncm.Song
	bannerIndex int
}

// toplistData is the 排行榜 page: the chart index and the first chart's songs.
type toplistData struct {
	loading
	charts []ncm.TopList
}

// mineData is 我的音乐: the account's playlists and the entries that open its
// special lists.
type mineData struct {
	loading
	playlists []ncm.Playlist
	liked     []ncm.Song
	daily     []ncm.Song
	cloud     []ncm.Song
	loggedOut bool
}

// playlistData is one playlist or album page.
type playlistData struct {
	loading
	playlist ncm.Playlist
	album    ncm.Album
	counts   ncm.PlaylistCounts
}

// albumData is an album page.
type albumData struct {
	loading
	album ncm.Album
}

// artistData is an artist page.
type artistData struct {
	loading
	detail ncm.ArtistDetail
}

// searchData is one keyword and kind of search.
type searchData struct {
	loading
	result ncm.SearchResult
	offset int
	more   bool
}

// lyricData is a song's words, ready to scroll.
type lyricData struct {
	loading
	song  ncm.Song
	lyric ncm.Lyric
}

// searchState is what the search page keeps between paints.
type searchState struct {
	field   string
	kind    ncm.SearchKind
	history []string
}

// loginState is the login panel: a QR code and the poll's answer, or the
// phone-number alternative with a texted code.
type loginState struct {
	open    bool
	busy    bool
	err     string
	mode    string // "qr" or "phone"
	qr      []qrModule
	unikey  string
	state   string
	phone   string
	captcha string
	country string
	// sentAt is when the code was requested, for the resend countdown.
	sentAt   time.Time
	method   string // "captcha" (a texted code) or "password"
	password string
}

// loginModeQR and loginModePhone are the panel's two tabs.
const (
	loginModeQR    = "qr"
	loginModePhone = "phone"
)

// load starts whatever the route needs, unless it is already loaded.
func (a *App) load(r route) {
	a.data.init()
	switch r.kind {
	case routeDiscover:
		if a.data.discover.loaded || a.data.discover.busy {
			return
		}
		a.loadDiscover()
	case routeToplist:
		if a.data.toplist.loaded || a.data.toplist.busy {
			return
		}
		a.loadToplists()
	case routePlaylist:
		if data := a.data.playlists[r.id]; data != nil && (data.loaded || data.busy) {
			return
		}
		a.loadPlaylist(r.id)
	case routeAlbum:
		if data := a.data.albums[r.id]; data != nil && (data.loaded || data.busy) {
			return
		}
		a.loadAlbum(r.id)
	case routeArtist:
		if data := a.data.artists[r.id]; data != nil && (data.loaded || data.busy) {
			return
		}
		a.loadArtist(r.id)
	case routeMine:
		if a.data.mine.loaded || a.data.mine.busy {
			return
		}
		a.loadMine()
	case routeSearch:
		a.runSearch(false)
	case routeLyrics:
		a.loadLyric(r.id)
	}
}

// loadDiscover fetches the four blocks of the discover page.
func (a *App) loadDiscover() {
	a.data.discover.begin()
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		banners, bannersErr := a.client.Banners(ctx)
		// A signed-out visitor gets the public charts' playlists: the
		// personalised recommendation answers 301 without a session.
		var playlists []ncm.Playlist
		var playlistsErr error
		if a.client.LoggedIn() {
			playlists, playlistsErr = a.client.RecommendPlaylists(ctx)
			if playlistsErr != nil {
				playlists, playlistsErr = a.client.HighQualityPlaylists(ctx, "", 12)
			}
		} else {
			playlists, playlistsErr = a.client.HighQualityPlaylists(ctx, "", 12)
		}
		albums, albumsErr := a.client.NewAlbums(ctx, "ALL", 0, 12)
		a.work.push(func() {
			a.data.discover.banners = banners
			a.data.discover.playlists = playlists
			a.data.discover.newAlbums = albums
			// One block failing should not empty the page: report the first
			// error and show what did arrive.
			for _, err := range []error{playlistsErr, bannersErr, albumsErr} {
				if err != nil {
					a.data.discover.err = errText(err)
					break
				}
			}
			a.data.discover.busy = false
			a.data.discover.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadToplists fetches the chart index.
func (a *App) loadToplists() {
	a.data.toplist.begin()
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		charts, err := a.client.ToplistIndex(ctx)
		a.work.push(func() {
			if err != nil {
				a.data.toplist.fail(err)
				return
			}
			a.data.toplist.charts = charts
			a.data.toplist.busy = false
			a.data.toplist.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadPlaylist fetches one playlist and its social block.
func (a *App) loadPlaylist(id int64) {
	a.data.init()
	data := &playlistData{}
	a.data.playlists[id] = data
	data.begin()
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		playlist, err := a.client.PlaylistDetail(ctx, id)
		var counts ncm.PlaylistCounts
		if err == nil {
			counts, _ = a.client.PlaylistDynamic(ctx, id)
		}
		a.work.push(func() {
			if err != nil {
				data.fail(err)
				return
			}
			data.playlist = playlist
			data.counts = counts
			data.busy = false
			data.loaded = true
			if a.user.UserID != 0 {
				a.loadLikedIDs()
			}
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadAlbum fetches an album with its songs.
func (a *App) loadAlbum(id int64) {
	a.data.init()
	data := &albumData{}
	a.data.albums[id] = data
	data.begin()
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		album, err := a.client.AlbumDetail(ctx, id)
		a.work.push(func() {
			if err != nil {
				data.fail(err)
				return
			}
			data.album = album
			data.busy = false
			data.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadArtist fetches an artist's hot songs.
func (a *App) loadArtist(id int64) {
	a.data.init()
	data := &artistData{}
	a.data.artists[id] = data
	data.begin()
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		detail, err := a.client.Artist(ctx, id)
		a.work.push(func() {
			if err != nil {
				data.fail(err)
				return
			}
			data.detail = detail
			data.busy = false
			data.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadMine fetches the account's playlists and its three special lists.
func (a *App) loadMine() {
	a.data.mine.begin()
	a.data.mine.loggedOut = a.user.UserID == 0
	if a.user.UserID == 0 {
		a.data.mine.busy = false
		a.data.mine.loaded = true
		return
	}
	uid := a.user.UserID
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		playlists, err := a.client.UserPlaylists(ctx, uid, 0, 200)
		var daily []ncm.Song
		var cloud []ncm.Song
		var liked []ncm.Song
		if err == nil {
			daily, _ = a.client.RecommendSongs(ctx)
			cloud, _ = a.client.CloudDisk(ctx)
			if ids, idErr := a.client.LikedSongIDs(ctx, uid); idErr == nil && len(ids) > 0 {
				if len(ids) > 500 {
					ids = ids[:500]
				}
				liked, _ = a.client.SongsDetail(ctx, ids)
			}
		}
		a.work.push(func() {
			if err != nil {
				a.data.mine.fail(err)
				return
			}
			a.data.mine.playlists = playlists
			a.data.mine.daily = daily
			a.data.mine.cloud = cloud
			a.data.mine.liked = liked
			for _, song := range liked {
				a.likedSet[song.ID] = true
			}
			a.data.mine.busy = false
			a.data.mine.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadLikedIDs reads which songs are in 我喜欢的音乐, for the hearts in the
// lists. It is cheap and refreshed after a like.
func (a *App) loadLikedIDs() {
	if a.user.UserID == 0 {
		return
	}
	uid := a.user.UserID
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ids, err := a.client.LikedSongIDs(ctx, uid)
		if err != nil {
			return nil
		}
		a.work.push(func() {
			a.likedSet = make(map[int64]bool, len(ids))
			for _, id := range ids {
				a.likedSet[id] = true
			}
		})
		a.invalidate()
		return nil
	}, nil)
}

// searchKey names one search in the cache.
func searchKey(keyword string, kind ncm.SearchKind, offset int) string {
	return strconv.Itoa(int(kind)) + "|" + strconv.Itoa(offset) + "|" + keyword
}

// runSearch asks for one page of results. When more is true it appends the next
// page into the active search entry rather than writing to an unshown cache key.
func (a *App) runSearch(more bool) {
	keyword := a.search.field
	if keyword == "" {
		return
	}
	a.data.init()
	kind := a.search.kind
	key := searchKey(keyword, kind, 0)
	data := a.data.searches[key]
	offset := 0
	if more {
		if data == nil || data.busy || !data.more {
			return
		}
		offset = data.offset + 50
		data.busy = true
	} else {
		if data != nil && (data.busy || data.loaded) {
			return
		}
		data = &searchData{}
		a.data.searches[key] = data
		data.begin()
	}
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		result, err := a.client.Search(ctx, keyword, kind, offset, 50)
		a.work.push(func() {
			history := []string{keyword}
			for _, old := range a.config.SearchHistory {
				if old != keyword && len(history) < 12 {
					history = append(history, old)
				}
			}
			if err != nil {
				if len(history) != len(a.config.SearchHistory) || (len(history) > 0 && a.config.SearchHistory[0] != keyword) {
					a.config.SearchHistory = history
					_ = a.settings.Save(a.config)
				}
				if !more {
					data.fail(err)
				} else {
					data.busy = false
				}
				return
			}
			a.config.SearchHistory = history
			_ = a.settings.Save(a.config)
			if more {
				data.result.Songs = append(data.result.Songs, result.Songs...)
				data.result.Artists = append(data.result.Artists, result.Artists...)
				data.result.Albums = append(data.result.Albums, result.Albums...)
				data.result.Playlists = append(data.result.Playlists, result.Playlists...)
				data.offset = offset
			} else {
				data.result = result
				data.offset = 0
			}
			data.more = result.HasMore
			data.busy = false
			data.loaded = true
		})
		a.invalidate()
		return nil
	}, nil)
}

// loadLyric fetches one song's words, cache first.
func (a *App) loadLyric(songID int64) {
	a.data.init()
	if data := a.data.lyrics[songID]; data != nil && (data.loaded || data.busy) {
		return
	}
	song := a.songByID(songID)
	data := &lyricData{song: song}
	a.data.lyrics[songID] = data
	data.begin()
	a.background(func() error {
		if raw, trans, ok := a.lyrics.Load(song); ok {
			a.work.push(func() {
				data.lyric = ncm.Lyric{Raw: raw, Trans: trans, Lines: ncm.ParseLRC(raw, trans)}
				data.busy = false
				data.loaded = true
			})
			a.invalidate()
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lyric, err := a.client.Lyric(ctx, songID)
		a.work.push(func() {
			if err != nil {
				data.fail(err)
				return
			}
			data.lyric = lyric
			data.busy = false
			data.loaded = true
			if lyric.Raw != "" {
				_ = a.lyrics.Save(song, lyric.Raw, lyric.Trans)
			}
		})
		a.invalidate()
		return nil
	}, nil)
}

// songByID finds a song in whatever the application already holds: the queue,
// the loaded lists, or a bare id as a last resort.
func (a *App) songByID(id int64) ncm.Song {
	for _, song := range a.queue {
		if song.ID == id {
			return song
		}
	}
	for _, playlist := range a.data.playlists {
		for _, song := range playlist.playlist.Tracks {
			if song.ID == id {
				return song
			}
		}
	}
	for _, album := range a.data.albums {
		for _, song := range album.album.Songs {
			if song.ID == id {
				return song
			}
		}
	}
	for _, search := range a.data.searches {
		for _, song := range search.result.Songs {
			if song.ID == id {
				return song
			}
		}
	}
	for _, song := range a.data.mine.liked {
		if song.ID == id {
			return song
		}
	}
	for _, song := range a.data.mine.daily {
		if song.ID == id {
			return song
		}
	}
	return ncm.Song{ID: id, Name: strconv.FormatInt(id, 10)}
}
