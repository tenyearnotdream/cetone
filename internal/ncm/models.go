package ncm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Quality is one of the eight音质 tiers the API sells. Its order is the order
// the settings menu lists them in, worst to best.
type Quality int

const (
	QualityStandard   Quality = iota // 标准, 128 kb/s
	QualityHigher                    // 较高, 192 kb/s
	QualityExtreme                   // 极高, 320 kb/s
	QualityLossless                  // 无损
	QualityHiRes                     // Hi-Res
	QualitySurround                  // 环绕声
	QualityAudioVivid                // 沉浸声
	QualityMaster                    // 母带
	qualityCount
)

// Bitrate is the cache key's rate for a quality. It matches the numbers the
// GTK client has written into its cache file names since 2022, so a cache
// directory from that client is playable here unchanged.
func (q Quality) Bitrate() int {
	switch q {
	case QualityStandard:
		return 128000
	case QualityHigher:
		return 192000
	case QualityExtreme:
		return 320000
	case QualityLossless:
		return 999000
	case QualityHiRes:
		return 1900000
	case QualitySurround:
		return 804505
	case QualityAudioVivid:
		return 2695684
	case QualityMaster:
		return 4532511
	}
	return 320000
}

// Level is the string the v1 URL endpoint takes.
func (q Quality) Level() string {
	switch q {
	case QualityStandard:
		return "standard"
	case QualityHigher:
		return "higher"
	case QualityExtreme:
		return "exhigh"
	case QualityLossless:
		return "lossless"
	case QualityHiRes:
		return "hires"
	case QualitySurround:
		return "jyeffect"
	case QualityAudioVivid:
		return "sky"
	case QualityMaster:
		return "jymaster"
	}
	return "exhigh"
}

// EncodeType is the container the endpoint should answer with: mp3 for the
// three lossy tiers (which decode with no system library), flac above them.
func (q Quality) EncodeType() string {
	switch q {
	case QualityStandard, QualityHigher, QualityExtreme:
		return "mp3"
	default:
		return "flac"
	}
}

func (q Quality) String() string {
	switch q {
	case QualityStandard:
		return "标准"
	case QualityHigher:
		return "较高"
	case QualityExtreme:
		return "极高"
	case QualityLossless:
		return "无损"
	case QualityHiRes:
		return "Hi-Res"
	case QualitySurround:
		return "环绕声"
	case QualityAudioVivid:
		return "沉浸声"
	case QualityMaster:
		return "母带"
	}
	return "未知"
}

// Qualities lists every tier, for a settings menu.
func Qualities() []Quality {
	out := make([]Quality, 0, qualityCount)
	for q := QualityStandard; q < qualityCount; q++ {
		out = append(out, q)
	}
	return out
}

// QualityFromLevel maps the API's level string back to a tier.
func QualityFromLevel(level string) (Quality, bool) {
	for q := QualityStandard; q < qualityCount; q++ {
		if q.Level() == level {
			return q, true
		}
	}
	return QualityExtreme, false
}

// qualityFromBitrate guesses a tier from a bit rate, for responses that carry
// no level.
func qualityFromBitrate(rate int) Quality {
	switch {
	case rate <= 128000:
		return QualityStandard
	case rate <= 192000:
		return QualityHigher
	case rate <= 320000:
		return QualityExtreme
	case rate <= 999000:
		return QualityLossless
	case rate <= 1900000:
		return QualityHiRes
	case rate <= 2695683:
		return QualitySurround
	case rate <= 4532510:
		return QualityAudioVivid
	}
	return QualityMaster
}

// Artist is the performer credit on a song, or a search hit of its own.
type Artist struct {
	ID     int64
	Name   string
	PicURL string
}

// Album is the record a song belongs to. On a song only ID, Name and PicURL
// are filled; the album page fills the rest.
type Album struct {
	ID          int64
	Name        string
	PicURL      string
	Artist      string
	ArtistID    int64
	PublishTime int64 // ms since the epoch
	Description string
	TrackCount  int
	Songs       []Song
}

// Copyright is what the user is allowed to do with a song. It is derived from
// the privilege block the API returns with every song, and it is why a greyed
// row exists: VIP-only, paid, or gone.
type Copyright int

const (
	CopyrightUnknown Copyright = iota
	CopyrightFree
	CopyrightVIP
	CopyrightPaid
	CopyrightVIPHighRate
	CopyrightUnavailable
)

func (c Copyright) String() string {
	switch c {
	case CopyrightFree:
		return "免费"
	case CopyrightVIP:
		return "VIP"
	case CopyrightPaid:
		return "付费"
	case CopyrightVIPHighRate:
		return "VIP高音质"
	case CopyrightUnavailable:
		return "无版权"
	}
	return ""
}

// Playable reports whether a request for the song has a chance of returning a
// URL. A cached file wins even when this says no: that is the point of the
// cache.
func (c Copyright) Playable() bool { return c != CopyrightUnavailable }

// Song is one track. Its fields are the flat shape the UI reads; the API's
// several song shapes are all normalised into it by the decoders below.
type Song struct {
	ID       int64
	Name     string
	Alias    []string
	Artists  []Artist
	Album    Album
	Duration time.Duration
	Fee      int
	Status   int
	// Available is the set of tiers the account may stream, when the API
	// reports it.
	Available []Quality
	// Selected is the tier the user picked for this play, and SelectedSet says the
	// pick is authoritative. The flag exists because QualityStandard is zero: a
	// bare Selected cannot tell "the user chose 标准" from "the user chose nothing".
	Selected    Quality
	SelectedSet bool
	// Actual is the tier the server answered with.
	Actual Quality
	// URL is the stream URL of the last resolve, empty when unresolved.
	URL string
	// Privilege is the raw privilege code block, kept for the like button and
	// the quality menu.
	Privilege Privilege
}

// Copyright derives the playable state from the privilege block.
func (s *Song) Copyright() Copyright {
	p := s.Privilege
	if p.Status < 0 || (s.Status < 0 && p.Status == 0) {
		return CopyrightUnavailable
	}
	if p.Playable == false && p.Status == 0 {
		// Some endpoints only send st/fee on the song itself.
		if s.Fee == 0 && s.Status == 0 {
			return CopyrightUnknown
		}
	}
	switch s.Fee {
	case 0:
		return CopyrightFree
	case 1:
		return CopyrightVIP
	case 4:
		return CopyrightPaid
	case 8:
		return CopyrightVIPHighRate
	}
	return CopyrightUnknown
}

// Singer is the display form of the artist list: 名字一/名字二.
func (s Song) Singer() string {
	if len(s.Artists) == 0 {
		return "未知艺术家"
	}
	names := make([]string, 0, len(s.Artists))
	for _, a := range s.Artists {
		names = append(names, a.Name)
	}
	return strings.Join(names, "/")
}

// ArtistID is the first artist's id, for the artist page.
func (s Song) ArtistID() int64 {
	if len(s.Artists) == 0 {
		return 0
	}
	return s.Artists[0].ID
}

// Title is the name with its alias, as the player bar shows it.
func (s Song) Title() string {
	if len(s.Alias) > 0 && s.Alias[0] != "" {
		return s.Name + " (" + s.Alias[0] + ")"
	}
	return s.Name
}

// Privilege is the per-song permission block. Only the fields the application
// reads are modelled; the JSON carries a dozen more.
type Privilege struct {
	ID       int64 `json:"id"`
	Fee      int   `json:"fee"`
	Playable bool  `json:"-"`
	Status   int   `json:"st"`
	Subp     int   `json:"subp"`
	MaxBr    int   `json:"maxbr"`
	Flag     int   `json:"flag"`
	Pl       int   `json:"pl"`
	Dl       int   `json:"dl"`
	CP       int   `json:"cp"`
	// Level is the highest tier this account may play.
	Level    string `json:"plLevel"`
	Download string `json:"dlLevel"`
}

// Playlist is a user playlist, a toplist, or a daily-recommendation block;
// they share one shape on the wire.
type Playlist struct {
	ID          int64
	Name        string
	CoverImgURL string
	TrackCount  int
	PlayCount   int64
	Description string
	Creator     UserInfo
	UpdateTime  time.Time
	Subscribed  bool
	SpecialType int
	// Tracks is filled for a detail call, empty in a list call.
	Tracks []Song
	// TrackIDs is what a detail call returns instead of tracks when the list
	// is over a thousand songs.
	TrackIDs []int64
}

// TopList is one entry of the toplist index.
type TopList struct {
	ID              int64
	Name            string
	CoverImgURL     string
	UpdateFrequency string
	TrackCount      int
	PlayCount       int64
	Description     string
}

// Banner is a discover carousel slide.
type Banner struct {
	ImageURL   string
	Title      string
	TargetType int
	TargetID   int64
	URL        string
}

// UserInfo is the signed-in account, or a playlist's creator.
type UserInfo struct {
	UserID    int64
	Nickname  string
	AvatarURL string
	Signature string
	VIPType   int
	Followeds int
	Follows   int
}

// Lyric is a song's words. Raw is the LRC file as the server sent it; Lines is
// the parsed timed text.
type Lyric struct {
	Raw     string
	Trans   string
	Roman   string
	Lines   []LyricLine
	HasWord bool
}

// LyricLine is one timed line. Translation is empty when there is none.
type LyricLine struct {
	At          time.Duration
	Text        string
	Translation string
}

// SearchKind is the resource a search asks for.
type SearchKind int

const (
	SearchSongs SearchKind = iota
	SearchArtists
	SearchAlbums
	SearchPlaylists
)

// Search requests the API type number for a kind.
func (k SearchKind) apiType() int {
	switch k {
	case SearchArtists:
		return 100
	case SearchAlbums:
		return 10
	case SearchPlaylists:
		return 1000
	}
	return 1
}

func (k SearchKind) String() string {
	switch k {
	case SearchArtists:
		return "歌手"
	case SearchAlbums:
		return "专辑"
	case SearchPlaylists:
		return "歌单"
	}
	return "单曲"
}

// SearchResult is one page of any kind of search.
type SearchResult struct {
	Kind      SearchKind
	Songs     []Song
	Artists   []Artist
	Albums    []Album
	Playlists []Playlist
	Total     int
	HasMore   bool
}

// Msg is an endpoint answer that carries only a code.
type Msg struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (m Msg) OK() bool { return m.Code == 200 }

// SongURL is one entry of the URL endpoint's answer.
type SongURL struct {
	ID         int64
	URL        string
	Bitrate    int
	Size       int64
	MD5        string
	Type       string
	Level      string
	Quality    Quality
	EncodeType string
	Code       int
	Fee        int
	FreeTrial  bool
}

// ----- raw wire shapes -----

type rawArtist struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	PicURL    string `json:"picUrl"`
	Img1v1URL string `json:"img1v1Url"`
	AlbumSize int    `json:"albumSize"`
}

func (r rawArtist) artist() Artist {
	pic := r.PicURL
	if pic == "" {
		pic = r.Img1v1URL
	}
	return Artist{ID: r.ID, Name: r.Name, PicURL: pic}
}

type rawAlbum struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	PicURL      string     `json:"picUrl"`
	Artist      *rawArtist `json:"artist"`
	PublishTime int64      `json:"publishTime"`
	Description string     `json:"description"`
	Size        int        `json:"size"`
}

func (a *rawAlbum) album() Album {
	if a == nil {
		return Album{}
	}
	out := Album{ID: a.ID, Name: a.Name, PicURL: a.PicURL, PublishTime: a.PublishTime, Description: a.Description, TrackCount: a.Size}
	if a.Artist != nil {
		out.Artist = a.Artist.Name
		out.ArtistID = a.Artist.ID
	}
	return out
}

type rawSong struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Alias     []string      `json:"alia"`
	Ar        []rawArtist   `json:"ar"`
	Artists   []rawArtist   `json:"artists"`
	Al        *rawAlbum     `json:"al"`
	Album     *rawAlbum     `json:"album"`
	Dt        int64         `json:"dt"`
	Duration  int64         `json:"duration"`
	Fee       int           `json:"fee"`
	St        int           `json:"st"`
	Privilege *rawPrivilege `json:"privilege"`
	HMusic    *struct {
		Bitrate int `json:"bitrate"`
	} `json:"h"`
	MMusic *struct {
		Bitrate int `json:"bitrate"`
	} `json:"m"`
	LMusic *struct {
		Bitrate int `json:"bitrate"`
	} `json:"l"`
	// Cloud disk entries carry the fields flat.
	SongID   int64  `json:"songId"`
	SongName string `json:"songName"`
}

type rawPrivilege struct {
	ID      int64  `json:"id"`
	Fee     int    `json:"fee"`
	St      int    `json:"st"`
	Subp    int    `json:"subp"`
	MaxBr   int    `json:"maxbr"`
	Flag    int    `json:"flag"`
	Pl      int    `json:"pl"`
	Dl      int    `json:"dl"`
	Cp      int    `json:"cp"`
	PlLevel string `json:"plLevel"`
	DlLevel string `json:"dlLevel"`
}

func (p *rawPrivilege) privilege() Privilege {
	if p == nil {
		return Privilege{}
	}
	return Privilege{ID: p.ID, Fee: p.Fee, Status: p.St, Subp: p.Subp, MaxBr: p.MaxBr, Flag: p.Flag, Pl: p.Pl, Dl: p.Dl, CP: p.Cp, Level: p.PlLevel, Download: p.DlLevel}
}

// song normalises one wire song into the flat application shape.
func (r rawSong) song() Song {
	id, name := r.ID, r.Name
	if id == 0 {
		id, name = r.SongID, r.SongName
	}
	artists := r.Ar
	if len(artists) == 0 {
		artists = r.Artists
	}
	out := Song{
		ID:        id,
		Name:      name,
		Alias:     r.Alias,
		Duration:  time.Duration(r.Dt) * time.Millisecond,
		Fee:       r.Fee,
		Status:    r.St,
		Privilege: r.Privilege.privilege(),
	}
	if out.Duration == 0 {
		out.Duration = time.Duration(r.Duration) * time.Millisecond
	}
	for _, a := range artists {
		out.Artists = append(out.Artists, a.artist())
	}
	album := r.Al
	if album == nil {
		album = r.Album
	}
	out.Album = album.album()
	if out.Fee == 0 {
		out.Fee = out.Privilege.Fee
	}
	if out.Status == 0 {
		out.Status = out.Privilege.Status
	}
	// The tiers the account can stream, in the order a menu should show them.
	for _, br := range []int{out.Privilege.MaxBr, bitrateOf(r.HMusic), bitrateOf(r.MMusic), bitrateOf(r.LMusic)} {
		if br > 0 {
			out.Available = appendQuality(out.Available, qualityFromBitrate(br))
		}
	}
	return out
}

func bitrateOf(v *struct {
	Bitrate int `json:"bitrate"`
}) int {
	if v == nil {
		return 0
	}
	return v.Bitrate
}

func appendQuality(list []Quality, q Quality) []Quality {
	for _, have := range list {
		if have == q {
			return list
		}
	}
	return append(list, q)
}

type rawPlaylist struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	CoverImgURL string    `json:"coverImgUrl"`
	TrackCount  int       `json:"trackCount"`
	PlayCount   int64     `json:"playCount"`
	Description string    `json:"description"`
	Creator     *rawUser  `json:"creator"`
	UpdateTime  int64     `json:"updateTime"`
	Subscribed  bool      `json:"subscribed"`
	SpecialType int       `json:"specialType"`
	Tracks      []rawSong `json:"tracks"`
	TrackIDs    []struct {
		ID int64 `json:"id"`
	} `json:"trackIds"`
}

func (p rawPlaylist) playlist() Playlist {
	out := Playlist{
		ID:          p.ID,
		Name:        p.Name,
		CoverImgURL: p.CoverImgURL,
		TrackCount:  p.TrackCount,
		PlayCount:   p.PlayCount,
		Description: p.Description,
		Subscribed:  p.Subscribed,
		SpecialType: p.SpecialType,
	}
	if p.Creator != nil {
		out.Creator = p.Creator.user()
	}
	if p.UpdateTime > 0 {
		out.UpdateTime = time.UnixMilli(p.UpdateTime)
	}
	for _, t := range p.Tracks {
		out.Tracks = append(out.Tracks, t.song())
	}
	if len(out.Tracks) == 0 {
		for _, t := range p.TrackIDs {
			out.TrackIDs = append(out.TrackIDs, t.ID)
		}
	}
	return out
}

type rawUser struct {
	UserID    int64  `json:"userId"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatarUrl"`
	Signature string `json:"signature"`
	VIPType   int    `json:"vipType"`
	Followeds int    `json:"followeds"`
	Follows   int    `json:"follows"`
}

func (u rawUser) user() UserInfo {
	return UserInfo{UserID: u.UserID, Nickname: u.Nickname, AvatarURL: u.AvatarURL, Signature: u.Signature, VIPType: u.VIPType, Followeds: u.Followeds, Follows: u.Follows}
}

// ----- small helpers -----

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func randomByte() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return byte(time.Now().UnixNano())
	}
	return b[0]
}

// DurationString formats a duration as m:ss, or h:mm:ss past an hour.
func DurationString(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d / time.Second)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
