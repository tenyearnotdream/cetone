package ncm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// deviceName is what a login reports to the API. It shows in the account's
// device list.
const deviceName = "cetone"

// QRLoginTicket is one poll of a QR login: the unikey to poll with and the URL
// to draw as a code.
type QRLoginTicket struct {
	QRURL  string
	Unikey string
}

// QR state codes the login endpoint answers with.
const (
	QRWaiting  = 801 // scanned? no: waiting to be scanned
	QRScanned  = 802 // scanned, waiting for the phone's confirmation
	QRExpired  = 800
	QRSuccess  = 803
	QRNotFound = 800
)

// LoginQR creates a login QR code. The QRURL is what a QR encoder draws; the
// Unikey is passed to LoginQRCheck until it answers QRSuccess or QRExpired.
func (c *Client) LoginQR(ctx context.Context) (QRLoginTicket, error) {
	var out struct {
		Unikey string `json:"unikey"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/login/qrcode/unikey", map[string]string{"type": "1"}, &out); err != nil {
		return QRLoginTicket{}, err
	}
	if out.Unikey == "" {
		return QRLoginTicket{}, fmt.Errorf("ncm: login QR: no unikey in the answer")
	}
	return QRLoginTicket{QRURL: "https://music.163.com/login?codekey=" + out.Unikey, Unikey: out.Unikey}, nil
}

// LoginQRCheck polls a QR login. On QRSuccess the session cookies are set and
// the caller should call LoginStatus for the profile.
func (c *Client) LoginQRCheck(ctx context.Context, unikey string) (code int, msg string, err error) {
	var out struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := c.callQR(ctx, "/weapi/login/qrcode/client/login", map[string]string{"key": unikey, "type": "1"}, &out); err != nil {
		return 0, "", err
	}
	if out.Code == QRSuccess || out.Code == QRScanned {
		c.markLoggedIn()
	}
	return out.Code, out.Message, nil
}

// callQR is call without the code==200 check: the QR endpoints answer 800,
// 801, 802 and 803, all of which are an answer rather than an error.
func (c *Client) callQR(ctx context.Context, path string, params map[string]string, out any) error {
	data, err := c.post(ctx, modeWeapi, path, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ncm: %s: decode: %w", path, err)
	}
	return nil
}

// CaptchaSent asks the server to text a login code to a phone number.
func (c *Client) CaptchaSent(ctx context.Context, countryCode, phone string) error {
	if countryCode == "" {
		countryCode = "86"
	}
	params := map[string]string{
		"cellphone": phone,
		"ctcode":    countryCode,
		"secrete":   "music_middleuser_pclogin",
	}
	return c.call(ctx, modeWeapi, "/weapi/sms/captcha/sent", params, nil)
}

// LoginCellphone signs in with a texted code.
func (c *Client) LoginCellphone(ctx context.Context, countryCode, phone, captcha string) error {
	if countryCode == "" {
		countryCode = "86"
	}
	params := map[string]string{
		"phone":       phone,
		"countrycode": countryCode,
		"captcha":     captcha,
		"type":        "1",
		"https":       "true",
		"remember":    "true",
	}
	if err := c.call(ctx, modeWeapi, "/weapi/w/login/cellphone", params, nil); err != nil {
		return err
	}
	c.markLoggedIn()
	return nil
}

func (c *Client) markLoggedIn() {
	c.muLogin.Lock()
	c.login = true
	c.muLogin.Unlock()
}

// LoginStatus reads the signed-in profile. It returns ErrNotLoggedIn when the
// session has no account.
func (c *Client) LoginStatus(ctx context.Context) (UserInfo, error) {
	var out struct {
		Code    int `json:"code"`
		Account *struct {
			ID int64 `json:"id"`
		} `json:"account"`
		Profile *rawUser `json:"profile"`
	}
	data, err := c.post(ctx, modeWeapi, "/api/nuser/account/get", nil)
	if err != nil {
		return UserInfo{}, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return UserInfo{}, fmt.Errorf("ncm: login status: decode: %w", err)
	}
	if out.Profile == nil || out.Profile.UserID == 0 {
		c.muLogin.Lock()
		c.login = false
		c.muLogin.Unlock()
		return UserInfo{}, ErrNotLoggedIn
	}
	user := out.Profile.user()
	if user.UserID == 0 && out.Account != nil {
		user.UserID = out.Account.ID
	}
	c.muLogin.Lock()
	c.login = true
	c.muLogin.Unlock()
	return user, nil
}

// ErrNotLoggedIn reports a request that needs a session without one.
var ErrNotLoggedIn = fmt.Errorf("ncm: not signed in")

// Logout clears the session on the server and locally.
func (c *Client) Logout(ctx context.Context) {
	_, _ = c.post(ctx, modeWeapi, "/weapi/logout", nil)
	c.cookies.clearSession()
	c.mu.Lock()
	c.csrf = ""
	c.mu.Unlock()
	c.muLogin.Lock()
	c.login = false
	c.muLogin.Unlock()
}

// UserPlaylists lists the playlists a user owns or collects.
func (c *Client) UserPlaylists(ctx context.Context, uid int64, offset, limit int) ([]Playlist, error) {
	if uid == 0 {
		return nil, ErrNotLoggedIn
	}
	params := map[string]string{
		"uid":    idString(uid),
		"offset": uintString(offset),
		"limit":  uintString(limit),
	}
	var out struct {
		Playlist []rawPlaylist `json:"playlist"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/user/playlist", params, &out); err != nil {
		return nil, err
	}
	list := make([]Playlist, 0, len(out.Playlist))
	for _, p := range out.Playlist {
		list = append(list, p.playlist())
	}
	return list, nil
}

// LikedSongIDs lists the ids of the songs in 我喜欢的音乐.
func (c *Client) LikedSongIDs(ctx context.Context, uid int64) ([]int64, error) {
	if uid == 0 {
		return nil, ErrNotLoggedIn
	}
	var out struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/song/like/get", map[string]string{"uid": idString(uid)}, &out); err != nil {
		return nil, err
	}
	return out.IDs, nil
}

// PlaylistDetail fetches one playlist, filling its tracks. A playlist over a
// thousand songs comes back as ids only, so the tracks are fetched in batches.
func (c *Client) PlaylistDetail(ctx context.Context, id int64) (Playlist, error) {
	var out struct {
		Playlist   rawPlaylist    `json:"playlist"`
		Privileges []rawPrivilege `json:"privileges"`
	}
	params := map[string]string{"id": idString(id), "n": "1000", "s": "8"}
	if err := c.call(ctx, modeEapi, "/api/v6/playlist/detail", params, &out); err != nil {
		return Playlist{}, err
	}
	detail := out.Playlist.playlist()
	if len(detail.Tracks) == 0 && len(detail.TrackIDs) > 0 {
		songs, err := c.SongsDetail(ctx, detail.TrackIDs)
		if err != nil {
			return Playlist{}, err
		}
		detail.Tracks = songs
	}
	if len(out.Privileges) > 0 {
		byID := make(map[int64]Privilege, len(out.Privileges))
		for i := range out.Privileges {
			p := out.Privileges[i].privilege()
			byID[p.ID] = p
		}
		for i := range detail.Tracks {
			if p, ok := byID[detail.Tracks[i].ID]; ok {
				detail.Tracks[i].Privilege = p
			}
		}
	}
	return detail, nil
}

// PlaylistCounts is the social block under a playlist header.
type PlaylistCounts struct {
	CommentCount    int64
	ShareCount      int64
	SubscribedCount int64
	PlayCount       int64
}

// PlaylistDynamic reads a playlist's play and comment counts.
func (c *Client) PlaylistDynamic(ctx context.Context, id int64) (PlaylistCounts, error) {
	var out struct {
		CommentCount    int64 `json:"commentCount"`
		ShareCount      int64 `json:"shareCount"`
		SubscribedCount int64 `json:"subscribedCount"`
		PlayCount       int64 `json:"playCount"`
	}
	err := c.call(ctx, modeWeapi, "/weapi/playlist/detail/dynamic", map[string]string{"id": idString(id)}, &out)
	if err != nil {
		return PlaylistCounts{}, err
	}
	return PlaylistCounts{CommentCount: out.CommentCount, ShareCount: out.ShareCount, SubscribedCount: out.SubscribedCount, PlayCount: out.PlayCount}, nil
}

// AlbumDetail fetches one album with its songs.
func (c *Client) AlbumDetail(ctx context.Context, id int64) (Album, error) {
	var out struct {
		Album *rawAlbum `json:"album"`
		Songs []rawSong `json:"songs"`
	}
	if err := c.call(ctx, modeWeapi, fmt.Sprintf("/weapi/v1/album/%d", id), nil, &out); err != nil {
		return Album{}, err
	}
	if out.Album == nil {
		return Album{}, fmt.Errorf("ncm: album %d: empty answer", id)
	}
	album := out.Album.album()
	for _, s := range out.Songs {
		album.Songs = append(album.Songs, s.song())
	}
	album.TrackCount = len(album.Songs)
	return album, nil
}

// AlbumSublist lists the albums the account has collected.
func (c *Client) AlbumSublist(ctx context.Context, offset, limit int) ([]Album, error) {
	params := map[string]string{"offset": uintString(offset), "limit": uintString(limit), "total": "true"}
	var out struct {
		Albums []rawAlbum `json:"albums"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/album/sublist", params, &out); err != nil {
		return nil, err
	}
	albums := make([]Album, 0, len(out.Albums))
	for i := range out.Albums {
		albums = append(albums, out.Albums[i].album())
	}
	return albums, nil
}

// NewAlbums lists the newest releases. area is ALL, ZH, EA, KR or JP.
func (c *Client) NewAlbums(ctx context.Context, area string, offset, limit int) ([]Album, error) {
	if area == "" {
		area = "ALL"
	}
	params := map[string]string{
		"area":   area,
		"offset": uintString(offset),
		"limit":  uintString(limit),
		"total":  "true",
	}
	var out struct {
		Albums []rawAlbum `json:"albums"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/album/new", params, &out); err != nil {
		return nil, err
	}
	albums := make([]Album, 0, len(out.Albums))
	for i := range out.Albums {
		albums = append(albums, out.Albums[i].album())
	}
	return albums, nil
}

// SongsDetail fetches full song records for a batch of ids, in chunks the
// endpoint accepts.
func (c *Client) SongsDetail(ctx context.Context, ids []int64) ([]Song, error) {
	const batch = 500
	out := make([]Song, 0, len(ids))
	for start := 0; start < len(ids); start += batch {
		end := min(start+batch, len(ids))
		chunk := ids[start:end]
		detailed := make([]map[string]int64, len(chunk))
		for i, id := range chunk {
			detailed[i] = map[string]int64{"id": id}
		}
		cParam, err := jsonValue(detailed)
		if err != nil {
			return nil, err
		}
		var answer struct {
			Songs      []rawSong      `json:"songs"`
			Privileges []rawPrivilege `json:"privileges"`
		}
		if err := c.call(ctx, modeWeapi, "/weapi/v3/song/detail", map[string]string{"c": cParam}, &answer); err != nil {
			return nil, err
		}
		byID := make(map[int64]Privilege, len(answer.Privileges))
		for i := range answer.Privileges {
			p := answer.Privileges[i].privilege()
			byID[p.ID] = p
		}
		for _, raw := range answer.Songs {
			song := raw.song()
			if p, ok := byID[song.ID]; ok {
				song.Privilege = p
			}
			out = append(out, song)
		}
	}
	return out, nil
}

// SongURLs resolves playable URLs for a batch of songs at one quality.
func (c *Client) SongURLs(ctx context.Context, ids []int64, quality Quality) ([]SongURL, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	idJSON, err := jsonValue(ids)
	if err != nil {
		return nil, err
	}
	params := map[string]string{
		"ids":        idJSON,
		"level":      quality.Level(),
		"encodeType": quality.EncodeType(),
	}
	var out struct {
		Data []struct {
			ID         int64  `json:"id"`
			URL        string `json:"url"`
			Br         int    `json:"br"`
			Size       int64  `json:"size"`
			MD5        string `json:"md5"`
			Type       string `json:"type"`
			Level      string `json:"level"`
			EncodeType string `json:"encodeType"`
			Code       int    `json:"code"`
			Fee        int    `json:"fee"`
			FreeTrial  *struct {
				Start int64 `json:"start"`
				End   int64 `json:"end"`
			} `json:"freeTrialInfo"`
		} `json:"data"`
	}
	if err := c.call(ctx, modeEapi, "/api/song/enhance/player/url/v1", params, &out); err != nil {
		return nil, err
	}
	urls := make([]SongURL, 0, len(out.Data))
	for _, d := range out.Data {
		u := SongURL{
			ID:         d.ID,
			URL:        d.URL,
			Bitrate:    d.Br,
			Size:       d.Size,
			MD5:        d.MD5,
			Type:       d.Type,
			Level:      d.Level,
			EncodeType: d.EncodeType,
			Code:       d.Code,
			Fee:        d.Fee,
			FreeTrial:  d.FreeTrial != nil,
		}
		if q, ok := QualityFromLevel(d.Level); ok {
			u.Quality = q
		} else {
			u.Quality = qualityFromBitrate(d.Br)
		}
		urls = append(urls, u)
	}
	return urls, nil
}

// RecommendPlaylists lists the playlists the daily recommendation picked.
func (c *Client) RecommendPlaylists(ctx context.Context) ([]Playlist, error) {
	var out struct {
		Recommend []rawPlaylist `json:"recommend"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/v1/discovery/recommend/resource", nil, &out); err != nil {
		return nil, err
	}
	list := make([]Playlist, 0, len(out.Recommend))
	for _, p := range out.Recommend {
		list = append(list, p.playlist())
	}
	return list, nil
}

// RecommendSongs is 每日推荐: the thirty songs picked for today.
func (c *Client) RecommendSongs(ctx context.Context) ([]Song, error) {
	var out struct {
		Data struct {
			DailySongs []rawSong `json:"dailySongs"`
		} `json:"data"`
	}
	if err := c.call(ctx, modeWeapi, "/api/v3/discovery/recommend/songs", map[string]string{"afresh": "false"}, &out); err != nil {
		return nil, err
	}
	songs := make([]Song, 0, len(out.Data.DailySongs))
	for _, s := range out.Data.DailySongs {
		songs = append(songs, s.song())
	}
	return songs, nil
}

// PersonalFM is the 私人FM radio queue.
func (c *Client) PersonalFM(ctx context.Context) ([]Song, error) {
	var out struct {
		Data []rawSong `json:"data"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/v1/radio/get", nil, &out); err != nil {
		return nil, err
	}
	songs := make([]Song, 0, len(out.Data))
	for _, s := range out.Data {
		songs = append(songs, s.song())
	}
	return songs, nil
}

// HeartbeatSongs is 心动模式: songs the recommender puts after songID when the
// queue is playlistID.
func (c *Client) HeartbeatSongs(ctx context.Context, songID, playlistID int64) ([]Song, error) {
	params := map[string]string{
		"songId":       idString(songID),
		"playlistId":   idString(playlistID),
		"startMusicId": idString(songID),
		"count":        "20",
	}
	var out struct {
		Data []struct {
			SongInfo rawSong `json:"songInfo"`
		} `json:"data"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/playmode/intelligence/list", params, &out); err != nil {
		return nil, err
	}
	songs := make([]Song, 0, len(out.Data))
	for _, s := range out.Data {
		songs = append(songs, s.SongInfo.song())
	}
	return songs, nil
}

// ToplistIndex lists the official charts.
func (c *Client) ToplistIndex(ctx context.Context) ([]TopList, error) {
	var out struct {
		List []struct {
			ID              int64  `json:"id"`
			Name            string `json:"name"`
			CoverImgURL     string `json:"coverImgUrl"`
			UpdateFrequency string `json:"updateFrequency"`
			TrackCount      int    `json:"trackCount"`
			PlayCount       int64  `json:"playCount"`
			Description     string `json:"description"`
		} `json:"list"`
	}
	if err := c.call(ctx, modeWeapi, "/api/toplist", nil, &out); err != nil {
		return nil, err
	}
	list := make([]TopList, 0, len(out.List))
	for _, t := range out.List {
		list = append(list, TopList{ID: t.ID, Name: t.Name, CoverImgURL: t.CoverImgURL, UpdateFrequency: t.UpdateFrequency, TrackCount: t.TrackCount, PlayCount: t.PlayCount, Description: t.Description})
	}
	return list, nil
}

// Search asks for one page of one kind of result.
func (c *Client) Search(ctx context.Context, keyword string, kind SearchKind, offset, limit int) (SearchResult, error) {
	if strings.TrimSpace(keyword) == "" {
		return SearchResult{Kind: kind}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	params := map[string]string{
		"s":      keyword,
		"type":   uintString(kind.apiType()),
		"offset": uintString(offset),
		"limit":  uintString(limit),
	}
	var raw struct {
		Result *struct {
			Songs         []rawSong     `json:"songs"`
			Artists       []rawArtist   `json:"artists"`
			Albums        []rawAlbum    `json:"albums"`
			Playlists     []rawPlaylist `json:"playlists"`
			SongCount     int           `json:"songCount"`
			ArtistCount   int           `json:"artistCount"`
			AlbumCount    int           `json:"albumCount"`
			PlaylistCount int           `json:"playlistCount"`
			HasMore       bool          `json:"hasMore"`
		} `json:"result"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/search/get", params, &raw); err != nil {
		return SearchResult{Kind: kind}, err
	}
	out := SearchResult{Kind: kind}
	if raw.Result == nil {
		return out, nil
	}
	for _, s := range raw.Result.Songs {
		out.Songs = append(out.Songs, s.song())
	}
	for _, a := range raw.Result.Artists {
		out.Artists = append(out.Artists, a.artist())
	}
	for i := range raw.Result.Albums {
		out.Albums = append(out.Albums, raw.Result.Albums[i].album())
	}
	for _, p := range raw.Result.Playlists {
		out.Playlists = append(out.Playlists, p.playlist())
	}
	switch kind {
	case SearchSongs:
		out.Total = raw.Result.SongCount
	case SearchArtists:
		out.Total = raw.Result.ArtistCount
	case SearchAlbums:
		out.Total = raw.Result.AlbumCount
	case SearchPlaylists:
		out.Total = raw.Result.PlaylistCount
	}
	out.HasMore = raw.Result.HasMore
	return out, nil
}

// ArtistDetail is an artist page: the profile and its hottest songs.
type ArtistDetail struct {
	Artist Artist
	Songs  []Song
	Albums []Album
}

// Artist fetches an artist's profile and hot songs.
func (c *Client) Artist(ctx context.Context, id int64) (ArtistDetail, error) {
	var out struct {
		Artist   *rawArtist `json:"artist"`
		HotSongs []rawSong  `json:"hotSongs"`
	}
	if err := c.call(ctx, modeWeapi, fmt.Sprintf("/weapi/v1/artist/%d", id), nil, &out); err != nil {
		return ArtistDetail{}, err
	}
	detail := ArtistDetail{}
	if out.Artist != nil {
		detail.Artist = out.Artist.artist()
	}
	for _, s := range out.HotSongs {
		detail.Songs = append(detail.Songs, s.song())
	}
	return detail, nil
}

// Lyric fetches a song's words, with translation when the server has one.
func (c *Client) Lyric(ctx context.Context, id int64) (Lyric, error) {
	params := map[string]string{
		"id": idString(id),
		"lv": "-1",
		"tv": "-1",
		"rv": "-1",
	}
	var out struct {
		Lrc *struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLyric *struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		RomLyric *struct {
			Lyric string `json:"lyric"`
		} `json:"romalrc"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/song/lyric", params, &out); err != nil {
		return Lyric{}, err
	}
	lyric := Lyric{}
	if out.Lrc != nil {
		lyric.Raw = out.Lrc.Lyric
	}
	if out.TLyric != nil {
		lyric.Trans = out.TLyric.Lyric
	}
	if out.RomLyric != nil {
		lyric.Roman = out.RomLyric.Lyric
	}
	lyric.Lines = ParseLRC(lyric.Raw, lyric.Trans)
	return lyric, nil
}

// Like collects or uncollects a song in 我喜欢的音乐.
func (c *Client) Like(ctx context.Context, songID int64, like bool) error {
	params := map[string]string{
		"alg":     "itembased",
		"trackId": idString(songID),
		"like":    boolString(like),
		"time":    "3",
	}
	var out Msg
	if err := c.call(ctx, modeWeapi, "/weapi/radio/like", params, &out); err != nil {
		return err
	}
	if !out.OK() {
		return fmt.Errorf("ncm: like song %d: code %d %s", songID, out.Code, out.Msg)
	}
	return nil
}

// SubscribePlaylist collects or uncollects a playlist.
func (c *Client) SubscribePlaylist(ctx context.Context, id int64, subscribe bool) error {
	path := "/api/playlist/unsubscribe"
	if subscribe {
		path = "/api/playlist/subscribe"
	}
	params := map[string]string{"id": idString(id)}
	var out Msg
	if err := c.call(ctx, modeEapi, path, params, &out); err != nil {
		return err
	}
	if !out.OK() {
		return fmt.Errorf("ncm: subscribe playlist %d: code %d %s", id, out.Code, out.Msg)
	}
	return nil
}

// SubscribeAlbum collects or uncollects an album.
func (c *Client) SubscribeAlbum(ctx context.Context, id int64, subscribe bool) error {
	path := "/api/album/unsub"
	if subscribe {
		path = "/api/album/sub"
	}
	params := map[string]string{"id": idString(id)}
	if err := c.call(ctx, modeWeapi, path, params, nil); err != nil {
		return err
	}
	return nil
}

// CloudDisk lists the songs in the account's cloud drive.
func (c *Client) CloudDisk(ctx context.Context) ([]Song, error) {
	params := map[string]string{"offset": "0", "limit": "200"}
	var out struct {
		Data []rawSong `json:"data"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/v1/cloud/get", params, &out); err != nil {
		return nil, err
	}
	songs := make([]Song, 0, len(out.Data))
	for _, s := range out.Data {
		songs = append(songs, s.song())
	}
	return songs, nil
}

// Banners lists the discover carousel slides. The API sends targetId as a JSON
// number that is sometimes written in exponent form (1.969565566e+09), which
// only decodes into a float.
func (c *Client) Banners(ctx context.Context) ([]Banner, error) {
	var out struct {
		Banners []struct {
			ImageURL   string  `json:"imageUrl"`
			BigImage   string  `json:"bigImageUrl"`
			Title      string  `json:"title"`
			TypeTitle  string  `json:"typeTitle"`
			TargetType int     `json:"targetType"`
			TargetID   float64 `json:"targetId"`
			URL        string  `json:"url"`
		} `json:"banners"`
	}
	// The endpoint answers an empty carousel without a client type.
	if err := c.call(ctx, modeWeapi, "/weapi/v2/banner/get", map[string]string{"clientType": "pc"}, &out); err != nil {
		return nil, err
	}
	list := make([]Banner, 0, len(out.Banners))
	for _, b := range out.Banners {
		image := b.BigImage
		if image == "" {
			image = b.ImageURL
		}
		title := b.Title
		if title == "" {
			title = b.TypeTitle
		}
		list = append(list, Banner{ImageURL: image, Title: title, TargetType: b.TargetType, TargetID: int64(b.TargetID), URL: b.URL})
	}
	return list, nil
}

// HotPlaylists lists public playlists by category, newest or hottest first. It
// is what the discover page shows a visitor who has not signed in, since the
// personalised recommendation answers 301 without a session.
func (c *Client) HotPlaylists(ctx context.Context, category, order string, limit int) ([]Playlist, error) {
	if category == "" {
		category = "全部"
	}
	if order == "" {
		order = "hot"
	}
	params := map[string]string{
		"cat":    category,
		"order":  order,
		"limit":  uintString(limit),
		"offset": "0",
		"total":  "true",
	}
	var out struct {
		Playlists []rawPlaylist `json:"playlists"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/playlist/list", params, &out); err != nil {
		return nil, err
	}
	list := make([]Playlist, 0, len(out.Playlists))
	for _, p := range out.Playlists {
		list = append(list, p.playlist())
	}
	return list, nil
}

// HighQualityPlaylists lists the curated 精品歌单.
func (c *Client) HighQualityPlaylists(ctx context.Context, category string, limit int) ([]Playlist, error) {
	if category == "" {
		category = "全部"
	}
	params := map[string]string{
		"cat":      category,
		"limit":    uintString(limit),
		"lasttime": "0",
		"total":    "true",
	}
	var out struct {
		Playlists []rawPlaylist `json:"playlists"`
	}
	if err := c.call(ctx, modeWeapi, "/api/playlist/highquality/list", params, &out); err != nil {
		return nil, err
	}
	list := make([]Playlist, 0, len(out.Playlists))
	for _, p := range out.Playlists {
		list = append(list, p.playlist())
	}
	return list, nil
}

// SignedURL is one of the API's own download links, refreshed when it expires.
func (c *Client) SignedURL(ctx context.Context, id string) (string, error) {
	var out struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := c.call(ctx, modeWeapi, "/weapi/song/enhance/download/url", map[string]string{"id": id, "br": "320000"}, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 || out.Data[0].URL == "" {
		return "", fmt.Errorf("ncm: no download url for %s", id)
	}
	return out.Data[0].URL, nil
}

// CoverURL rewrites a cover link to ask the image server for a given square
// size. The server's own URLs carry their own parameters; this returns the URL
// unchanged when it is not one of them.
func CoverURL(raw string, size int) string { return CoverBox(raw, size, size) }

// CoverBox is CoverURL for an image that is not square, such as a banner: the
// server crops and resizes to the box asked for, so the client never has to.
func CoverBox(raw string, width, height int) string {
	if raw == "" || width <= 0 || height <= 0 {
		return raw
	}
	if strings.Contains(raw, "music.126.net") || strings.Contains(raw, "126.net") {
		if u, err := url.Parse(raw); err == nil {
			q := u.Query()
			q.Set("param", fmt.Sprintf("%d y %d", width, height))
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return raw
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
