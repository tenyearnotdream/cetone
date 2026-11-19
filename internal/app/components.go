package app

import (
	"fmt"
	"strings"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// The pieces every page composes: a card, a song row, an empty state, a cover
// with its placeholder. They are page components in Viva's sense — functions of
// the page that return the box the caller reacts from — and they keep no state.

// avatar draws a round picture with initials behind it until the image loads.
func (a *App) avatar(c ui.C, rawURL, name string, size float32) {
	theme := c.Theme()
	ui.Row(c).Size(size, size).Radius(size / 2).Clip().Children(func() {
		if bitmap := a.coverBitmap(rawURL, int(size*2)); bitmap.Width > 0 {
			ui.Image(c, bitmap).Size(size, size)
			return
		}
		ui.Row(c).Size(size, size).Background(theme.Muted).Center().Children(func() {
			ui.Text(c, initials(name)).FontSize(size * 0.4).Muted()
		})
	})
}

// initials is the one or two characters a placeholder avatar shows: the first
// character of a name written in Chinese, Japanese or Korean, and the first two
// letters of a Latin one.
func initials(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return "?"
	}
	if runes[0] > 0x2E80 {
		return string(runes[0])
	}
	first := strings.ToUpper(string(runes[0]))
	if len(runes) > 1 && runes[1] < 0x2E80 && runes[1] != ' ' {
		return first + strings.ToUpper(string(runes[1]))
	}
	return first
}

// coverBox is a square cover with the album's initial behind it while the art
// loads. The caller gives it the size it should take.
func (a *App) coverBox(c ui.C, rawURL, fallback string, size float32) ui.Box {
	theme := c.Theme()
	box := ui.Row(c).Size(size, size).Radius(theme.Metrics.RadiusSurface).Clip().Background(theme.Muted).Shrink(0)
	box.Children(func() {
		if bitmap := a.coverBitmap(rawURL, int(size*2)); bitmap.Width > 0 {
			ui.Image(c, bitmap).Size(size, size)
			return
		}
		ui.Row(c).Size(size, size).Center().Children(func() {
			ui.Icon(c, iconDisc).Size(size*0.34, size*0.34).Color(theme.MutedForeground)
		})
	})
	return box
}

// emptyState is the panel a page shows when it has nothing to list.
func emptyState(c ui.C, icon gfx.Icon, title, body string) {
	theme := c.Theme()
	ui.Column(c).Fill().Pad(40).Gap(10).Center().Children(func() {
		ui.Row(c).Size(56, 56).Radius(28).Background(theme.Muted).Center().Children(func() {
			ui.Icon(c, icon).Size(24, 24).Color(theme.MutedForeground)
		})
		ui.Text(c, title).FontSize(14).Bold()
		ui.Text(c, body).FontSize(12).Muted().MaxLines(2).TextCenter()
	})
}

// loadingState is the panel while a page's first request is in flight.
func loadingState(c ui.C, what string) {
	ui.Column(c).Fill().Pad(40).Gap(12).Center().Children(func() {
		ui.Spinner(c)
		ui.Text(c, "正在加载"+what).FontSize(12).Muted()
	})
}

// errorState is the panel a failed request leaves, with a retry button.
func errorState(c ui.C, message string, retry func()) {
	theme := c.Theme()
	ui.Column(c).Fill().Pad(40).Gap(10).Center().Children(func() {
		ui.Row(c).Size(48, 48).Radius(24).Background(theme.Danger.WithAlpha(0.12)).Center().Children(func() {
			ui.Icon(c, iconInfo).Size(22, 22).Color(theme.Danger)
		})
		ui.Text(c, "加载失败").FontSize(14).Bold()
		ui.Text(c, message).FontSize(12).Muted().MaxLines(3).TextCenter()
		if ui.Button(c, "重试").Outline().Clicked() && retry != nil {
			retry()
		}
	})
}

// sectionTitle is the heading above a row of cards.
func sectionTitle(c ui.C, title, action string, onClick func()) {
	ui.Row(c).FillWidth().Items(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(16).Weight(gfx.WeightMedium)
		ui.Spacer(c)
		if action != "" {
			if ui.Button(c, action).Link().XSmall().Clicked() && onClick != nil {
				onClick()
			}
		}
	})
}

// badge is a small label that says what a song or a playlist is.
func badge(c ui.C, text string, tone gfx.Color) {
	theme := c.Theme()
	ui.Text(c, text).
		FontSize(10).
		Color(tone).
		PadSides(5, 6, 5, 6).
		Radius(4).
		Background(theme.Muted)
}

// songRow is one row of a track list: the index, the title with its badges, the
// singer, the album and the duration, plus the like button. It returns nothing:
// the caller passes the list it belongs to so a click on the row can play it.
type songList struct {
	// songs is the list the rows index into.
	songs []ncm.Song
	// onPlay is called with the row's index.
	onPlay func(index int)
	// playing is the index the player is on, or -1.
	playing int
	// showAlbum hides the album column in a narrow pane.
	showAlbum bool
	// onLike, when set, is the heart's action; nil hides the heart.
	onLike func(index int)
	// liked reports whether the song at index is in 我喜欢的音乐.
	liked func(index int) bool
	// onMore, when set, opens the row's context menu entries.
	onMore func(index int)
	// onAlbum, when set, opens the album page.
	onAlbum func(index int)
}

// songList declares a scrolling track list with a header row.
func (a *App) songList(c ui.C, list songList) {
	theme := c.Theme()
	ui.Column(c).Fill().Children(func() {
		if len(list.songs) == 0 {
			emptyState(c, iconLyrics, "还没有歌曲", "这个列表里没有可显示的内容")
			return
		}
		// Column titles, aligned with the rows below.
		ui.Row(c).FillWidth().Height(32).PadX(16).Gap(12).Items(ui.Center).Background(theme.Background).Children(func() {
			ui.Text(c, "#").FontSize(11).Muted().Width(28)
			ui.Text(c, "标题").FontSize(11).Muted().Grow(1)
			if list.showAlbum {
				ui.Text(c, "专辑").FontSize(11).Muted().Width(180)
			}
			ui.Text(c, "时长").FontSize(11).Muted().Width(52).TextEnd()
			ui.Row(c).Width(56)
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			for i := range list.songs {
				a.songRow(c, list, i)
			}
		})
	})
}

// songRow is one track line.
func (a *App) songRow(c ui.C, list songList, index int) {
	theme := c.Theme()
	song := list.songs[index]
	isCurrent := list.playing == index
	row := ui.Row(c.KeyInt(index)).FillWidth().Height(46).PadX(16).Gap(12).Items(ui.Center)
	row.Hover(ui.Look{}.Background(theme.Hover))
	if isCurrent {
		row.Background(theme.Accent)
	}
	row.Hit(ui.ID(c), song.Name)
	row.Children(func() {
		// The index, or the playing marker on the current song.
		if isCurrent {
			ui.Icon(c, iconWave).Size(14, 14).Color(brandTint(*theme, isDark(*theme)))
		} else {
			ui.Textf(c, "%d", index+1).FontSize(12).Muted().Mono().Width(28)
		}
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Row(c).Gap(6).Items(ui.Center).Children(func() {
				ui.Text(c, song.Name).FontSize(13).Ellipsis()
				switch song.Copyright() {
				case ncm.CopyrightVIP:
					badge(c, "VIP", theme.Primary)
				case ncm.CopyrightPaid:
					badge(c, "付费", theme.Chart[1])
				case ncm.CopyrightVIPHighRate:
					badge(c, "VIP", theme.Primary)
					badge(c, "高音质", theme.Chart[1])
				case ncm.CopyrightUnavailable:
					badge(c, "无版权", theme.Danger)
				}
				// A cached song plays offline, and that is worth saying: it is
				// the client's own feature.
				if a.cached.has(song.ID) {
					badge(c, "已缓存", theme.Success)
				}
			})
			ui.Text(c, song.Singer()).FontSize(11).Muted().Ellipsis()
		})
		if list.showAlbum {
			ui.Text(c, song.Album.Name).FontSize(12).Muted().Width(180).Ellipsis()
		}
		ui.Text(c, ncm.DurationString(song.Duration)).FontSize(11).Muted().Mono().Width(52).TextEnd()
		ui.Row(c).Width(56).Gap(2).Items(ui.Center).Children(func() {
			if list.onLike != nil {
				icon := iconHeart
				if list.liked != nil && list.liked(index) {
					icon = iconHeartFilled
				}
				like := ui.Button(c, "").Icon(icon).IconOnly().XSmall().Ghost().Tooltip("收藏")
				like.Label("收藏")
				if like.Clicked() {
					list.onLike(index)
				}
			}
			if list.onMore != nil {
				more := ui.Button(c, "").Icon(iconQueue).IconOnly().XSmall().Ghost().Tooltip("更多")
				more.Label("更多操作")
				ui.Menu(c, more, &a.rowMenuOpen, func(menu *ui.MenuItems) {
					a.rowMenu(menu, list, index)
				})
			}
		})
	})
	if row.DoubleClicked() && list.onPlay != nil {
		list.onPlay(index)
	}
	// A single click on a row plays it, as the GTK client does; the heart and
	// the menu take their own clicks.
	if row.Clicked() {
		if list.onAlbum != nil && row.ClickModifiers()&gfx.ModifierAlt != 0 {
			list.onAlbum(index)
		} else if list.onPlay != nil {
			list.onPlay(index)
		}
	}
	if row.Valid() {
		ui.ContextMenu(c, row, func(menu *ui.MenuItems) {
			a.rowMenu(menu, list, index)
		})
	}
}

// rowMenu is the actions every track row offers.
func (a *App) rowMenu(menu *ui.MenuItems, list songList, index int) {
	if index < 0 || index >= len(list.songs) {
		return
	}
	song := list.songs[index]
	if menu.Item("立即播放").Clicked() && list.onPlay != nil {
		list.onPlay(index)
	}
	if menu.Item("下一首播放").Clicked() {
		a.playNextInQueue(song)
	}
	if menu.Item("加入播放列表").Clicked() {
		a.addToQueue([]ncm.Song{song}, false)
		a.toast("已加入播放列表", song.Name, ui.ToastSuccess)
	}
	menu.Separator()
	if menu.Item("查看歌词").Clicked() {
		a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
	}
	if song.ArtistID() != 0 && menu.Item("查看歌手").Clicked() {
		a.push(route{kind: routeArtist, id: song.ArtistID(), title: song.Singer()})
	}
	if menu.Item("收藏/取消收藏").Clicked() {
		a.like(song)
	}
	menu.Separator()
	if path, quality, ok := a.cache.Lookup(song.ID, a.config.PreferredQuality()); ok {
		if menu.Item("导出缓存文件").Clicked() {
			a.exportCached(song, path, quality)
		}
	}
	if menu.Item("复制歌曲链接").Clicked() {
		a.copySongLink(song)
	}
}

// playNextInQueue inserts a song after the current one.
func (a *App) playNextInQueue(song ncm.Song) {
	if len(a.queue) == 0 {
		a.addToQueue([]ncm.Song{song}, true)
		return
	}
	at := a.queueAt + 1
	a.queue = append(a.queue, ncm.Song{})
	copy(a.queue[at+1:], a.queue[at:])
	a.queue[at] = song
	a.persistQueue()
	a.toast("下一首播放", song.Name, ui.ToastSuccess)
	a.invalidate()
}

// playlistCard is one tile of the discover grid: cover, name, play count.
func (a *App) playlistCard(c ui.C, playlist ncm.Playlist, width float32) {
	theme := c.Theme()
	_ = theme
	ui.Column(c).Width(width).Gap(6).Children(func() {
		cover := a.coverBox(c, ncm.CoverURL(playlist.CoverImgURL, 300), playlist.Name, width)
		cover.Hit(ui.ID(c), playlist.Name)
		cover.Hover(ui.Look{}.Opacity(0.85))
		if cover.Clicked() {
			a.push(route{kind: routePlaylist, id: playlist.ID, title: playlist.Name})
		}
		ui.Text(c, playlist.Name).FontSize(12).MaxLines(2)
		ui.Text(c, fmt.Sprintf("%s 首 · %s", humanCount(int64(playlist.TrackCount)), humanCount(playlist.PlayCount))).FontSize(10).Muted()
	})
}

// albumCard is one tile of the new-albums row.
func (a *App) albumCard(c ui.C, album ncm.Album, width float32) {
	cover := a.coverBox(c, ncm.CoverURL(album.PicURL, 300), album.Name, width)
	cover.Hit(ui.ID(c), album.Name)
	cover.Hover(ui.Look{}.Opacity(0.85))
	if cover.Clicked() {
		a.push(route{kind: routeAlbum, id: album.ID, title: album.Name})
	}
	ui.Text(c, album.Name).FontSize(12).MaxLines(1).Ellipsis().Width(width)
	ui.Text(c, album.Artist).FontSize(10).Muted().Ellipsis().Width(width)
}
