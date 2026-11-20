package app

import (
	"fmt"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
	"cetone/internal/player"
	"cetone/internal/store"
)

// Run opens the window and keeps the media controls exported for as long as it
// lives.
func Run(base app.Config, paths store.Paths) error {
	a, err := New(paths)
	if err != nil {
		return err
	}
	// The desktop shows this picture on the taskbar and in Alt+Tab; without it
	// the window wears the framework's default.
	base = a.WindowConfig(base)
	return app.RunApplication(func(application *app.Application) error {
		_, config := a.Page(base)
		a.application = application
		// Closing the window hides it when there is a status item to come back
		// from; otherwise it quits, as any window does.
		config.OnCloseRequest = a.handleCloseRequest
		// OnWake is already set by Page: work published by a worker or by a tray
		// menu is applied there as well as in the page, because a hidden window
		// is not painted and the page's own drain never runs.
		config.OnClosed = func() {
			// The cookies the server set during this run are what the next start
			// needs; saving them here is what makes the login remembered.
			a.persistSession()
			if a.stopAppearance != nil {
				a.stopAppearance()
				a.stopAppearance = nil
			}
			a.stopTray()
			a.stopMPRIS()
			application.Quit()
		}
		window, err := application.OpenWindow(config, new(gfx.Canvas))
		if err != nil {
			return err
		}
		a.window = window
		a.inv.Store(config.Invalidator)
		// The status item is added after the window exists, so the first press
		// on it has somewhere to show.
		a.startTray()
		a.startMPRIS()
		a.watchAppearance()
		a.restoreQueue()
		// A saved login is checked against the server, so an expired one shows
		// as expired instead of as an account nothing works for.
		a.refreshSession()
		if a.trayErr != nil {
			a.toast("状态栏图标不可用", a.trayErr.Error(), ui.ToastWarning)
		}
		return nil
	})
}

// page declares the whole window: the sidebar, the page on screen, and the
// player bar under it.
func (a *App) page(c ui.C) {
	// A worker's closure runs before the paint that shows its result, so the
	// page always reads one consistent model.
	a.sync()
	a.now = c.Canvas().Now()

	// Playing is a thing that changes while nothing else happens: the clock in
	// the transport, the highlighted lyric line and the MPRIS position all move
	// without an event. Asking for the next frame here is what lets them move;
	// while stopped the window stays idle, and a desk with a music player on it
	// should not repaint sixty times a second to say so.
	if a.engine.Snapshot().Status == player.StatusPlaying {
		c.Canvas().RequestFrameIn(playbackTick)
	}

	if a.quitRequested {
		a.quitRequested = false
		a.quit()
	}

	// Shortcuts are declared where they act. A field being typed in keeps the
	// plain keys; these all need Primary, so they never steal a letter.
	if ui.Shortcut(c, ui.Primary, " ") {
		a.togglePlay()
	}
	if ui.Shortcut(c, ui.Primary, "n") {
		a.next()
	}
	if ui.Shortcut(c, ui.Primary, "b") {
		a.previous()
	}
	if ui.Shortcut(c, ui.Primary, "f") {
		a.searchFocused = true
	}
	if ui.Shortcut(c, ui.Primary, "q") {
		a.queuePanel = !a.queuePanel
	}
	if ui.Shortcut(c, ui.Primary, "l") {
		if song, ok := a.current(); ok {
			a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
		}
	}
	if ui.Shortcut(c, 0, "escape") {
		if a.login.open {
			a.login.open = false
		} else if a.queuePanel {
			a.queuePanel = false
		}
	}

	// The shell is a fixed frame: the content takes the room the bar and the
	// dividers leave, so a long page scrolls inside it instead of pushing the
	// player off the bottom of the window.
	ui.Stack(c).Fill().Children(func() {
		// The sea is one picture under everything: contour lines, the dot grid
		// and the ambient light, drawn once per appearance rather than declared
		// as elements that every hover would re-tessellate.
		ui.Image(c, a.oceanBitmap()).Fill()
		ui.Column(c).Fill().Children(func() {
			a.titlebar(c)
			ui.Row(c).FillWidth().FillHeight().Children(func() {
				a.sidebar(c)
				ui.Divider(c)
				ui.Column(c).Grow(1).FillHeight().Children(func() {
					a.header(c)
					ui.Divider(c)
					ui.Column(c).Grow(1).FillHeight().Children(func() {
						a.content(c)
					})
				})
				if a.queuePanel {
					a.queueSidebar(c)
				}
			})
			oceanStrip(c)
			a.playerBar(c)
		})
	})

	a.loginModal(c)
	ui.Toasts(c, &a.toaster)
	debugToastsCalled = true
	debugBounds = c.Bounds()
}

// debugToastsCalled records that the page reached its toast declaration, and
// debugBounds the page's own rectangle, for one investigation.
var (
	debugToastsCalled bool
	debugBounds       ui.Rect
)

// sidebar is the navigation rail.
func (a *App) sidebar(c ui.C) {
	a.initLists()
	a.initQueueList()
	theme := c.Theme()
	// The rail keeps its width: the content beside it is what gives way when a
	// page's cards are wide.
	// The rail is not a plate: the sea runs under it, and a hairline at its edge
	// is what separates it from the content. A second translucent panel here
	// would put a lighter rectangle on the sea for no reason.
	rail := ui.Column(c).Width(212).MinWidth(212).Shrink(0).FillHeight().Pad(12).Gap(4)
	rail.Children(func() {
		ui.Row(c).Gap(10).Items(ui.Center).PadSides(8, 8, 14, 8).Children(func() {
			// The application's own icon, decoded once and kept: the rail is
			// painted on every frame, and decoding a PNG there would be a
			// per-frame file read.
			if logo := a.brandBitmap(); logo.Width > 0 {
				ui.Image(c, logo).Size(28, 28)
			} else {
				ui.Row(c).Size(28, 28).Radius(8).Background(theme.Primary).Center().Children(func() {
					ui.Icon(c, iconWave).Color(theme.PrimaryForeground)
				})
			}
			ui.Column(c).Gap(0).Children(func() {
				ui.Text(c, "Cetone").FontSize(14).Bold()
				ui.Text(c, "网易云音乐客户端").FontSize(11).Muted()
			})
		})
		a.sidebarItem(c, routeDiscover, iconDiscover, "发现")
		a.sidebarItem(c, routeToplist, iconChart, "排行榜")
		a.sidebarItem(c, routeSearch, iconSearch, "搜索")
		a.sidebarItem(c, routeMine, iconLibrary, "我的音乐")
		ui.Spacer(c)
		ui.Divider(c)
		a.sidebarItem(c, routeSettings, iconSettings, "设置")
		a.userBadge(c)
	})
}

// sidebarItem is one navigation row, marked when its section is open.
func (a *App) sidebarItem(c ui.C, kind routeKind, icon gfx.Icon, label string) {
	theme := c.Theme()
	active := len(a.nav) > 0 && a.nav[0].kind == kind
	row := ui.Row(c).FillWidth().PadSides(8, 8, 8, 8).Gap(10).Radius(theme.Metrics.RadiusControl).Items(ui.Center)
	if active {
		row.Background(theme.Accent)
	} else {
		row.Hover(ui.Look{}.Background(theme.Hover))
	}
	row.Hit(ui.ID(c), label).Children(func() {
		color := theme.MutedForeground
		if active {
			color = theme.AccentForeground
		}
		ui.Icon(c, icon).Size(18, 18).Color(color)
		var weight float32
		if active {
			weight = gfx.WeightMedium
		}
		ui.Text(c, label).FontSize(13).Color(color).Weight(weight)
	})
	if row.Clicked() {
		a.section(kind)
	}
}

// userBadge is the account row at the foot of the rail: the avatar and name
// when signed in, a login button when not.
func (a *App) userBadge(c ui.C) {
	if a.user.UserID != 0 {
		ui.Row(c).FillWidth().Pad(8).Gap(10).Items(ui.Center).Children(func() {
			a.avatar(c, a.user.AvatarURL, a.user.Nickname, 28)
			ui.Column(c).Gap(0).Grow(1).Children(func() {
				ui.Text(c, a.user.Nickname).FontSize(12).Bold().Ellipsis()
				ui.Text(c, "已登录").FontSize(10).Muted()
			})
			if ui.Button(c, "退出").XSmall().Ghost().Clicked() {
				a.logout()
			}
		})
		return
	}
	if ui.Button(c, "扫码登录").FillWidth().Clicked() {
		a.openLogin()
	}
}

// header is the bar above the content: back, the page title, search and the
// queue toggle.
func (a *App) header(c ui.C) {
	// The header is chrome over the same sea, not a band of its own.
	header := ui.Row(c).FillWidth().Height(52).PadX(16).Gap(10).Items(ui.Center)
	header.Children(func() {
		if len(a.nav) > 1 {
			back := ui.Button(c, "").Icon(iconBack).IconOnly().Ghost().Tooltip("返回")
			back.Label("返回")
			if back.Clicked() {
				a.pop()
			}
		}
		top := a.top()
		ui.Column(c).Gap(0).Children(func() {
			ui.Text(c, top.title).FontSize(15).Weight(gfx.WeightMedium)
			if subtitle := a.subtitle(top); subtitle != "" {
				ui.Text(c, subtitle).FontSize(11).Muted()
			}
		})
		ui.Spacer(c)
		a.searchBox(c)
		queue := ui.Button(c, "").Icon(iconQueue).IconOnly().Ghost().Tooltip("播放列表")
		queue.Label("播放列表")
		if queue.Clicked() {
			a.queuePanel = !a.queuePanel
		}
	})
}

// subtitle is the small line under a page title.
func (a *App) subtitle(r route) string {
	switch r.kind {
	case routePlaylist:
		if data := a.data.playlists[r.id]; data != nil && data.loaded {
			return fmt.Sprintf("%d 首 · 播放 %s", len(data.playlist.Tracks), humanCount(data.counts.PlayCount))
		}
	case routeAlbum:
		if data := a.data.albums[r.id]; data != nil && data.loaded {
			return fmt.Sprintf("%s · %d 首", data.album.Artist, len(data.album.Songs))
		}
	case routeArtist:
		if data := a.data.artists[r.id]; data != nil && data.loaded {
			return fmt.Sprintf("热门单曲 %d 首", len(data.detail.Songs))
		}
	case routeSearch:
		return a.search.kind.String()
	}
	return ""
}

// searchBox is the header's search field.
func (a *App) searchBox(c ui.C) {
	box := ui.TextField(c, &a.search.field, "搜索", "搜索歌曲、歌手、歌单")
	box.Width(240).Icon(iconSearch)
	if a.searchFocused {
		box.AutoFocus()
		a.searchFocused = false
	}
	if box.Submitted() {
		a.openSearch(a.search.field)
	}
	if box.ActionClicked() {
		a.search.field = ""
	}
}

// content switches on the route stack's top page.
func (a *App) content(c ui.C) {
	top := a.top()
	switch top.kind {
	case routeDiscover:
		a.discoverPage(c)
	case routeToplist:
		a.toplistPage(c)
	case routePlaylist:
		a.playlistPage(c, top.id)
	case routeAlbum:
		a.albumPage(c, top.id)
	case routeArtist:
		a.artistPage(c, top.id)
	case routeSearch:
		a.searchPage(c)
	case routeMine:
		a.minePage(c)
	case routeSettings:
		a.settingsPage(c)
	case routeLyrics:
		a.lyricsPage(c, top.id)
	case routeQueue:
		a.queuePage(c)
	}
}

// playerBar is the transport under the content: art, title, controls, progress
// and volume.
func (a *App) playerBar(c ui.C) {
	theme := c.Theme()
	snapshot := a.engine.Snapshot()
	song, hasSong := a.current()
	busy := snapshot.Status == player.StatusBuffering

	// The player bar is a control surface, so it stays a panel; it is the one
	// place a translucent plate is doing work rather than decorating.
	bar := ui.Row(c).FillWidth().Height(84).PadSides(12, 16, 12, 12).Gap(14).Items(ui.Center)
	bar.Background(glass(*theme))
	bar.Border(1, glassBorder(*theme))
	bar.Children(func() {
		// The cover and the title open the lyrics page, as a click on the art
		// does in the GTK client.
		art := ui.Row(c).Size(56, 56).Radius(theme.Metrics.RadiusControl).Clip()
		art.Hit(ui.ID(c), "播放封面").Children(func() {
			if hasSong {
				if bitmap := a.coverBitmap(song.Album.PicURL, 112); bitmap.Width > 0 {
					ui.Image(c, bitmap).Size(56, 56)
				} else {
					ui.Row(c).Size(56, 56).Background(theme.Muted).Center().Children(func() {
						ui.Icon(c, iconDisc).Color(theme.MutedForeground)
					})
				}
			} else {
				ui.Row(c).Size(56, 56).Background(theme.Muted).Center().Children(func() {
					ui.Icon(c, iconWave).Color(theme.MutedForeground)
				})
			}
		})
		if art.Clicked() && hasSong {
			a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
		}

		ui.Column(c).Width(190).Gap(2).Children(func() {
			if hasSong {
				a.textButton(c, song.Name, func() { a.push(route{kind: routeLyrics, id: song.ID, title: song.Name}) }).FontSize(13).Bold().Ellipsis()
				ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
					ui.Text(c, song.Singer()).FontSize(11).Muted().Ellipsis()
					// What is actually playing: the tier the server answered with,
					// not the one that was asked for. A setting above the account's
					// plan is answered with a lower level, and the interface has to
					// say so rather than let the user believe otherwise.
					if playing := a.playingQuality(song); playing != "" {
						ui.Text(c, playing).FontSize(9).Mono().Pad(2).PadX(5).
							Radius(3).Background(theme.Accent).Color(theme.AccentForeground)
					}
				})
			} else {
				ui.Text(c, "未在播放").FontSize(13).Bold().Muted()
				ui.Text(c, "从发现页挑一首").FontSize(11).Muted()
			}
		})

		// Transport.
		ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
			repeatIcon, repeatTip := iconRepeat, "列表循环"
			switch a.config.RepeatMode() {
			case "one":
				repeatIcon, repeatTip = iconRepeatOne, "单曲循环"
			case "off":
				repeatTip = "不循环"
			}
			repeat := ui.Button(c, "").Icon(repeatIcon).IconOnly().Ghost().Tooltip(repeatTip)
			repeat.Label("播放模式")
			if repeat.Clicked() {
				a.cycleRepeat()
			}
			previous := ui.Button(c, "").Icon(iconPrevious).IconOnly().Ghost().Tooltip("上一首").Disabled(!hasSong)
			previous.Label("上一首")
			if previous.Clicked() {
				a.previous()
			}
			play := ui.Button(c, "")
			play.Icon(iconPlayPause(snapshot.Status == player.StatusPlaying || busy)).IconOnly().Secondary()
			play.Label("播放/暂停").Tooltip("播放/暂停")
			if play.Clicked() {
				a.togglePlay()
			}
			next := ui.Button(c, "").Icon(iconNext).IconOnly().Ghost().Tooltip("下一首").Disabled(!hasSong)
			next.Label("下一首")
			if next.Clicked() {
				a.next()
			}
			heartbeatTip := "心动模式"
			if a.heartbeat {
				heartbeatTip = "关闭心动模式"
			}
			heartbeatButton := ui.Button(c, "").Icon(iconHeart).IconOnly().Ghost().Tooltip(heartbeatTip)
			heartbeatButton.Label("心动模式")
			if box := heartbeatButton; a.heartbeat {
				box.Color(theme.Primary)
				if box.Clicked() {
					a.toggleHeartbeat()
				}
			} else if box.Clicked() {
				a.toggleHeartbeat()
			}
			lyricButton := ui.Button(c, "").Icon(iconLyrics).IconOnly().Ghost().Tooltip("歌词").Disabled(!hasSong)
			lyricButton.Label("歌词")
			if lyricButton.Clicked() {
				if hasSong {
					a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
				}
			}
		})

		// Progress: a slider the drag writes, and the two clocks.
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			position := snapshot.Position
			duration := snapshot.Duration
			if duration <= 0 && hasSong {
				duration = song.Duration
			}
			seconds := float32(duration / time.Second)
			if seconds <= 0 {
				seconds = 1
			}
			// The bar is a slider: a drag, a click on the track and the arrow
			// keys all work, and the state stays inside the song's length.
			//
			// While the pointer holds the bar, the bar is the truth: the position
			// it was released at is where the song restarts. The transport follows
			// on release rather than on every pixel — each seek rebuilds the
			// decoder, so seeking during a drag made the whole thing stutter and
			// never catch up with the pointer.
			dragging := a.seekDrag.Dragging()
			if !dragging {
				a.seekDrag.Value = float32(position/time.Second) * 100 / seconds
			}
			seek := ui.Slider(c, &a.seekDrag).Range(0, 100).Step(0.1).FillWidth()
			seek.Label("播放进度")
			seek.Disabled(!hasSong)
			if seek.Changed() && duration > 0 {
				// Recorded now, applied below: a click and a drag both arrive as a
				// change here, and only the drag must wait for its release.
				a.seekTarget = time.Duration(float64(seconds) * float64(a.seekDrag.Value) / 100 * float64(time.Second))
				a.seekPending = true
			}
			if a.seekPending && duration > 0 && !a.seekDrag.Dragging() {
				a.seekPending = false
				a.seekTo(a.seekTarget)
				position = a.engine.Snapshot().Position
			}
			// The clock reads the bar while it is being dragged, so the time under
			// the pointer is the time it will play from.
			if dragging && duration > 0 {
				position = time.Duration(float64(seconds) * float64(a.seekDrag.Value) / 100 * float64(time.Second))
			}
			if busy {
				ui.Progress(c, float32(snapshot.Buffered)).Indeterminate().FillWidth().Height(2)
			}
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, ncm.DurationString(position)).FontSize(10).Muted().Mono()
				ui.Spacer(c)
				ui.Text(c, ncm.DurationString(duration)).FontSize(10).Muted().Mono()
			})
		})

		// Volume.
		ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
			icon := iconVolumeWaves
			if snapshot.Muted || snapshot.Volume == 0 {
				icon = iconMute
			}
			mute := ui.Button(c, "").Icon(icon).IconOnly().Ghost().Tooltip("静音")
			mute.Label("静音")
			if mute.Clicked() {
				a.toggleMute()
			}
			slider := ui.Slider(c, &a.volumeDrag).Range(0, 100).Step(1).Width(110)
			slider.Label("音量")
			if slider.Changed() {
				a.setVolume(float64(a.volumeDrag.Value) / 100)
			}
		})
	})
}

// textButton is a label that acts as a button: the player's title, a card's
// name. It is a Hit box, so it takes the click and the hover the plain Button
// would, without the button's padding and border.
func (a *App) textButton(c ui.C, label string, onClick func()) ui.Box {
	theme := c.Theme()
	box := ui.Text(c, label)
	box.Hit(ui.ID(c), label)
	box.Hover(ui.Look{}.Color(theme.Primary))
	if box.Clicked() && onClick != nil {
		onClick()
	}
	return box
}

// cycleRepeat steps through the repeat modes and stores the new one.
func (a *App) cycleRepeat() {
	switch a.config.RepeatMode() {
	case "list":
		a.config.Repeat = "one"
	case "one":
		a.config.Repeat = "off"
	default:
		a.config.Repeat = "list"
	}
	a.saveConfig()
	a.saveAndToast("播放模式", map[string]string{"list": "列表循环", "one": "单曲循环", "off": "不循环"}[a.config.RepeatMode()])
}

// saveAndToast writes the settings and says what changed.
func (a *App) saveAndToast(title, body string) {
	a.saveConfig()
	a.toast(title, body, ui.ToastDefault)
	a.invalidate()
}

// queueSidebar is the play queue panel on the right.
func (a *App) queueSidebar(c ui.C) {
	theme := c.Theme()
	panel := ui.Column(c).Width(320).FillHeight()
	panel = glassPanel(panel, *theme)
	panel.Children(func() {
		ui.Row(c).FillWidth().PadSides(16, 12, 12, 16).Gap(8).Items(ui.Center).Children(func() {
			ui.Textf(c, "播放列表 (%d)", len(a.queue)).FontSize(13).Bold()
			ui.Spacer(c)
			clear := ui.Button(c, "").Icon(iconTrash).IconOnly().Ghost().Tooltip("清空列表")
			clear.Label("清空播放列表")
			if clear.Clicked() {
				a.clearQueue()
			}
			closePanel := ui.Button(c, "").Icon(iconClose).IconOnly().Ghost().Tooltip("关闭")
			closePanel.Label("关闭播放列表")
			if closePanel.Clicked() {
				a.queuePanel = false
			}
		})
		ui.Divider(c)
		if len(a.queue) == 0 {
			emptyState(c, iconQueue, "列表是空的", "从发现页或歌单里挑一首歌")
			return
		}
		ui.List(c, &a.queueList, len(a.queue), a.queueRow).Label("播放列表").Grow(1)
	})
}

// humanCount shortens a play count the way the site does.
func humanCount(n int64) string {
	switch {
	case n >= 100000000:
		return fmt.Sprintf("%.1f亿", float64(n)/100000000)
	case n >= 10000:
		return fmt.Sprintf("%.1f万", float64(n)/10000)
	}
	return fmt.Sprintf("%d", n)
}

// playbackTick is how often the window repaints while a song plays: often enough
// for the transport and the lyric line to look continuous, rare enough that an
// idle window is not redrawn for nothing.
const playbackTick = 100 * time.Millisecond

// WindowConfig is the window's own decoration: the icon of the current
// appearance, and a title bar the page paints so the sea reaches the top edge.
//
// It is a method so a test can check what Run would open: a window that fell back
// to the desktop's title bar is exactly the seam this is here to remove, and a
// test that built its own config would not see it.
func (a *App) WindowConfig(base app.Config) app.Config {
	// The icon follows the appearance: the dark tile on a dark taskbar, the pale
	// one on a pale taskbar.
	a.applyTheme()
	if icon, err := a.windowIcon(); err == nil {
		base.Icon = icon
	}
	base.WindowMode = app.WindowCustomTitlebar
	base.TitlebarHeight = titlebarHeight
	base.ClearColor = a.clearColor()
	// The buttons Viva draws in the strip are styled by a function, because the
	// config asks it at every paint: they follow a theme change.
	base.WindowControls = a.windowControlsStyle
	return base
}
