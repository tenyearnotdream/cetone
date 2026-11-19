package app

import (
	"fmt"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// discoverPage is 发现: the daily playlists, the new albums and the personal
// radio. It is the page the window opens on.
func (a *App) discoverPage(c ui.C) {
	data := &a.data.discover
	if !data.loaded && !data.busy && !a.noRemote {
		a.loadDiscover()
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, a.loadDiscover)
			return
		}
		loadingState(c, "推荐内容")
		return
	}
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Pad(24).Gap(24).Children(func() {
			if a.user.UserID != 0 {
				a.oceanHero(c, func() {
					a.avatar(c, a.user.AvatarURL, a.user.Nickname, 44)
					ui.Column(c).Gap(2).Children(func() {
						ui.Textf(c, "晚上好，%s", a.user.Nickname).FontSize(20).Weight(gfx.WeightMedium)
						ui.Text(c, "今天想听点什么？").FontSize(12).Muted()
					})
					ui.Spacer(c)
					if ui.Button(c, "每日推荐").Outline().Icon(iconDiscover).Clicked() {
						a.openDailyRecommendation()
					}
					if ui.Button(c, "私人FM").Outline().Icon(iconRadio).Clicked() {
						a.openPersonalFM()
					}
				})
			} else {
				glassPanel(ui.Row(c).FillWidth().Pad(20).Gap(16).Radius(theme.Metrics.RadiusSurface), *theme).Items(ui.Center).Children(func() {
					ui.Column(c).Gap(4).Grow(1).Children(func() {
						ui.Text(c, "登录后同步你的音乐").FontSize(18).Weight(gfx.WeightMedium)
						ui.Text(c, "我喜欢的音乐、每日推荐和云盘都会出现在这里；VIP 失效前缓存的歌曲仍然可以离线播放。").FontSize(12).Muted().MaxLines(2)
					})
					if ui.Button(c, "扫码登录").Clicked() {
						a.openLogin()
					}
				})
			}

			if len(data.banners) > 0 {
				a.carousel(c, data)
			}

			if data.err != "" {
				ui.Row(c).FillWidth().Pad(12).Radius(theme.Metrics.RadiusControl).Background(theme.Danger.WithAlpha(0.1)).Children(func() {
					ui.Text(c, "部分内容加载失败："+data.err).FontSize(12).Color(theme.Danger)
				})
			}

			if a.data.mine.loaded && len(a.data.mine.daily) > 0 {
				sectionTitle(c, "每日推荐", "查看全部", a.openDailyRecommendationPage)
				ui.Flow(c).FillWidth().Gap(12).LineGap(12).Children(func() {
					limit := min(len(a.data.mine.daily), 10)
					for i := 0; i < limit; i++ {
						a.dailyCard(c, i)
					}
				})
			}

			if len(data.playlists) > 0 {
				sectionTitle(c, "推荐歌单", "", nil)
				ui.Flow(c).FillWidth().Gap(16).LineGap(18).Children(func() {
					for i := range data.playlists {
						a.playlistCard(c, data.playlists[i], 148)
					}
				})
			}

			if len(data.newAlbums) > 0 {
				sectionTitle(c, "新碟上架", "", nil)
				ui.Flow(c).FillWidth().Gap(16).LineGap(18).Children(func() {
					for i := range data.newAlbums {
						album := data.newAlbums[i]
						ui.Column(c).Width(148).Gap(4).Children(func() {
							a.albumCard(c, album, 148)
						})
					}
				})
			}

			if len(data.playlists) == 0 && len(data.newAlbums) == 0 && data.err == "" {
				emptyState(c, iconDiscover, "没有内容", "推荐接口没有返回内容，稍后再试")
			}
		})
	})
}

// carousel is the banner strip: one wide image at a time, its caption over the
// lower edge and a dot per slide. It advances only when the user asks, so
// nothing animates while the window is idle.
func (a *App) carousel(c ui.C, data *discoverData) {
	theme := c.Theme()
	if data.bannerIndex < 0 || data.bannerIndex >= len(data.banners) {
		data.bannerIndex = 0
	}
	banner := data.banners[data.bannerIndex]
	// The box matches the strip's own proportion, so the server does the
	// cropping once instead of the client stretching a square.
	const bannerWidth, bannerHeight = 1080, 216
	strip := ui.Stack(c).FillWidth().Height(180).Radius(theme.Metrics.RadiusSurface).Clip()
	strip.Hit(ui.ID(c), "推荐位")
	strip.Children(func() {
		if bitmap := a.coverBitmapBox(ncm.CoverBox(banner.ImageURL, bannerWidth, bannerHeight), bannerWidth, bannerHeight); bitmap.Width > 0 {
			// The server crops to the box asked for, so the strip shows the
			// whole picture rather than a squashed or cropped one.
			ui.Image(c, bitmap).Fill()
		} else {
			// The banner's own picture has not arrived: the placeholder is the sea
			// the icon is drawn in, cyan into deep blue.
			from, to := gfx.RGB24(colorCyan), gfx.RGB24(colorDeep)
			if isDark(*theme) {
				from, to = gfx.RGB24(0x0E4E73), gfx.RGB24(0x081B2A)
			}
			ui.Row(c).Fill().Gradient(ui.ToRight, from, to)
		}
		// The caption sits in a scrim at the foot of the image: a translucent
		// fill under a borderless line, so it reads on any artwork.
		ui.Row(c).Absolute().Left(0).Right(0).Bottom(0).Height(44).PadX(20).Items(ui.Center).
			Background(glass(*theme)).Opacity(0.94).Children(func() {
			ui.Text(c, banner.Title).FontSize(14).Weight(gfx.WeightMedium).Color(theme.Foreground).Ellipsis()
			ui.Spacer(c)
			ui.Text(c, "点击查看").FontSize(11).Muted()
		})
		next := ui.Row(c).Size(32, 32).Radius(16).Absolute().Right(12).Top(74).Opacity(0.85)
		next.Background(glass(*theme)).Border(1, glassBorder(*theme)).Center()
		next.Hit(ui.ID(c), "下一张推荐")
		next.Children(func() {
			ui.Icon(c, iconChevronRight).Size(16, 16).Color(theme.Foreground)
		})
		if next.Clicked() {
			data.bannerIndex = (data.bannerIndex + 1) % len(data.banners)
		}
	})
	if strip.Clicked() {
		a.openBanner(banner)
	}
	ui.Row(c).Gap(6).Justify(ui.Center).Children(func() {
		for i := range data.banners {
			dot := ui.Row(c).Size(8, 8).Radius(4)
			if i == data.bannerIndex {
				dot.Background(theme.Primary)
			} else {
				dot.Background(theme.Muted)
			}
			dot.Hit(ui.ID(c), fmt.Sprintf("第 %d 张推荐", i+1))
			if dot.Clicked() {
				data.bannerIndex = i
			}
		}
	})
}

// dailyCard is one song of the daily recommendation, with its cover.
func (a *App) dailyCard(c ui.C, index int) {
	song := a.data.mine.daily[index]
	theme := c.Theme()
	// A card on the sea: a lifted glass tile, its cover, and its two lines.
	// The card is as tall as its parts: cover, then the two lines with their own
	// padding. The width is the cover's plus the card's padding.
	const cover = 132
	const pad = 6
	card := glassPanel(ui.Column(c).Width(cover+pad*2).Gap(6), *theme)
	card.Radius(theme.Metrics.RadiusSurface)
	card.Pad(pad)
	card.Shadow(gfx.ShadowXS)
	card.Hit(ui.ID(c), song.Name)
	card.Hover(ui.Look{}.Background(theme.Hover).Shadow(gfx.ShadowMD))
	card.Children(func() {
		a.coverBox(c, ncm.CoverURL(song.Album.PicURL, 200), song.Name, cover)
		ui.Column(c).PadX(2).Gap(1).Children(func() {
			ui.Text(c, song.Name).FontSize(12).Ellipsis()
			ui.Text(c, song.Singer()).FontSize(10).Muted().Ellipsis()
		})
	})
	if card.Clicked() {
		a.playSongs(a.data.mine.daily, index)
	}
}

// toplistPage is 排行榜: the chart index as a list of cards.
func (a *App) toplistPage(c ui.C) {
	data := &a.data.toplist
	if !data.loaded && !data.busy {
		a.loadToplists()
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, a.loadToplists)
			return
		}
		loadingState(c, "排行榜")
		return
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Flow(c).FillWidth().Pad(24).Gap(16).LineGap(18).Children(func() {
			for i := range data.charts {
				chart := data.charts[i]
				ui.Column(c).Width(200).Gap(6).Children(func() {
					cover := a.coverBox(c, ncm.CoverURL(chart.CoverImgURL, 400), chart.Name, 200)
					cover.Hit(ui.ID(c), chart.Name)
					cover.Hover(ui.Look{}.Opacity(0.85))
					if cover.Clicked() {
						a.push(route{kind: routePlaylist, id: chart.ID, title: chart.Name})
					}
					ui.Text(c, chart.Name).FontSize(13).Bold()
					ui.Text(c, chart.UpdateFrequency).FontSize(10).Muted()
				})
			}
		})
	})
}

// playlistPage is a playlist's header and its tracks.
func (a *App) playlistPage(c ui.C, id int64) {
	a.data.init()
	data := a.data.playlists[id]
	if data == nil {
		a.loadPlaylist(id)
		loadingState(c, "歌单")
		return
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, func() { a.loadPlaylist(id) })
			return
		}
		loadingState(c, "歌单")
		return
	}
	theme := c.Theme()
	playlist := data.playlist
	ui.Column(c).Fill().Children(func() {
		// Header: big cover on the left, the facts on the right.
		ui.Row(c).FillWidth().Pad(24).Gap(20).Children(func() {
			a.coverBox(c, ncm.CoverURL(playlist.CoverImgURL, 400), playlist.Name, 168)
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				ui.Text(c, playlist.Name).FontSize(24).Weight(gfx.WeightMedium).MaxLines(2)
				ui.Row(c).Gap(10).Items(ui.Center).Children(func() {
					if playlist.Creator.Nickname != "" {
						a.avatar(c, playlist.Creator.AvatarURL, playlist.Creator.Nickname, 22)
						ui.Text(c, playlist.Creator.Nickname).FontSize(12)
					}
					ui.Textf(c, "%d 首", len(playlist.Tracks)).FontSize(12).Muted()
					if data.counts.PlayCount > 0 {
						ui.Textf(c, "播放 %s", humanCount(data.counts.PlayCount)).FontSize(12).Muted()
					}
					if data.counts.SubscribedCount > 0 {
						ui.Textf(c, "收藏 %s", humanCount(data.counts.SubscribedCount)).FontSize(12).Muted()
					}
				})
				if playlist.Description != "" {
					ui.Text(c, playlist.Description).FontSize(12).Muted().MaxLines(3)
				}
				ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
					if ui.Button(c, "播放全部").Icon(iconPlay).Clicked() {
						a.playSongs(playlist.Tracks, 0)
					}
					if ui.Button(c, "加入列表").Outline().Icon(iconPlus).Clicked() {
						a.addToQueue(playlist.Tracks, false)
						a.toast("已加入播放列表", playlist.Name, ui.ToastSuccess)
					}
					if ui.Button(c, "缓存全部").Outline().Icon(iconDownload).Clicked() {
						a.cachePlaylist(playlist)
					}
					subscribeLabel := "收藏"
					if playlist.Subscribed {
						subscribeLabel = "已收藏"
					}
					if ui.Button(c, subscribeLabel).Outline().Icon(iconHeart).Clicked() {
						a.subscribePlaylist(playlist)
					}
				})
			})
		})
		ui.Divider(c)
		a.songList(c, songList{
			songs:     playlist.Tracks,
			onPlay:    func(index int) { a.playSongs(playlist.Tracks, index) },
			playing:   a.currentIndexIn(playlist.Tracks),
			showAlbum: true,
			onLike:    func(index int) { a.like(playlist.Tracks[index]) },
			liked:     func(index int) bool { return a.likedSet[playlist.Tracks[index].ID] },
			onMore:    func(index int) {},
			onAlbum: func(index int) {
				album := playlist.Tracks[index].Album
				if album.ID != 0 {
					a.push(route{kind: routeAlbum, id: album.ID, title: album.Name})
				}
			},
		})
	})
	_ = theme
}

// albumPage is an album's header and its tracks.
func (a *App) albumPage(c ui.C, id int64) {
	a.data.init()
	data := a.data.albums[id]
	if data == nil {
		a.loadAlbum(id)
		loadingState(c, "专辑")
		return
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, func() { a.loadAlbum(id) })
			return
		}
		loadingState(c, "专辑")
		return
	}
	album := data.album
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().Pad(24).Gap(20).Children(func() {
			a.coverBox(c, ncm.CoverURL(album.PicURL, 400), album.Name, 168)
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				ui.Text(c, album.Name).FontSize(24).Weight(gfx.WeightMedium)
				ui.Row(c).Gap(10).Items(ui.Center).Children(func() {
					if album.Artist != "" {
						a.textButton(c, album.Artist, func() {
							if album.ArtistID != 0 {
								a.push(route{kind: routeArtist, id: album.ArtistID, title: album.Artist})
							}
						})
					}
					ui.Textf(c, "%d 首", len(album.Songs)).FontSize(12).Muted()
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "播放全部").Icon(iconPlay).Clicked() {
						a.playSongs(album.Songs, 0)
					}
					if ui.Button(c, "加入列表").Outline().Icon(iconPlus).Clicked() {
						a.addToQueue(album.Songs, false)
						a.toast("已加入播放列表", album.Name, ui.ToastSuccess)
					}
				})
			})
		})
		ui.Divider(c)
		a.songList(c, songList{
			songs:   album.Songs,
			onPlay:  func(index int) { a.playSongs(album.Songs, index) },
			playing: a.currentIndexIn(album.Songs),
			onLike:  func(index int) { a.like(album.Songs[index]) },
			liked:   func(index int) bool { return a.likedSet[album.Songs[index].ID] },
			onMore:  func(index int) {},
		})
	})
}

// artistPage is an artist's profile and hot songs.
func (a *App) artistPage(c ui.C, id int64) {
	a.data.init()
	data := a.data.artists[id]
	if data == nil {
		a.loadArtist(id)
		loadingState(c, "歌手")
		return
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, func() { a.loadArtist(id) })
			return
		}
		loadingState(c, "歌手")
		return
	}
	detail := data.detail
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().Pad(24).Gap(20).Items(ui.Center).Children(func() {
			ui.Row(c).Size(120, 120).Radius(60).Clip().Children(func() {
				if bitmap := a.coverBitmap(detail.Artist.PicURL, 240); bitmap.Width > 0 {
					ui.Image(c, bitmap).Size(120, 120)
				} else {
					ui.Row(c).Size(120, 120).Background(c.Theme().Muted).Center().Children(func() {
						ui.Icon(c, iconUser).Size(44, 44).Color(c.Theme().MutedForeground)
					})
				}
			})
			ui.Column(c).Gap(8).Grow(1).Children(func() {
				ui.Text(c, detail.Artist.Name).FontSize(24).Weight(gfx.WeightMedium)
				ui.Textf(c, "热门单曲 %d 首", len(detail.Songs)).FontSize(12).Muted()
				if ui.Button(c, "播放全部").Icon(iconPlay).Clicked() {
					a.playSongs(detail.Songs, 0)
				}
			})
		})
		ui.Divider(c)
		a.songList(c, songList{
			songs:   detail.Songs,
			onPlay:  func(index int) { a.playSongs(detail.Songs, index) },
			playing: a.currentIndexIn(detail.Songs),
			onLike:  func(index int) { a.like(detail.Songs[index]) },
			liked:   func(index int) bool { return a.likedSet[detail.Songs[index].ID] },
			onMore:  func(index int) {},
		})
	})
}

// currentIndexIn finds the playing song's row in a list, or -1.
func (a *App) currentIndexIn(songs []ncm.Song) int {
	snapshot := a.engine.Snapshot()
	if snapshot.Status != playerPlaying && snapshot.Status != playerBuffering {
		return -1
	}
	song, ok := a.current()
	if !ok {
		return -1
	}
	for i := range songs {
		if songs[i].ID == song.ID {
			return i
		}
	}
	return -1
}
