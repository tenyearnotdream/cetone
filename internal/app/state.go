// Package app is the application: its state, the pages it shows and the wiring
// between the API client, the audio engine and the window.
//
// A page function here declares only what is on screen; anything that takes
// time (a request, a decode, a cover download) runs on a worker and publishes a
// closure that the window thread runs at the start of the next paint. That is
// the one rule that keeps the model single-threaded.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/mpris"
	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

// App is everything the window shows and everything it remembers.
type App struct {
	paths    store.Paths
	settings *store.Settings
	sessions *store.Sessions
	queues   *store.QueueStore
	cache    *store.Cache
	cached   *store.CacheIndex
	library  *store.Library
	lyrics   *store.LyricStore
	covers   *store.CoverStore

	client *ncm.Client
	engine *player.Engine

	// resolveURL defaults to client.SongURLs and can be overridden in tests.
	resolveURL func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error)

	config  store.Config
	user    ncm.UserInfo
	userVIP atomic.Int64 // mirrors user.VIPType for safe reads from playback workers

	// inv wakes the window when a worker publishes.
	inv atomic.Pointer[app.Invalidator]

	// work is the queue of closures published by workers for the window thread.
	work *workQueue

	// nav is the page stack; the last entry is on screen.
	nav []route

	// data holds loaded page payloads.
	data pageData

	// Cover art cache and in-flight tracking.
	bitmaps      map[coverKey]gfx.Bitmap
	coverMisses  map[coverKey]bool
	coverLoading map[coverKey]bool

	// Play queue and playback orchestration.
	queue    []ncm.Song
	queueAt  int
	playing  bool
	likedSet map[int64]bool

	// playGen monotonically numbers playback starts so stale async URL
	// resolutions from earlier clicks never overwrite a newer selection.
	playGen      atomic.Uint64
	playCancelMu sync.Mutex
	playCancel   context.CancelFunc

	// Background highest-quality cache upgrades in flight, keyed by song ID.
	upgradeMu sync.Mutex
	upgrading map[int64]bool

	// View state.
	search        searchState
	queuePanel    bool
	login         loginState
	toaster       ui.Toaster
	volumeDrag    ui.SliderState
	seekDrag      ui.SliderState
	seekTarget    time.Duration
	seekPending   bool
	searchFocused bool
	queueList     ui.ListState
	lyricList     ui.ListState
	lastLyricSong int64
	lastLyricLine int

	// Per-row action menu and player-bar quality menu state.
	rowMenuOpen     bool
	rowMenuSongID   int64
	qualityMenuOpen bool

	// Settings form buffers, initialized once from config so clearing them works.
	proxyText     string
	cacheDirsText string

	// noRemote suppresses remote fetches during paints when set via env.
	noRemote bool

	// 心动模式 (heartbeat radio).
	heartbeat         bool
	heartbeatPlaylist int64

	// resumeAt is the position a restored queue continues from on first Play.
	resumeAt time.Duration

	// Desktop integration.
	window         *app.Window
	application    *app.Application
	mprisServer    mprisServer
	lastMPRISAt    time.Time
	quitRequested  bool
	stopAppearance func()

	// Visual state.
	ocean oceanTexture
	theme gfx.Theme
	brand struct {
		Decoded bool
		Dark    bool
		Bitmap  gfx.Bitmap
	}

	// System tray state.
	tray            *app.StatusItem
	trayAvailable   bool
	trayUnavailable bool
	trayErr         error

	toastSeq int
	now      time.Duration
}

// mprisServer is the interface the app needs of the media-controls server.
type mprisServer interface {
	Update(track mpris.Track, status mpris.Status, position mpris.Duration, volume float64)
	PublishPosition(position mpris.Duration)
	Close()
}

type coverKey struct {
	url    string
	width  int
	height int
}

type route struct {
	kind     routeKind
	id       int64
	title    string
	playlist ncm.Playlist
	album    ncm.Album
	artist   ncm.Artist
	keyword  string
	search   ncm.SearchKind
}

type routeKind int

const (
	routeDiscover routeKind = iota
	routeToplist
	routePlaylist
	routeAlbum
	routeArtist
	routeSearch
	routeMine
	routeSettings
	routeLyrics
	routeQueue
)

// New builds the application from its directories, loading settings, session,
// queue and cache index.
func New(paths store.Paths) (*App, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	a := &App{
		paths:         paths,
		settings:      store.NewSettings(paths),
		sessions:      store.NewSessions(paths),
		queues:        store.NewQueueStore(paths),
		lyrics:        store.NewLyricStore(paths),
		covers:        store.NewCoverStore(paths),
		work:          newWorkQueue(),
		bitmaps:       make(map[coverKey]gfx.Bitmap),
		coverMisses:   make(map[coverKey]bool),
		coverLoading:  make(map[coverKey]bool),
		likedSet:      make(map[int64]bool),
		upgrading:     make(map[int64]bool),
		lastLyricLine: -1,
	}
	config, err := a.settings.Load()
	if err != nil {
		a.notifyLater("设置文件损坏", "已使用默认设置："+err.Error())
	}
	a.config = config
	a.proxyText = config.Proxy
	a.cacheDirsText = joinLines(config.CacheDirs)

	a.cache = store.NewCache(paths, config.CacheDirs)
	a.cached = store.NewCacheIndex(a.cache)
	a.library = store.NewLibrary(paths)
	_ = a.library.Load()

	client := ncm.NewClient()
	if err := client.SetProxy(config.Proxy); err != nil {
		a.notifyLater("代理无效", err.Error())
	}
	a.client = client
	if session, ok, err := a.sessions.Load(); err == nil && ok {
		client.LoadCookies(session.Cookies)
		a.setUser(session.User)
	}
	if queue, ok, err := a.queues.Load(); err == nil && ok {
		a.queue = queue.Songs
		a.queueAt = queue.Index
	}

	a.resolveURL = a.client.SongURLs
	a.engine = player.NewEngine()
	a.engine.HTTP = client.HTTPClient()
	a.engine.SetVolume(config.Volume)
	a.engine.SetMuted(config.Muted)
	a.inv.Store(&app.Invalidator{})
	a.engine.OnChange = a.invalidate
	a.engine.OnFinish = func() {
		a.work.push(func() { a.finishedTrack() })
		a.invalidate()
	}
	a.engine.OnCacheWritten = func(path string) {
		a.cached.SetDirty()
		a.fileWasCached(path)
	}
	a.volumeDrag = ui.SliderState{Value: float32(config.Volume * 100)}
	a.initLists()
	a.initQueueList()

	a.noRemote = os.Getenv("NCMVIVA_NOREMOTE") == "1"
	a.nav = []route{{kind: routeDiscover, title: "发现"}}
	return a, nil
}

func (a *App) setUser(user ncm.UserInfo) {
	a.user = user
	a.userVIP.Store(int64(user.VIPType))
}

func (a *App) currentVIP() int {
	return int(a.userVIP.Load())
}

// ----- the window -----

// Page builds the page and its window config with Invalidator and OnWake wired.
func (a *App) Page(base app.Config) (*ui.Page, app.Config) {
	theme, follow := a.themeConfig()
	page := ui.NewPage(ui.PageConfig{View: a.page, Theme: theme, FollowSystemTheme: follow})
	config := page.Config(base)
	config.OnWake = a.sync
	a.inv.Store(config.Invalidator)
	return page, config
}

func (a *App) invalidate() {
	if inv := a.inv.Load(); inv != nil {
		inv.Invalidate()
	}
}

// SetInvalidator hands the window's waker to the application.
func (a *App) SetInvalidator(inv *app.Invalidator) { a.inv.Store(inv) }

func (a *App) themeConfig() (*gfx.Theme, bool) {
	a.applyTheme()
	return &a.theme, false
}

func (a *App) applyTheme() {
	dark := false
	switch a.config.Theme {
	case "dark":
		dark = true
	case "light":
		dark = false
	default:
		dark = app.SystemAppearance() == app.AppearanceDark
	}
	if dark {
		a.theme = a.decorate(gfx.DarkTheme(), true)
		return
	}
	a.theme = a.decorate(gfx.LightTheme(), false)
}

func (a *App) watchAppearance() {
	a.stopAppearance = app.WatchSystemAppearance(func() {
		if a.config.Theme != "system" && a.config.Theme != "" {
			return
		}
		a.applyTheme()
		a.invalidate()
	})
}

const (
	colorCyan  = 0x18D8F0
	colorBlue  = 0x0090F0
	colorDeep  = 0x0048A8
	colorAbyss = 0x0B1A26
	colorFoam  = 0xF2F9FF
	colorShore = 0x06243B
)

func (a *App) decorate(theme gfx.Theme, dark bool) gfx.Theme {
	if dark {
		theme.Background = gfx.RGB24(0x07131C)
		theme.Foreground = gfx.RGB24(0xE6F4FF)
		theme.Card = gfx.RGB24(0x0B1A26)
		theme.CardForeground = gfx.RGB24(0xE6F4FF)
		theme.CardElevated = gfx.RGB24(0x102432)
		theme.Popover = gfx.RGB24(0x0E2030)
		theme.PopoverForeground = gfx.RGB24(0xE6F4FF)
		theme.Muted = gfx.RGB24(0x13293A)
		theme.MutedForeground = gfx.RGB24(0x8FB3CB)
		theme.Border = gfx.RGB24(0x1B3A50)
		theme.Hover = gfx.RGB24(0x122A3B)
		theme.Input = gfx.RGB24(0x1B3A50)
		theme.Primary = gfx.RGB24(colorBlue)
		theme.PrimaryForeground = gfx.RGB24(0x04121F)
		theme.Secondary = gfx.RGB24(0x143247)
		theme.SecondaryForeground = gfx.RGB24(0xD8ECFA)
		theme.Accent = gfx.RGB24(0x123044)
		theme.AccentForeground = gfx.RGB24(0xD8ECFA)
		theme.Selection = gfx.RGB24(0x10425F)
		theme.SelectionForeground = gfx.RGB24(0xEAF7FF)
		theme.Ring = gfx.RGB24(colorCyan)
		theme.Success = gfx.RGB24(0x2FD4A0)
		theme.Danger = gfx.RGB24(0xFF6B7A)
		return theme
	}
	theme.Background = gfx.RGB24(0xFAFDFF)
	theme.Foreground = gfx.RGB24(0x081C2B)
	theme.Card = gfx.RGB24(0xFFFFFF)
	theme.CardForeground = gfx.RGB24(0x081C2B)
	theme.CardElevated = gfx.RGB24(0xF1F8FD)
	theme.Popover = gfx.RGB24(0xFFFFFF)
	theme.PopoverForeground = gfx.RGB24(0x081C2B)
	theme.Muted = gfx.RGB24(0xEAF4FB)
	theme.MutedForeground = gfx.RGB24(0x5B7C93)
	theme.Border = gfx.RGB24(0xD5E7F3)
	theme.Hover = gfx.RGB24(0xF0F8FD)
	theme.Input = gfx.RGB24(0xD5E7F3)
	theme.Primary = gfx.RGB24(colorDeep)
	theme.PrimaryForeground = gfx.RGB24(colorFoam)
	theme.Secondary = gfx.RGB24(0xE8F4FC)
	theme.SecondaryForeground = gfx.RGB24(colorShore)
	theme.Accent = gfx.RGB24(0xE4F3FD)
	theme.AccentForeground = gfx.RGB24(colorShore)
	theme.Selection = gfx.RGB24(0xCCE8F9)
	theme.SelectionForeground = gfx.RGB24(colorShore)
	theme.Ring = gfx.RGB24(colorBlue)
	theme.Success = gfx.RGB24(0x0F9D6E)
	theme.Danger = gfx.RGB24(0xD92B45)
	return theme
}

func (a *App) sync() { a.work.drain() }

// ----- background work -----

type workQueue struct {
	mu    sync.Mutex
	items []func()
}

func newWorkQueue() *workQueue { return &workQueue{} }

func (w *workQueue) push(fn func()) {
	w.mu.Lock()
	w.items = append(w.items, fn)
	w.mu.Unlock()
}

func (w *workQueue) quiet() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.items) == 0
}

func (w *workQueue) drain() {
	w.mu.Lock()
	items := w.items
	w.items = nil
	w.mu.Unlock()
	for _, fn := range items {
		fn()
	}
}

func (a *App) background(fn func() error, done func(error)) {
	go func() {
		err := fn()
		a.work.push(func() {
			if done != nil {
				done(err)
			}
		})
		a.invalidate()
	}()
}

func (a *App) notifyLater(title, body string) {
	a.work.push(func() { a.toast(title, body, ui.ToastDefault) })
}

func (a *App) toast(title, body string, kind ui.ToastKind) {
	a.toaster.Show(title, body, kind)
	a.toastSeq++
	a.invalidate()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ncm.ErrNotLoggedIn) {
		return "需要登录"
	}
	var api *ncm.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case 301:
			return "需要登录后操作"
		case 400:
			return "请求被拒绝（400）"
		case 403:
			return "没有权限（403）"
		}
		if api.Msg != "" {
			return api.Msg
		}
		return fmt.Sprintf("接口返回 %d", api.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "网络超时"
	}
	return err.Error()
}

// ----- navigation -----

func (a *App) top() route {
	if len(a.nav) == 0 {
		return route{kind: routeDiscover, title: "发现"}
	}
	return a.nav[len(a.nav)-1]
}

func (a *App) push(r route) {
	if len(a.nav) > 0 {
		if current := a.nav[len(a.nav)-1]; current.kind == r.kind && current.id == r.id && r.kind != routeSearch {
			return
		}
	}
	a.nav = append(a.nav, r)
	a.load(r)
	a.invalidate()
}

func (a *App) pop() {
	if len(a.nav) > 1 {
		a.nav = a.nav[:len(a.nav)-1]
		a.invalidate()
	}
}

func (a *App) section(kind routeKind) {
	titles := map[routeKind]string{
		routeDiscover: "发现",
		routeToplist:  "排行榜",
		routeSearch:   "搜索",
		routeMine:     "我的音乐",
		routeSettings: "设置",
	}
	a.nav = []route{{kind: kind, title: titles[kind]}}
	a.load(a.nav[0])
	a.invalidate()
}

// ----- session -----

func (a *App) persistSession() {
	if a.user.UserID == 0 {
		return
	}
	_ = a.sessions.Save(storeSessionFrom(a.client, a.user))
}

func (a *App) refreshSession() {
	if a.user.UserID == 0 {
		return
	}
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		user, err := a.client.LoginStatus(ctx)
		a.work.push(func() {
			switch {
			case err == nil:
				a.setUser(user)
				a.persistSession()
			case errors.Is(err, ncm.ErrNotLoggedIn):
				a.setUser(ncm.UserInfo{})
				a.likedSet = make(map[int64]bool)
				a.data.mine = mineData{}
				_ = a.sessions.Clear()
				a.toast("登录已失效", "请重新扫码登录", ui.ToastWarning)
				a.loadDiscover()
			}
		})
		a.invalidate()
		return nil
	}, nil)
}
