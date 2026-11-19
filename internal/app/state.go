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
	lyrics   *store.LyricStore
	covers   *store.CoverStore

	client *ncm.Client
	engine *player.Engine

	// resolveURL is the client's URL endpoint. It is a field so a test can serve
	// a stream without the API, and so the one call that decides whether a song
	// is a full track or a trial has a single place to be exercised from.
	resolveURL func(ctx context.Context, ids []int64, quality ncm.Quality) ([]ncm.SongURL, error)

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
	// window, application, the status item and the MPRIS server are the desktop
	// integration.
	window        *app.Window
	application   *app.Application
	mprisServer   mprisServer
	quitRequested bool
	// stopAppearance ends the desktop appearance watch, when one is running.
	stopAppearance func()

	// theme is what the window paints with. It is a field, not a value built per
	// call, because the page keeps a pointer to it: reassigning it is how a theme
	// change reaches the screen.
	theme gfx.Theme

	// brand is the application's icon, decoded once for the page's header.
	brand struct {
		Decoded bool
		Dark    bool
		Bitmap  gfx.Bitmap
	}

	// tray is the status item, when the desktop has a status area for one.
	tray          *app.StatusItem
	trayAvailable bool
	// trayUnavailable is set when the desktop reported that it has no status
	// area, which is a normal machine, not a failure to report.
	trayUnavailable bool
	trayErr         error
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
	a.resolveURL = a.client.SongURLs
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

// Page builds the page and its window config. The returned config already
// carries the Invalidator workers wake, and the application holds that same
// value: replacing config.Invalidator with a fresh one afterwards would leave
// the application waking an invalidator no window listens to, and a worker's
// result would never be painted.
//
// It also carries OnWake, which drains the work queue before each repaint. That
// is what makes a worker's result visible: the wake asks for the repaint, and
// the drain is what puts the result in the model the repaint reads. A window
// opened with this config — by Run or by a test — behaves the same way.
func (a *App) Page(base app.Config) (*ui.Page, app.Config) {
	theme, follow := a.themeConfig()
	page := ui.NewPage(ui.PageConfig{View: a.page, Theme: theme, FollowSystemTheme: follow})
	config := page.Config(base)
	config.OnWake = a.sync
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
//
// The page is given &a.theme, a field of the model, not a local: a theme built on
// the stack lives only as long as the call that built it, and a setting changed
// later would change the file while the window kept painting the old theme.
func (a *App) themeConfig() (*gfx.Theme, bool) {
	a.applyTheme()
	return &a.theme, false
}

// applyTheme recomputes the theme the window paints from the setting. It is
// called when the page is built and again whenever the setting changes.
//
// "system" is resolved here rather than left to the page's FollowSystemTheme,
// because the application paints with colors of its own: a theme the framework
// picks by itself would lose them. The watch below moves the same field when the
// desktop switches.
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

// watchAppearance repaints in the other theme when the desktop switches, while
// the setting says to follow it. It runs only under "system": a fixed theme is
// the user's answer, and a switch must not override it.
func (a *App) watchAppearance() {
	a.stopAppearance = app.WatchSystemAppearance(func() {
		if a.config.Theme != "system" && a.config.Theme != "" {
			return
		}
		a.applyTheme()
		a.invalidate()
	})
}

// The application's own palette, taken from the icon: the whale is drawn in
// three blues on near-black, and the window uses the same water.
//
//	cyan   #18D8F0  the whale's rim light
//	blue   #0090F0  its body
//	deep   #0048A8  the note
//	abyss  #0B1A26  the sea at night
//	foam   #F2F9FF  the light on the surface
const (
	colorCyan  = 0x18D8F0
	colorBlue  = 0x0090F0
	colorDeep  = 0x0048A8
	colorAbyss = 0x0B1A26
	colorFoam  = 0xF2F9FF
	colorShore = 0x06243B
)

// decorate returns the theme with the application's own colors. Only the tokens
// that carry the brand change; the neutrals stay the framework's, so contrast,
// the control recipes and the dark/light switch keep working.
//
// The dark appearance is the sea at night: a deep blue-black, cyan accents, and
// text that keeps its contrast. The light one is the same water under a bright
// sky: white and pale blue surfaces, the deep blue carrying the accent so it
// stays readable on white.
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

// persistSession writes the cookies the client holds now. It is called after a
// login, after a session was refreshed, and on the way out: whatever the server
// set during the run is what the next start needs, and a run that never wrote it
// back would ask for the QR code again.
func (a *App) persistSession() {
	if a.user.UserID == 0 {
		return
	}
	_ = a.sessions.Save(storeSessionFrom(a.client, a.user))
}

// refreshSession asks the server whether the saved login still works. A session
// the server no longer accepts is cleared and the window says so, rather than
// showing an account whose every request will be refused.
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
				a.user = user
				// The answer may carry refreshed cookies: keep them.
				a.persistSession()
			case errors.Is(err, ncm.ErrNotLoggedIn):
				a.user = ncm.UserInfo{}
				a.likedSet = make(map[int64]bool)
				a.data.mine = mineData{}
				_ = a.sessions.Clear()
				a.toast("登录已失效", "请重新扫码登录", ui.ToastWarning)
				a.loadDiscover()
			default:
				// A network failure says nothing about the login: keep it.
			}
		})
		a.invalidate()
		return nil
	}, nil)
}
