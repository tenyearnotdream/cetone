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

// playbackTick is how often the window repaints while a song plays or buffers.
const playbackTick = 100 * time.Millisecond

// Run opens the window and keeps the tray and MPRIS controls alive for as long
// as it runs.
func Run(base app.Config, paths store.Paths) error {
	a, err := New(paths)
	if err != nil {
		return err
	}
	base = a.WindowConfig(base)
	return app.RunApplication(func(application *app.Application) error {
		_, config := a.Page(base)
		a.application = application
		config.OnCloseRequest = a.handleCloseRequest
		config.OnClosed = func() {
			a.persistSession()
			a.persistQueue()
			if a.stopAppearance != nil {
				a.stopAppearance()
				a.stopAppearance = nil
			}
			a.stopTray()
			a.stopMPRIS()
			a.engine.Close()
			application.Quit()
		}
		window, err := application.OpenWindow(config, new(gfx.Canvas))
		if err != nil {
			return err
		}
		a.window = window
		a.inv.Store(config.Invalidator)
		a.startTray()
		a.startMPRIS()
		a.watchAppearance()
		a.restoreQueue()
		a.refreshSession()
		if a.trayErr != nil {
			a.toast("状态栏图标不可用", a.trayErr.Error(), ui.ToastWarning)
		}
		return nil
	})
}

// page declares the whole window: title bar, sidebar, active page, optional
// queue drawer, player bar, login modal and toasts.
func (a *App) page(c ui.C) {
	a.sync()
	a.now = c.Canvas().Now()

	snapshot := a.engine.Snapshot()
	if snapshot.Status == player.StatusPlaying || snapshot.Status == player.StatusBuffering {
		c.Canvas().RequestFrameIn(playbackTick)
		if snapshot.Status == player.StatusPlaying && time.Since(a.lastMPRISAt) >= time.Second {
			a.lastMPRISAt = time.Now()
			a.publishMPRISPosition(snapshot.Position)
		}
	}

	if a.quitRequested {
		a.quitRequested = false
		a.quit()
	}

	// Global keyboard shortcuts.
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

	ui.Stack(c).Fill().Children(func() {
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
			a.playerBar(c, snapshot)
		})
	})

	a.loginModal(c)
	ui.Toasts(c, &a.toaster)
}

func (a *App) sidebar(c ui.C) {
	theme := c.Theme()
	rail := ui.Column(c).Width(212).MinWidth(212).Shrink(0).FillHeight().Pad(12).Gap(4)
	rail.Children(func() {
		ui.Row(c).Gap(10).Items(ui.Center).PadSides(8, 8, 14, 8).Children(func() {
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

func (a *App) userBadge(c ui.C) {
	if a.user.UserID != 0 {
		ui.Row(c).FillWidth().Pad(8).Gap(10).Items(ui.Center).Children(func() {
			a.avatar(c, a.user.AvatarURL, a.user.Nickname, 28)
			ui.Column(c).Gap(0).Grow(1).Children(func() {
				ui.Text(c, a.user.Nickname).FontSize(12).Bold().Ellipsis()
				if a.user.VIPType > 0 {
					ui.Text(c, "VIP · 已登录").FontSize(10).Muted()
				} else {
					ui.Text(c, "已登录").FontSize(10).Muted()
				}
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

func (a *App) header(c ui.C) {
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

// playerBar is the bottom transport: cover, title & quality selector, playback
// controls, seek progress slider, and volume control.
func (a *App) playerBar(c ui.C, snapshot player.Snapshot) {
	theme := c.Theme()
	song, hasSong := a.current()
	busy := snapshot.Status == player.StatusBuffering

	bar := ui.Row(c).FillWidth().Height(84).PadSides(12, 16, 12, 12).Gap(14).Items(ui.Center)
	bar.Background(glass(*theme))
	bar.Border(1, glassBorder(*theme))
	bar.Children(func() {
		// Cover art opens the lyrics page.
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

		// Track title, singer, and interactive quality selector badge.
		ui.Column(c).Width(200).Gap(2).Children(func() {
			if hasSong {
				a.textButton(c, song.Name, func() {
					a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
				}).FontSize(13).Bold().Ellipsis()
				ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
					ui.Text(c, song.Singer()).FontSize(11).Muted().Ellipsis().Grow(1)
					a.qualityBadge(c, song)
				})
			} else {
				ui.Text(c, "未在播放").FontSize(13).Bold().Muted()
				ui.Text(c, "从发现页挑一首").FontSize(11).Muted()
			}
		})

		// Transport buttons.
		ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
			repeatIcon, repeatTip := iconRepeat, "列表循环"
			switch a.config.RepeatMode() {
			case store.RepeatOne:
				repeatIcon, repeatTip = iconRepeatOne, "单曲循环"
			case store.RepeatOff:
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
			if a.heartbeat {
				heartbeatButton.Color(theme.Primary)
			}
			if heartbeatButton.Clicked() {
				a.toggleHeartbeat()
			}
			lyricButton := ui.Button(c, "").Icon(iconLyrics).IconOnly().Ghost().Tooltip("歌词").Disabled(!hasSong)
			lyricButton.Label("歌词")
			if lyricButton.Clicked() && hasSong {
				a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
			}
		})

		// Progress slider and elapsed / total clocks.
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			position := snapshot.Position
			duration := snapshot.Duration
			// Defense in depth: if the engine snapshot belongs to a different
			// song, show the current song's metadata from 0 rather than stale time.
			if hasSong && snapshot.SongID != 0 && snapshot.SongID != song.ID {
				position = 0
				duration = song.Duration
			}
			if duration <= 0 && hasSong {
				duration = song.Duration
			}
			if duration > 0 && position > duration {
				position = duration
			}

			wasDragging := a.seekDrag.Dragging()
			if !wasDragging && !a.seekPending {
				a.seekDrag.Value = progressPercent(position, duration)
			}

			seek := ui.Slider(c, &a.seekDrag).Range(0, 100).Step(0.1).FillWidth()
			seek.Label("播放进度")
			seek.Disabled(!hasSong)

			changed := seek.Changed()
			nowDragging := a.seekDrag.Dragging()

			if changed && duration > 0 {
				a.seekTarget = time.Duration(float64(a.seekDrag.Value) / 100.0 * float64(duration))
				if nowDragging {
					// Defer engine seek while dragging so we never thrash the audio
					// device on every mouse-move pixel; commit cleanly on release.
					a.seekPending = true
				} else {
					// Keyboard arrows or non-drag change: commit immediately.
					a.seekPending = false
					a.seekTo(a.seekTarget)
					position = a.seekTarget
				}
			} else if a.seekPending && !nowDragging && duration > 0 {
				// Pointer released after click or drag: commit once.
				a.seekPending = false
				a.seekTo(a.seekTarget)
				position = a.seekTarget
			}

			if (nowDragging || a.seekPending) && duration > 0 {
				position = a.seekTarget
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

		// Volume control.
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

// qualityBadge displays the active audio quality and opens a menu above the
// player bar to switch qualities on the fly without restarting the track.
func (a *App) qualityBadge(c ui.C, song ncm.Song) {
	theme := c.Theme()
	playing := a.playingQuality(song)
	if playing == "" {
		if q, ok := a.cached.BestQuality(song.ID); ok {
			playing = q.String()
		} else {
			playing = "音质"
		}
	}
	badge := ui.Row(c).Pad(2).PadX(5).Radius(3).Shrink(0).Background(theme.Accent).Hit(ui.ID(c), "音质："+playing)
	badge.Label("音质")
	badge.Tooltip("切换播放音质")
	badge.Hover(ui.Look{}.Background(theme.Selection))
	badge.Children(func() {
		ui.Text(c, playing).FontSize(9).Mono().Color(theme.AccentForeground)
	})
	if badge.Clicked() {
		a.qualityMenuOpen = !a.qualityMenuOpen
	}
	ui.Menu(c, badge, &a.qualityMenuOpen, func(menu *ui.MenuItems) {
		menu.Above()
		menu.WidthRange(176, 260)
		best := a.bestQualityFor(song)
		cachedSet := make(map[ncm.Quality]bool)
		for _, q := range a.cached.Qualities(song.ID) {
			cachedSet[q] = true
		}

		menu.Label("切换播放音质")
		autoItem := menu.Item("自动（最高音质）").Description("优先缓存最高音质")
		if !song.SelectedSet {
			autoItem.Chosen(true)
		}
		if autoItem.Clicked() {
			a.chooseAutoQuality(song)
		}
		menu.Separator()
		for q := best; q >= ncm.QualityStandard; q-- {
			quality := q
			item := menu.Item(quality.String())
			if cachedSet[quality] {
				item.Description("已本地缓存")
			}
			if song.SelectedSet && quality == song.Selected {
				item.Chosen(true)
			} else if !song.SelectedSet && song.ActualSet && quality == song.Actual {
				item.Checked(true)
			}
			if item.Clicked() {
				a.chooseQuality(song, quality)
			}
		}
		if best < ncm.QualityMaster {
			menu.Separator()
			menu.Label("更高档位当前账号不可用")
		}
	})
}

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

func (a *App) cycleRepeat() {
	switch a.config.RepeatMode() {
	case store.RepeatList:
		a.config.Repeat = store.RepeatOne
	case store.RepeatOne:
		a.config.Repeat = store.RepeatOff
	default:
		a.config.Repeat = store.RepeatList
	}
	a.saveAndToast("播放模式", map[string]string{
		store.RepeatList: "列表循环",
		store.RepeatOne:  "单曲循环",
		store.RepeatOff:  "不循环",
	}[a.config.RepeatMode()])
}

func (a *App) saveAndToast(title, body string) {
	a.saveConfig()
	a.toast(title, body, ui.ToastDefault)
	a.invalidate()
}

func humanCount(n int64) string {
	switch {
	case n >= 100000000:
		return fmt.Sprintf("%.1f亿", float64(n)/100000000)
	case n >= 10000:
		return fmt.Sprintf("%.1f万", float64(n)/10000)
	}
	return fmt.Sprintf("%d", n)
}

// WindowConfig decorates the window with a theme-aware icon and custom titlebar.
func (a *App) WindowConfig(base app.Config) app.Config {
	a.applyTheme()
	if icon, err := a.windowIcon(); err == nil {
		base.Icon = icon
	}
	base.WindowMode = app.WindowCustomTitlebar
	base.TitlebarHeight = titlebarHeight
	base.ClearColor = a.clearColor()
	base.WindowControls = a.windowControlsStyle
	return base
}
