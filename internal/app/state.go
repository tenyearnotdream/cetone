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
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"ncmviva/internal/mpris"
	"ncmviva/internal/ncm"
	"ncmviva/internal/player"
	"ncmviva/internal/store"
)

// App is everything the window shows and everything it remembers.
type App struct {
	paths    store.Paths
	settings *store.Settings
	sessions *store.Sessions
	queues   *store.QueueStore
	cache    *store.Cache
	lyrics   *store.LyricStore
	covers   *store.CoverStore

	client *ncm.Client
	engine *player.Engine

	config store.Config
	user   ncm.UserInfo

	// inv wakes the window when a worker publishes. It is set by Run and by the
	// page's Config.
	inv *app.Invalidator

	// work is the queue of closures a worker published for the window thread.
	work *workQueue

	// nav is the page stack; the last entry is what is on screen.
	nav []route

	// data is what each page loaded, keyed by the route it belongs to.
	data pageData

	// bitmaps caches decoded album art by URL and size.
	bitmaps map[coverKey]gfx.Bitmap
	// cached is the set of songs with a cache file, rebuilt when one arrives so
	// a list of a thousand rows does not stat the disk per paint.
	cached *cacheIndex
	// coverMisses remembers art that failed, so a broken URL is not refetched
	// on every paint.
	coverMisses map[coverKey]bool
	// coverLoading marks art a worker is fetching, so a paint asks once.
	coverLoading map[coverKey]bool

	// queue is the play queue and its cursor.
	queue    []ncm.Song
	queueAt  int
	playing  bool
	likedSet map[int64]bool

	// view state that is neither data nor navigation.
	search        searchState
	queuePanel    bool
	login         loginState
	toaster       ui.Toaster
	volumeDrag    ui.SliderState
	seekDrag      ui.SliderState
	rowMenuOpen   bool
	searchFocused bool
	// queueList and lyricList keep the lists' scroll and choice between paints.
	queueList ui.ListState
	lyricList ui.ListState

	// form buffers for the settings page. The fields themselves are declared
	// where they are used; only the values they bind live here.
	proxyText     string
	cacheDirsText string

	// noRemote suppresses background I/O during a paint; it exists only for the
	// startup investigation and is set from NCMVIVA_NOREMOTE.
	noRemote bool

	// heartbeat is 心动模式: the queue extends itself from the recommender.
	heartbeat bool
	// heartbeatPlaylist is the playlist the queue was started from, which seeds
	// the recommender.
	heartbeatPlaylist int64

	// resumeAt is the position a restored queue should continue from.
	resumeAt time.Duration
	// window, application and the MPRIS server are the desktop integration.
	window        *app.Window
	application   *app.Application
	mprisServer   mprisServer
	quitRequested bool
	// toastSeq counts the toasts this run showed, for tests.
	toastSeq int
	// now is the animation clock of the last paint, kept for the player bar's
	// position readout.
	now time.Duration
}

// mprisServer is the interface the app needs of the media-controls server.
type mprisServer interface {
	Update(track mpris.Track, status mpris.Status, position mpris.Duration, volume float64)
	PublishPosition(position mpris.Duration)
	Close()
}

// coverKey identifies one decoded cover.
type coverKey struct {
	url    string
	width  int
	height int
}

// route is one page in the navigation stack.
type route struct {
	kind  routeKind
	id    int64
	title string
	// kind-specific payload, already loaded or to be loaded.
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

// New builds the application from its directories. It reads the settings, the
// session and the queue; a broken file is reported as a toast rather than
// stopping the window.
func New(paths store.Paths) (*App, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	a := &App{
		paths:        paths,
		settings:     store.NewSettings(paths),
		sessions:     store.NewSessions(paths),
		queues:       store.NewQueueStore(paths),
		lyrics:       store.NewLyricStore(paths),
		covers:       store.NewCoverStore(paths),
		work:         newWorkQueue(),
		bitmaps:      make(map[coverKey]gfx.Bitmap),
		coverMisses:  make(map[coverKey]bool),
		coverLoading: make(map[coverKey]bool),
		likedSet:     make(map[int64]bool),
	}
	config, err := a.settings.Load()
	if err != nil {
		a.notifyLater("设置文件损坏", "已使用默认设置："+err.Error())
	}
	a.config = config
	a.cache = store.NewCache(paths, config.CacheDirs)

	client := ncm.NewClient()
	if err := client.SetProxy(config.Proxy); err != nil {
		a.notifyLater("代理无效", err.Error())
	}
	a.client = client
	if session, ok, err := a.sessions.Load(); err == nil && ok {
		client.LoadCookies(session.Cookies)
		a.user = session.User
	}
	if queue, ok, err := a.queues.Load(); err == nil && ok {
		a.queue = queue.Songs
		a.queueAt = queue.Index
	}

	a.cached = newCacheIndex(a.cache)
	a.engine = player.NewEngine()
	a.engine.SetVolume(config.Volume)
	a.engine.SetMuted(config.Muted)
	a.engine.OnChange = a.invalidate
	a.engine.OnFinish = func() {
		a.work.push(func() { a.finishedTrack() })
		a.invalidate()
	}
	a.engine.OnCacheWritten = func() { a.cached.setDirty() }
	a.volumeDrag = ui.SliderState{Value: float32(config.Volume * 100)}

	a.noRemote = os.Getenv("NCMVIVA_NOREMOTE") == "1"
	a.nav = []route{{kind: routeDiscover, title: "发现"}}
	return a, nil
}

// ----- the window -----

// Page builds the page and its window config, for a test that drives it with
// no window. The returned config already carries the Invalidator workers wake,
// and the application holds that same value: replacing config.Invalidator with
// a fresh one afterwards would leave the application waking an invalidator no
// window listens to, and a worker's result would never be painted.
func (a *App) Page(base app.Config) (*ui.Page, app.Config) {
	theme, follow := a.themeConfig()
	page := ui.NewPage(ui.PageConfig{View: a.page, Theme: theme, FollowSystemTheme: follow})
	config := page.Config(base)
	a.inv = config.Invalidator
	return page, config
}

// pageConfig is the config the page was built with; a test uses Page.
func (a *App) invalidate() {
	if a.inv != nil {
		a.inv.Invalidate()
	}
}

// SetInvalidator is how Run hands the window's waker to the application.
func (a *App) SetInvalidator(inv *app.Invalidator) { a.inv = inv }

// themeConfig is the page's theme configuration: an explicit theme the user
// chose, or none at all with the system appearance followed, which is what
// "自适应皮肤" means.
func (a *App) themeConfig() (*gfx.Theme, bool) {
	switch a.config.Theme {
	case "dark":
		theme := gfx.DarkTheme()
		return &theme, false
	case "light":
		theme := gfx.LightTheme()
		return &theme, false
	}
	return nil, true
}

// sync runs the closures workers published. The page calls it before declaring
// anything, and a test calls it while waiting for data.
func (a *App) sync() { a.work.drain() }

// ----- background work -----

// workQueue hands closures from workers to the window thread.
type workQueue struct {
	mu      sync.Mutex
	items   []func()
	pending int
}

func newWorkQueue() *workQueue { return &workQueue{} }

func (w *workQueue) push(fn func()) {
	w.mu.Lock()
	w.items = append(w.items, fn)
	w.mu.Unlock()
}

// quiet reports whether no closure is waiting.
func (w *workQueue) quiet() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.items) == 0
}

// drain takes the published closures and runs them on the calling (window)
// thread.
func (w *workQueue) drain() {
	w.mu.Lock()
	items := w.items
	w.items = nil
	w.mu.Unlock()
	for _, fn := range items {
		fn()
	}
}

// background runs fn off the window thread and publishes its result for the
// window thread through apply. A panic in fn becomes an error result.
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

// notifyLater shows a toast on the next paint, from any goroutine.
func (a *App) notifyLater(title, body string) {
	a.work.push(func() { a.toast(title, body, ui.ToastDefault) })
}

// toast shows a message in the window's corner. It asks for the repaint itself:
// the Toaster only queues a toast, and a caller that showed one from a key
// handler or a worker may not be followed by a paint for any other reason.
func (a *App) toast(title, body string, kind ui.ToastKind) {
	a.toaster.Show(title, body, kind)
	a.toastSeq++
	a.invalidate()
}

// errText is what a failed request says to a user.
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

// top is the page on screen.
func (a *App) top() route {
	if len(a.nav) == 0 {
		return route{kind: routeDiscover, title: "发现"}
	}
	return a.nav[len(a.nav)-1]
}

// push opens a page, replacing the top one when it is the same page.
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

// pop goes back one page, keeping the first.
func (a *App) pop() {
	if len(a.nav) > 1 {
		a.nav = a.nav[:len(a.nav)-1]
		a.invalidate()
	}
}

// home returns to the first page.
func (a *App) home() {
	if len(a.nav) > 1 {
		a.nav = a.nav[:1]
		a.invalidate()
	}
}

// section switches between the top-level pages, clearing the stack.
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
