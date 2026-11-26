package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

// Playback and highest-quality caching policy:
//
//  1. Default playback aims for the highest quality available to the account
//     (asking the API for QualityMaster, which the server downgrades to the
//     song/account ceiling). Once that highest tier is cached and recorded in
//     library.json for the current VIP level, subsequent plays open the local
//     file immediately with zero network requests — even after VIP expires or
//     when offline.
//  2. If the user switches quality during playback from the player bar badge,
//     playback resumes seamlessly at the current position in the chosen tier
//     without overwriting or downgrading the highest-quality cache. If the song
//     is not yet cached at its highest tier while online, a background upgrade
//     still fetches and caches the highest tier.
//  3. Every song switch cancels any in-flight URL resolution via playGen +
//     context cancellation and calls engine.Prepare immediately so the previous
//     track stops at once and the progress bar resets cleanly.

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

// playAt starts the song at index from its beginning.
func (a *App) playAt(index int) {
	a.playAtResuming(index, 0)
}

// playAtResuming starts the song at index directly at position at (used for
// quality switching and restored queues).
func (a *App) playAtResuming(index int, at time.Duration) {
	if index < 0 || index >= len(a.queue) {
		return
	}
	if at < 0 {
		at = 0
	}
	a.queueAt = index
	song := a.queue[index]

	gen, ctx := a.nextPlaySlot()

	// Reset slider & engine immediately so old audio stops at once and the
	// progress bar never displays the previous track's time or duration.
	a.seekPending = false
	a.seekTarget = at
	a.seekDrag.Value = progressPercent(at, song.Duration)
	a.qualityMenuOpen = false
	a.engine.Prepare(song.ID, song.Name, song.Duration, at)

	a.persistQueueAt(at)
	a.followPlayingSong(song)
	a.updateMPRIS()
	a.updateTray()
	a.invalidate()

	go a.resolveAndPlayGen(ctx, gen, song, at)
}

// nextPlaySlot increments the playback generation and cancels any previous
// in-flight resolution.
func (a *App) nextPlaySlot() (uint64, context.Context) {
	gen := a.playGen.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	a.playCancelMu.Lock()
	prev := a.playCancel
	a.playCancel = cancel
	a.playCancelMu.Unlock()
	if prev != nil {
		prev()
	}
	return gen, ctx
}

func (a *App) cancelPendingPlay() {
	a.playGen.Add(1)
	a.playCancelMu.Lock()
	prev := a.playCancel
	a.playCancel = nil
	a.playCancelMu.Unlock()
	if prev != nil {
		prev()
	}
}

// resolveAndPlay resolves and plays song from 0 (kept for direct test callers).
func (a *App) resolveAndPlay(song ncm.Song) { a.resolveAndPlayAt(song, 0) }

// resolveAndPlayAt resolves and plays song at position at (kept for direct test
// callers).
func (a *App) resolveAndPlayAt(song ncm.Song, at time.Duration) {
	gen, ctx := a.nextPlaySlot()
	a.resolveAndPlayGen(ctx, gen, song, at)
}

func (a *App) resolveAndPlayGen(ctx context.Context, gen uint64, song ncm.Song, at time.Duration) {
	stale := func() bool {
		return a.playGen.Load() != gen || ctx.Err() != nil
	}
	if stale() {
		return
	}

	volume, muted := a.engine.Volume(), a.engine.Muted()
	vipType := a.currentVIP()

	if song.SelectedSet {
		a.resolveSelectedQuality(ctx, gen, song, at, volume, muted, vipType)
		return
	}
	a.resolveBestQuality(ctx, gen, song, at, volume, muted, vipType)
}

// resolveSelectedQuality plays an explicit user-chosen tier for song, using an
// exact cached file if present and otherwise streaming that tier without
// downgrading the highest-quality local cache.
func (a *App) resolveSelectedQuality(ctx context.Context, gen uint64, song ncm.Song, at time.Duration, volume float64, muted bool, vipType int) {
	stale := func() bool { return a.playGen.Load() != gen || ctx.Err() != nil }
	want := song.Selected

	// 1. Exact tier already cached locally: play immediately with no network.
	if exactPath, ok := a.cache.LookupExact(song.ID, want); ok {
		if a.playCachedGen(gen, song, exactPath, want, at, volume, muted) {
			a.ensureHighestCachedAsync(song, vipType)
			return
		}
		a.notifyCachedBroken(song)
	}

	// 2. Ask the API for the chosen tier.
	resolved, usable, err := a.fetchSongURL(ctx, song.ID, want)
	if stale() {
		return
	}

	if usable && !resolved.FreeTrial {
		cacheable := cacheableStream(song, resolved) && a.shouldCacheResolved(song, resolved.Quality)
		if err := a.playStreamGen(gen, song, resolved, cacheable, at, volume, muted); err == nil {
			// Record best if this tier is higher than anything seen so far.
			a.noteBestTierForVIP(song, resolved.Quality, vipType)
			// Even if the user picked a lower tier for playback, keep building
			// the highest-quality VIP offline library in the background.
			a.ensureHighestCachedAsync(song, vipType)
			return
		}
		return
	}

	// 3. Offline / VIP expired / server refused: fall back to the highest cached
	// file on disk rather than stopping playback.
	if bestPath, bestCached, ok := a.cache.LookupBest(song.ID); ok {
		if a.playCachedGen(gen, song, bestPath, bestCached, at, volume, muted) {
			a.work.push(func() {
				a.toast("已使用本地缓存", fmt.Sprintf("%s：无法获取 %s，已切换至本地 %s", song.Name, want.String(), bestCached.String()), ui.ToastDefault)
			})
			a.invalidate()
			return
		}
	}

	// 4. Only a free trial preview is available and nothing is cached.
	if usable && resolved.FreeTrial {
		_ = a.playStreamGen(gen, song, resolved, false, at, volume, muted)
		return
	}

	if stale() {
		return
	}
	reason := firstNonEmpty(errText(err), "无法切换到所选音质（可能网络或权限限制）")
	a.work.push(func() {
		a.toast("切换音质失败", song.Name+"："+reason, ui.ToastError)
	})
	a.invalidate()
}

// resolveBestQuality implements the core VIP highest-quality cache-first flow:
//   - If the highest tier for the current VIP level is already cached, play
//     immediately from disk with zero network requests.
//   - Otherwise ask the server for QualityMaster; stream & cache if a higher
//     tier is available, or fall back to the best local file when offline,
//     when VIP has lapsed (FreeTrial), or when the server cannot beat disk.
func (a *App) resolveBestQuality(ctx context.Context, gen uint64, song ncm.Song, at time.Duration, volume float64, muted bool, vipType int) {
	stale := func() bool { return a.playGen.Load() != gen || ctx.Err() != nil }

	bestPath, bestCached, hasCache := a.cache.LookupBest(song.ID)

	// Fast path: disk already holds the known highest tier for this account
	// (including after VIP has expired, since currentVIP <= ProbedVIP).
	if hasCache && a.cacheIsAlreadyBest(song.ID, bestCached, vipType) {
		if a.playCachedGen(gen, song, bestPath, bestCached, at, volume, muted) {
			return
		}
		hasCache = false
		a.notifyCachedBroken(song)
	}

	// Probe the server for the absolute highest tier available.
	resolved, usable, err := a.fetchSongURL(ctx, song.ID, ncm.QualityMaster)
	if stale() {
		return
	}

	// If offline, VIP expired (FreeTrial), or server has nothing better than
	// what is already on disk, play the cached file.
	if hasCache && (!usable || resolved.FreeTrial || resolved.Quality <= bestCached) {
		if usable && !resolved.FreeTrial && resolved.Quality <= bestCached {
			// Server confirmed disk already holds the ceiling for this VIP level;
			// persist that knowledge so future plays skip the network completely.
			a.recordCachedAsBest(song.ID, bestPath, bestCached, vipType)
		}
		if a.playCachedGen(gen, song, bestPath, bestCached, at, volume, muted) {
			return
		}
		// Cached file turned out corrupt; fall through to stream if usable.
		a.notifyCachedBroken(song)
	}

	if !usable {
		if stale() {
			return
		}
		reason := firstNonEmpty(errText(err), "该歌曲当前不可播放（可能版权或会员限制）")
		a.work.push(func() {
			if a.playGen.Load() != gen {
				return
			}
			a.toast("无法播放", song.Name+"："+reason, ui.ToastError)
			if len(a.queue) > 1 {
				a.advance(1, true)
			}
		})
		a.invalidate()
		return
	}

	if !resolved.FreeTrial {
		a.noteBestTierForVIP(song, resolved.Quality, vipType)
	}
	cacheable := cacheableStream(song, resolved) && a.isBestTier(song, resolved.Quality)
	_ = a.playStreamGen(gen, song, resolved, cacheable, at, volume, muted)
}

func (a *App) fetchSongURL(ctx context.Context, songID int64, quality ncm.Quality) (ncm.SongURL, bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resolve := a.resolveURL
	if resolve == nil {
		resolve = a.client.SongURLs
	}
	urls, err := resolve(reqCtx, []int64{songID}, quality)
	if err != nil || len(urls) == 0 || urls[0].URL == "" {
		return ncm.SongURL{}, false, err
	}
	return urls[0], true, nil
}

func (a *App) playStreamGen(gen uint64, song ncm.Song, resolved ncm.SongURL, cacheable bool, at time.Duration, volume float64, muted bool) error {
	if a.playGen.Load() != gen {
		return context.Canceled
	}
	a.engine.SetVolume(volume)
	a.engine.SetMuted(muted)
	source := player.Source{
		SongID:   song.ID,
		Title:    song.Name,
		Stream:   resolved.URL,
		Duration: song.Duration,
		Header:   http.Header{"Referer": {"https://music.163.com"}, "User-Agent": {userAgentHeader}},
	}
	if cacheable {
		source.CachePath = a.cache.Path(song.ID, resolved.Quality)
	}
	if err := a.engine.PlayAt(context.Background(), source, at); err != nil {
		if a.playGen.Load() != gen {
			return err
		}
		message := errText(err)
		a.work.push(func() {
			if a.playGen.Load() != gen {
				return
			}
			a.toast("无法播放", message, ui.ToastError)
		})
		a.invalidate()
		return err
	}
	a.work.push(func() {
		if a.playGen.Load() != gen {
			return
		}
		a.playing = true
		if resolved.FreeTrial {
			a.toast("仅可试听", song.Name+"：当前账户只能播放试听片段，未写入缓存", ui.ToastWarning)
		}
		a.noteCachedQuality(song.ID, resolved.Quality)
		a.updateMPRIS()
		a.updateTray()
	})
	a.invalidate()
	return nil
}

func (a *App) playCachedGen(gen uint64, song ncm.Song, path string, quality ncm.Quality, at time.Duration, volume float64, muted bool) bool {
	if a.playGen.Load() != gen {
		return true
	}
	a.engine.SetVolume(volume)
	a.engine.SetMuted(muted)
	err := a.engine.PlayAt(context.Background(), player.Source{
		SongID:   song.ID,
		Title:    song.Name,
		File:     path,
		Duration: song.Duration,
	}, at)
	if err != nil {
		return false
	}
	a.work.push(func() {
		if a.playGen.Load() != gen {
			return
		}
		a.playing = true
		a.noteCachedQuality(song.ID, quality)
		a.updateMPRIS()
		a.updateTray()
	})
	a.invalidate()
	return true
}

func (a *App) notifyCachedBroken(song ncm.Song) {
	a.work.push(func() {
		a.toast("缓存文件已损坏", song.Name+"：改为在线播放", ui.ToastWarning)
	})
	a.invalidate()
}

// cacheableStream reports whether the resolved stream is a full track rather
// than a 30-second VIP preview or truncated response.
func cacheableStream(song ncm.Song, resolved ncm.SongURL) bool {
	if resolved.FreeTrial {
		return false
	}
	if song.Duration < 30*time.Second {
		return false
	}
	if resolved.Size > 0 {
		minimum := int64(song.Duration.Seconds() * 64_000 / 8)
		if resolved.Size < minimum/2 {
			return false
		}
	}
	return true
}

// songQuality is the tier to request: the per-song pick if set, otherwise
// QualityMaster so the server answers with the highest tier available.
func (a *App) songQuality(song ncm.Song) ncm.Quality {
	if song.SelectedSet {
		return song.Selected
	}
	return ncm.QualityMaster
}

// isBestTier reports whether played qualifies as the highest tier worth caching.
func (a *App) isBestTier(song ncm.Song, played ncm.Quality) bool {
	if !song.SelectedSet {
		return true
	}
	return played >= song.Selected
}

// shouldCacheResolved decides whether a stream opened under an explicit quality
// pick should be written to cache: yes if it matches or beats the known ceiling
// and nothing better is already on disk, no if the user intentionally stepped
// down to a lower tier.
func (a *App) shouldCacheResolved(song ncm.Song, played ncm.Quality) bool {
	if !a.isBestTier(song, played) {
		return false
	}
	if _, bestCached, ok := a.cache.LookupBest(song.ID); ok && bestCached >= played {
		return false
	}
	if a.library != nil {
		if entry, ok := a.library.Get(song.ID); ok && entry.Best > played {
			return false
		}
	}
	return true
}

func (a *App) cacheIsAlreadyBest(songID int64, cachedQuality ncm.Quality, vipType int) bool {
	if cachedQuality >= ncm.QualityMaster {
		return true
	}
	if a.library == nil {
		return false
	}
	return a.library.IsBestCached(songID, cachedQuality, vipType)
}

func (a *App) recordCachedAsBest(songID int64, path string, quality ncm.Quality, vipType int) {
	if a.library == nil {
		return
	}
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	_ = a.library.Put(store.CacheEntry{
		SongID:    songID,
		Quality:   quality,
		File:      filepath.Base(path),
		Size:      size,
		SavedAt:   time.Now(),
		Best:      quality,
		ProbedVIP: vipType,
	})
}

// noteCachedQuality updates the playing tier shown in the transport bar.
func (a *App) noteCachedQuality(songID int64, quality ncm.Quality) {
	for i := range a.queue {
		if a.queue[i].ID != songID {
			continue
		}
		a.queue[i].Actual = quality
		a.queue[i].ActualSet = true
		if !a.queue[i].SelectedSet {
			a.queue[i].Selected = quality
		}
	}
}

// playingQuality returns the display name of the currently playing tier.
func (a *App) playingQuality(song ncm.Song) string {
	if a.queueAt < 0 || a.queueAt >= len(a.queue) || a.queue[a.queueAt].ID != song.ID {
		return ""
	}
	cur := a.queue[a.queueAt]
	if cur.ActualSet {
		return cur.Actual.String()
	}
	return ""
}

// bestQualityFor returns the highest tier known to be reachable for song,
// preserving the ceiling even when the user temporarily switches to a lower tier.
// Until the song has been probed at the current VIP tier, all tiers up to
// QualityMaster are offered so a VIP user is never locked into an older low-tier
// cache file.
func (a *App) bestQualityFor(song ncm.Song) ncm.Quality {
	vipType := a.currentVIP()
	best := ncm.QualityStandard
	haveCeiling := false
	if a.library != nil {
		if entry, ok := a.library.Get(song.ID); ok && entry.ProbedVIP >= vipType && (entry.File != "" || entry.Best > 0 || entry.ProbedVIP > 0) {
			best = entry.Best
			haveCeiling = true
		}
	}
	if !haveCeiling {
		return ncm.QualityMaster
	}
	if cached, ok := a.cached.BestQuality(song.ID); ok && cached > best {
		best = cached
	}
	if song.ActualSet && song.Actual > best {
		best = song.Actual
	}
	return best
}

// chooseQuality switches the current song to quality at the current position.
func (a *App) chooseQuality(song ncm.Song, quality ncm.Quality) {
	for i := range a.queue {
		if a.queue[i].ID != song.ID {
			continue
		}
		a.queue[i].Selected = quality
		a.queue[i].SelectedSet = true
	}
	at := a.currentPlaybackPosition()
	if index := a.queueAtOf(song.ID); index >= 0 {
		a.playAtResuming(index, at)
	}
}

// chooseAutoQuality clears any per-song quality override and switches back to
// automatic highest-quality playback at the current position.
func (a *App) chooseAutoQuality(song ncm.Song) {
	for i := range a.queue {
		if a.queue[i].ID != song.ID {
			continue
		}
		a.queue[i].SelectedSet = false
	}
	at := a.currentPlaybackPosition()
	if index := a.queueAtOf(song.ID); index >= 0 {
		a.playAtResuming(index, at)
	}
}

func (a *App) currentPlaybackPosition() time.Duration {
	if a.seekPending {
		return a.seekTarget
	}
	return a.engine.Snapshot().Position
}

func (a *App) queueAtOf(id int64) int {
	for i := range a.queue {
		if a.queue[i].ID == id {
			return i
		}
	}
	return -1
}

// fileWasCached records a newly promoted cache file in library.json.
func (a *App) fileWasCached(path string) {
	if a.library == nil || path == "" {
		return
	}
	songID, quality, ok := store.ParseName(filepath.Base(path))
	if !ok {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	vipType := a.currentVIP()
	// Never downgrade the recorded primary file quality if a better file exists.
	if prev, exists := a.library.Get(songID); exists && prev.File != "" && prev.Quality > quality {
		a.cached.SetDirty()
		return
	}
	entry := store.CacheEntry{
		SongID:    songID,
		Quality:   quality,
		File:      filepath.Base(path),
		Size:      info.Size(),
		SavedAt:   time.Now(),
		Best:      quality,
		ProbedVIP: vipType,
	}
	_ = a.library.Put(entry)
	a.work.push(func() {
		a.cached.SetDirty()
		song := ncm.Song{ID: songID, Name: entry.File}
		for i := range a.queue {
			if a.queue[i].ID == songID {
				song = a.queue[i]
				break
			}
		}
		a.upgradeCache(song, quality)
		a.invalidate()
	})
}

func (a *App) noteBestTier(song ncm.Song, played ncm.Quality) {
	a.noteBestTierForVIP(song, played, a.currentVIP())
}

func (a *App) noteBestTierForVIP(song ncm.Song, played ncm.Quality, vipType int) {
	if a.library == nil {
		return
	}
	_ = a.library.NoteBestForVIP(song.ID, played, vipType)
}

// ensureHighestCachedAsync guarantees that when the user plays a song at a
// lower quality while online (for example, after manually selecting 极高 or
// playing an older 128k cache file), the highest available VIP tier is still
// fetched and cached in the background.
func (a *App) ensureHighestCachedAsync(song ncm.Song, vipType int) {
	if a.library == nil || a.noRemote {
		return
	}
	_, bestCached, hasCache := a.cache.LookupBest(song.ID)
	if hasCache && a.cacheIsAlreadyBest(song.ID, bestCached, vipType) {
		return
	}
	a.startUpgradeDownload(song, ncm.QualityMaster, bestCached, hasCache, vipType)
}

// upgradeCache fetches a known higher tier in the background when disk holds a
// lower tier.
func (a *App) upgradeCache(song ncm.Song, have ncm.Quality) {
	if a.library == nil {
		return
	}
	best, wanted := a.library.WantsBetter(song.ID)
	if !wanted {
		return
	}
	a.startUpgradeDownload(song, best, have, true, a.currentVIP())
}

func (a *App) startUpgradeDownload(song ncm.Song, askQuality, currentCached ncm.Quality, hasCached bool, vipType int) {
	a.upgradeMu.Lock()
	if a.upgrading[song.ID] {
		a.upgradeMu.Unlock()
		return
	}
	a.upgrading[song.ID] = true
	a.upgradeMu.Unlock()

	resolve := a.resolveURL
	if resolve == nil {
		resolve = a.client.SongURLs
	}
	httpClient := a.client.HTTPClient()
	go func() {
		defer func() {
			a.upgradeMu.Lock()
			delete(a.upgrading, song.ID)
			a.upgradeMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		urls, err := resolve(ctx, []int64{song.ID}, askQuality)
		if err != nil || len(urls) == 0 || urls[0].URL == "" {
			return
		}
		resolved := urls[0]
		if resolved.FreeTrial || !cacheableStream(song, resolved) {
			return
		}
		if hasCached && resolved.Quality <= currentCached {
			if path, ok := a.cache.LookupExact(song.ID, currentCached); ok {
				a.recordCachedAsBest(song.ID, path, currentCached, vipType)
			}
			return
		}
		source := player.Source{
			SongID:    song.ID,
			Title:     song.Name,
			Stream:    resolved.URL,
			CachePath: a.cache.Path(song.ID, resolved.Quality),
			Header:    http.Header{"Referer": {"https://music.163.com"}, "User-Agent": {userAgentHeader}},
		}
		if err := player.FetchToCache(ctx, httpClient, source); err != nil {
			return
		}
		a.work.push(func() {
			if a.library == nil {
				return
			}
			var size int64
			if info, statErr := os.Stat(source.CachePath); statErr == nil {
				size = info.Size()
			}
			_ = a.library.Put(store.CacheEntry{
				SongID:    song.ID,
				Quality:   resolved.Quality,
				File:      store.Name(song.ID, resolved.Quality),
				Size:      size,
				SavedAt:   time.Now(),
				Best:      resolved.Quality,
				ProbedVIP: vipType,
			})
			a.cached.SetDirty()
			a.toast("已缓存最高音质", song.Name+"："+resolved.Quality.String(), ui.ToastSuccess)
		})
		a.invalidate()
	}()
}

// ----- transport & queue operations -----

const userAgentHeader = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func (a *App) current() (ncm.Song, bool) {
	if a.queueAt < 0 || a.queueAt >= len(a.queue) {
		return ncm.Song{}, false
	}
	return a.queue[a.queueAt], true
}

func (a *App) togglePlay() {
	switch a.engine.Snapshot().Status {
	case player.StatusPlaying, player.StatusBuffering:
		a.cancelPendingPlay()
		a.engine.Pause()
		a.playing = false
	case player.StatusPaused:
		_ = a.engine.Resume(context.Background())
		a.playing = true
	default:
		if len(a.queue) > 0 {
			at := a.resumeAt
			a.resumeAt = 0
			a.playAtResuming(a.queueAt, at)
		}
	}
	a.updateMPRIS()
	a.updateTray()
}

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
			a.cancelPendingPlay()
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

func (a *App) extendHeartbeat() bool {
	song, ok := a.current()
	if !ok {
		return false
	}
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

func (a *App) next()     { a.advance(1, false) }
func (a *App) previous() { a.advance(-1, false) }

func (a *App) followPlayingSong(song ncm.Song) {
	top := a.top()
	if top.kind != routeLyrics || top.id == song.ID {
		return
	}
	a.nav[len(a.nav)-1] = route{kind: routeLyrics, id: song.ID, title: song.Name}
	a.lastLyricSong = 0
	a.lastLyricLine = -1
	a.loadLyric(song.ID)
	a.invalidate()
}

func (a *App) seekTo(at time.Duration) {
	_ = a.engine.Seek(at)
	a.updateMPRIS()
}

func (a *App) playable(songs []ncm.Song) []ncm.Song {
	if !a.config.IgnoreUnplayable {
		return songs
	}
	kept := make([]ncm.Song, 0, len(songs))
	for _, song := range songs {
		if song.Copyright().Playable() || a.cached.Has(song.ID) {
			kept = append(kept, song)
		}
	}
	return kept
}

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

func (a *App) playSongs(songs []ncm.Song, index int) {
	if len(songs) == 0 {
		return
	}
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
	a.playQueue(kept, start)
	a.invalidate()
}

func (a *App) removeFromQueue(index int) {
	if index < 0 || index >= len(a.queue) {
		return
	}
	current := a.queueAt
	a.queue = append(a.queue[:index], a.queue[index+1:]...)
	switch {
	case len(a.queue) == 0:
		a.queueAt = 0
		a.cancelPendingPlay()
		a.engine.Stop()
		a.playing = false
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

func (a *App) clearQueue() {
	a.queue = nil
	a.queueAt = 0
	a.cancelPendingPlay()
	a.engine.Stop()
	a.playing = false
	a.persistQueue()
	a.invalidate()
}

func (a *App) setVolume(volume float64) {
	a.config.Volume = volume
	a.config.Muted = volume == 0 && a.config.Muted
	a.engine.SetVolume(volume)
	a.saveConfig()
	a.invalidate()
}

func (a *App) toggleMute() {
	a.config.Muted = !a.config.Muted
	a.engine.SetMuted(a.config.Muted)
	a.saveConfig()
	a.updateMPRIS()
	a.invalidate()
}

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

func (a *App) persistQueue() {
	a.persistQueueAt(a.engine.Snapshot().Position)
}

func (a *App) persistQueueAt(pos time.Duration) {
	queue := store.Queue{
		Songs:      a.queue,
		Index:      a.queueAt,
		Position:   pos,
		WasPlaying: a.playing,
	}
	_ = a.queues.Save(queue)
}

func (a *App) saveConfig() { _ = a.settings.Save(a.config) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// progressPercent converts position/duration to a slider percentage in [0, 100]
// snapped to the slider's 0.1 step so settleSlider never sees floating drift.
func progressPercent(position, duration time.Duration) float32 {
	if duration <= 0 || position <= 0 {
		return 0
	}
	if position >= duration {
		return 100
	}
	pct := position.Seconds() / duration.Seconds() * 100
	snapped := math.Round(pct*10) / 10
	if snapped < 0 {
		return 0
	}
	if snapped > 100 {
		return 100
	}
	return float32(snapped)
}
