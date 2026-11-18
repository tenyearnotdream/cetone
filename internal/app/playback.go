package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vivaui/viva/ui"

	"ncmviva/internal/ncm"
	"ncmviva/internal/player"
	"ncmviva/internal/store"
)

// Playback. The rule that makes this client's offline library work: a cached
// file is played before the API is asked for a URL, so a song whose VIP rights
// lapsed still plays from the disk, and a song the account may stream is stored
// the first time it plays.

// playQueue replaces the queue and starts at index.
func (a *App) playQueue(songs []ncm.Song, index int) {
	if len(songs) == 0 {
		return
	}
	if index < 0 || index >= len(songs) {
		index = 0
	}
	a.queue = append([]ncm.Song(nil), songs...)
	a.queueAt = index
	a.playAt(index)
}

// playAt starts the song at index in the queue.
func (a *App) playAt(index int) {
	if index < 0 || index >= len(a.queue) {
		return
	}
	a.queueAt = index
	song := a.queue[index]
	a.persistQueue()
	go a.resolveAndPlay(song)
}

// playAtResuming starts the song at index and continues from the position a
// previous run stopped at, which is what pressing play on a restored queue does.
func (a *App) playAtResuming(index int, at time.Duration) {
	if index < 0 || index >= len(a.queue) {
		return
	}
	a.queueAt = index
	song := a.queue[index]
	position := at
	go func() {
		a.resolveAndPlayAt(song, position)
	}()
}

// resolveAndPlay plays a song from its beginning.
func (a *App) resolveAndPlay(song ncm.Song) { a.resolveAndPlayAt(song, 0) }

// resolveAndPlayAt turns a song into a source and starts the engine. The engine
// is safe from any goroutine, so the whole resolution runs on a worker; only the
// model updates go through the work queue.
//
// The order is the feature: a cached file is tried first and it is not a
// question of what the account may stream any more. A cached file that will not
// decode (an interrupted download from an older client, a disk error) falls
// back to the network rather than stopping the queue, and the file is left where
// it is: it is the user's.
func (a *App) resolveAndPlayAt(song ncm.Song, at time.Duration) {
	quality := a.songQuality(song)
	volume, muted := a.config.Volume, a.config.Muted

	cacheNote := ""
	if path, cachedQuality, ok := a.cache.Lookup(song.ID, quality); ok {
		a.engine.SetVolume(volume)
		a.engine.SetMuted(muted)
		err := a.engine.Play(context.Background(), player.Source{
			Title:    song.Name,
			File:     path,
			Duration: song.Duration,
		})
		if err == nil {
			if at > 0 {
				a.engine.Seek(at)
			}
			a.work.push(func() {
				a.playing = true
				a.noteCachedQuality(song.ID, cachedQuality)
				a.updateMPRIS()
			})
			a.invalidate()
			return
		}
		cacheNote = "缓存文件无法解码，改为在线播放"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	urls, err := a.client.SongURLs(ctx, []int64{song.ID}, quality)
	if err != nil || len(urls) == 0 || urls[0].URL == "" {
		reason := firstNonEmpty(errText(err), "该歌曲当前不可播放（可能版权或会员限制）")
		a.work.push(func() {
			detail := reason
			if cacheNote != "" {
				detail = cacheNote + "；" + reason
			}
			a.toast("无法播放", song.Name+"："+detail, ui.ToastError)
			// Skip to the next song rather than stopping the queue on one
			// unplayable track.
			if len(a.queue) > 1 {
				a.advance(1, true)
			}
		})
		a.invalidate()
		return
	}
	resolved := urls[0]
	a.engine.SetVolume(volume)
	a.engine.SetMuted(muted)
	source := player.Source{
		Title:     song.Name,
		Stream:    resolved.URL,
		Duration:  song.Duration,
		CachePath: a.cache.Path(song.ID, resolved.Quality),
		Header:    http.Header{"Referer": {"https://music.163.com"}, "User-Agent": {userAgentHeader}},
	}
	if err := a.engine.Play(ctx, source); err != nil {
		message := errText(err)
		a.work.push(func() { a.toast("无法播放", message, ui.ToastError) })
		a.invalidate()
		return
	}
	if at > 0 {
		a.engine.Seek(at)
	}
	a.work.push(func() {
		a.playing = true
		if cacheNote != "" {
			a.toast("缓存文件已损坏", cacheNote, ui.ToastWarning)
		}
		a.noteCachedQuality(song.ID, resolved.Quality)
		a.updateMPRIS()
	})
	a.invalidate()
}

// songQuality is the tier to ask for: the one picked for this song, else the
// highest the account may stream at, else the setting.
func (a *App) songQuality(song ncm.Song) ncm.Quality {
	quality := song.Selected
	if quality == 0 && song.Privilege.Level != "" {
		if q, ok := ncm.QualityFromLevel(song.Privilege.Level); ok {
			quality = q
		}
	}
	if preferred := a.config.PreferredQuality(); preferred > quality {
		quality = preferred
	}
	return quality
}

// noteCachedQuality records the tier a song played at, so the list can show
// what the file on disk is.
func (a *App) noteCachedQuality(songID int64, quality ncm.Quality) {
	for i := range a.queue {
		if a.queue[i].ID != songID {
			continue
		}
		a.queue[i].Actual = quality
		if a.queue[i].Selected == 0 {
			a.queue[i].Selected = quality
		}
	}
}

// userAgentHeader is what the music CDN expects of a player.
const userAgentHeader = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// current is the song the queue points at.
func (a *App) current() (ncm.Song, bool) {
	if a.queueAt < 0 || a.queueAt >= len(a.queue) {
		return ncm.Song{}, false
	}
	return a.queue[a.queueAt], true
}

// togglePlay pauses or resumes; with nothing loaded it starts the queue.
func (a *App) togglePlay() {
	switch a.engine.Snapshot().Status {
	case player.StatusPlaying, player.StatusBuffering:
		a.engine.Pause()
		a.playing = false
	case player.StatusPaused:
		a.engine.Resume(context.Background())
		a.playing = true
	default:
		// A queue restored from the last run continues where it stopped, rather
		// than starting the song over.
		if len(a.queue) > 0 {
			at := a.resumeAt
			a.resumeAt = 0
			if at > 0 {
				a.playAtResuming(a.queueAt, at)
			} else {
				a.playAt(a.queueAt)
			}
		}
	}
	a.updateMPRIS()
}

// advance moves the cursor by delta, honouring the repeat mode, and starts the
// song. wrapToStart is set when skipping a song that could not play, which must
// not loop forever.
func (a *App) advance(delta int, fromFailure bool) {
	if len(a.queue) == 0 {
		return
	}
	switch a.config.RepeatMode() {
	case store.RepeatOne:
		if !fromFailure {
			a.playAt(a.queueAt)
			return
		}
	case store.RepeatOff:
		if !fromFailure && delta > 0 && a.queueAt == len(a.queue)-1 {
			a.engine.Stop()
			a.playing = false
			a.persistQueue()
			a.updateMPRIS()
			a.invalidate()
			return
		}
	}
	next := a.queueAt + delta
	if next >= len(a.queue) {
		next = 0
	}
	if next < 0 {
		next = len(a.queue) - 1
	}
	a.playAt(next)
}

// finishedTrack is the engine's end-of-song report. In 心动模式 the queue is
// extended before it advances, which is what makes the mode endless; after a
// failure to extend it falls back to the ordinary repeat rules.
func (a *App) finishedTrack() {
	if len(a.queue) == 0 {
		return
	}
	if a.heartbeat && a.queueAt == len(a.queue)-1 {
		if a.extendHeartbeat() {
			a.advance(1, false)
			return
		}
		a.heartbeat = false
		a.toast("心动模式已停止", "推荐没有返回新的歌曲", ui.ToastDefault)
	}
	a.advance(1, false)
}

// toggleHeartbeat starts or stops 心动模式: an endless queue built from the
// recommender, seeded by the song that is playing.
func (a *App) toggleHeartbeat() {
	if a.heartbeat {
		a.heartbeat = false
		a.toast("心动模式", "已关闭", ui.ToastDefault)
		a.invalidate()
		return
	}
	if _, ok := a.current(); !ok {
		a.toast("心动模式", "先播放一首歌", ui.ToastError)
		return
	}
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	a.heartbeat = true
	a.toast("心动模式", "播放到列表末尾时会自动加入推荐歌曲", ui.ToastSuccess)
	a.invalidate()
}

// extendHeartbeat appends recommended songs, seeded by the playlist the queue
// came from and the song that just played.
func (a *App) extendHeartbeat() bool {
	song, ok := a.current()
	if !ok {
		return false
	}
	// The API wants the playlist the seed song belongs to; the last song of
	// 我喜欢的音乐 works with the liked list, and a song of a playlist with that
	// playlist. When neither is known, the seed song is its own context.
	playlistID := a.heartbeatPlaylist
	if playlistID == 0 {
		playlistID = likedSongsID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	songs, err := a.client.HeartbeatSongs(ctx, song.ID, playlistID)
	if err != nil || len(songs) == 0 {
		return false
	}
	seen := make(map[int64]bool, len(a.queue))
	for _, existing := range a.queue {
		seen[existing.ID] = true
	}
	added := 0
	for _, candidate := range songs {
		if seen[candidate.ID] {
			continue
		}
		a.queue = append(a.queue, candidate)
		seen[candidate.ID] = true
		added++
	}
	if added == 0 {
		return false
	}
	a.persistQueue()
	a.toast("心动模式", fmt.Sprintf("已加入 %d 首推荐", added), ui.ToastSuccess)
	a.invalidate()
	return true
}

// next plays the next song.
func (a *App) next() { a.advance(1, false) }

// previous plays the previous song.
func (a *App) previous() { a.advance(-1, false) }

// seekTo moves the transport.
func (a *App) seekTo(at time.Duration) {
	a.engine.Seek(at)
	a.updateMPRIS()
}

// playable filters the songs this account may stream, unless the user asked to
// keep them. A cached song of a song with no rights left is still playable, so
// the cache is consulted before a row is dropped.
func (a *App) playable(songs []ncm.Song) []ncm.Song {
	if !a.config.IgnoreUnplayable {
		return songs
	}
	kept := make([]ncm.Song, 0, len(songs))
	for _, song := range songs {
		if song.Copyright().Playable() || a.cached.has(song.ID) {
			kept = append(kept, song)
		}
	}
	return kept
}

// addToQueue appends songs, starting the player when it was idle.
func (a *App) addToQueue(songs []ncm.Song, playNow bool) {
	songs = a.playable(songs)
	if len(songs) == 0 {
		return
	}
	startedEmpty := len(a.queue) == 0
	a.queue = append(a.queue, songs...)
	if startedEmpty {
		a.queueAt = 0
	}
	if playNow {
		a.playAt(len(a.queue) - len(songs))
		return
	}
	if startedEmpty {
		a.playAt(0)
	}
	a.persistQueue()
	a.invalidate()
}

// playSongs replaces the queue with songs and starts at index. The index is the
// row the user pressed, which filtering cannot move: the song is looked up in
// the filtered list, and dropped songs before it do not shift the start.
func (a *App) playSongs(songs []ncm.Song, index int) {
	if index < 0 || index >= len(songs) {
		index = 0
	}
	chosen := songs[index]
	kept := a.playable(songs)
	if len(kept) == 0 {
		a.toast("没有可播放的歌曲", "这些歌曲当前都不可播放；缓存过的歌曲仍可离线播放", ui.ToastError)
		return
	}
	start := 0
	for i := range kept {
		if kept[i].ID == chosen.ID {
			start = i
			break
		}
	}
	songs = kept
	index = start
	a.playQueue(songs, index)
	a.invalidate()
}

// removeFromQueue takes a row out, keeping the current song playing.
func (a *App) removeFromQueue(index int) {
	if index < 0 || index >= len(a.queue) {
		return
	}
	current := a.queueAt
	a.queue = append(a.queue[:index], a.queue[index+1:]...)
	switch {
	case len(a.queue) == 0:
		a.queueAt = 0
		a.engine.Stop()
	case index < current:
		a.queueAt = current - 1
	case index == current:
		a.queueAt = min(index, len(a.queue)-1)
		a.playAt(a.queueAt)
	default:
		a.queueAt = current
	}
	a.persistQueue()
	a.invalidate()
}

// clearQueue empties the queue and stops.
func (a *App) clearQueue() {
	a.queue = nil
	a.queueAt = 0
	a.engine.Stop()
	a.playing = false
	a.persistQueue()
	a.invalidate()
}

// setVolume stores and applies the gain.
func (a *App) setVolume(volume float64) {
	a.config.Volume = volume
	a.config.Muted = volume == 0 && a.config.Muted
	a.engine.SetVolume(volume)
	a.saveConfig()
	a.invalidate()
}

// toggleMute flips the mute state.
func (a *App) toggleMute() {
	a.config.Muted = !a.config.Muted
	a.engine.SetMuted(a.config.Muted)
	a.saveConfig()
	a.updateMPRIS()
	a.invalidate()
}

// like toggles a song in 我喜欢的音乐.
func (a *App) like(song ncm.Song) {
	if a.user.UserID == 0 {
		a.toast("需要登录", "扫码登录后才能收藏歌曲", ui.ToastError)
		a.login.open = true
		return
	}
	want := !a.likedSet[song.ID]
	a.likedSet[song.ID] = want
	songID := song.ID
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return a.client.Like(ctx, songID, want)
	}, func(err error) {
		if err != nil {
			a.likedSet[songID] = !want
			a.toast("收藏失败", errText(err), ui.ToastError)
			return
		}
		if want {
			a.toast("已收藏", song.Name, ui.ToastSuccess)
		} else {
			a.toast("已取消收藏", song.Name, ui.ToastDefault)
		}
		if a.data.mine.loaded {
			a.data.mine.loaded = false
			if a.top().kind == routeMine {
				a.loadMine()
			}
		}
	})
	a.invalidate()
}

// subscribePlaylist collects or uncollects a playlist.
func (a *App) subscribePlaylist(playlist ncm.Playlist) {
	if a.user.UserID == 0 {
		a.toast("需要登录", "扫码登录后才能收藏歌单", ui.ToastError)
		a.login.open = true
		return
	}
	want := !playlist.Subscribed
	id := playlist.ID
	name := playlist.Name
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return a.client.SubscribePlaylist(ctx, id, want)
	}, func(err error) {
		if err != nil {
			a.toast("操作失败", errText(err), ui.ToastError)
			return
		}
		if data := a.data.playlists[id]; data != nil {
			data.playlist.Subscribed = want
		}
		if want {
			a.toast("已收藏歌单", name, ui.ToastSuccess)
		} else {
			a.toast("已取消收藏", name, ui.ToastDefault)
		}
	})
	a.invalidate()
}

// persistQueue writes the queue, its cursor and the position, so the next run
// can offer to continue where this one stopped.
func (a *App) persistQueue() {
	snapshot := a.engine.Snapshot()
	queue := store.Queue{
		Songs:      a.queue,
		Index:      a.queueAt,
		Position:   snapshot.Position,
		WasPlaying: a.playing,
	}
	_ = a.queues.Save(queue)
}

// saveConfig writes the settings.
func (a *App) saveConfig() { _ = a.settings.Save(a.config) }

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
