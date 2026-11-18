package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
	"cetone/internal/player"
)

// The status value the transport compares against, kept out of the pages so a
// page reads like a page.
const (
	playerPlaying   = player.StatusPlaying
	playerBuffering = player.StatusBuffering
)

// searchPage is 搜索: the field's kinds, the history before a keyword, and one
// page of results after it.
func (a *App) searchPage(c ui.C) {
	if a.search.field == "" {
		a.searchLanding(c)
		return
	}
	key := searchKey(a.search.field, a.search.kind, 0)
	data := a.data.searches[key]
	if data == nil {
		a.runSearch(false)
		loadingState(c, "搜索结果")
		return
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, func() { a.runSearch(false) })
			return
		}
		loadingState(c, "搜索结果")
		return
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().PadSides(24, 24, 10, 24).Gap(8).Items(ui.Center).Children(func() {
			kinds := []ncm.SearchKind{ncm.SearchSongs, ncm.SearchArtists, ncm.SearchAlbums, ncm.SearchPlaylists}
			kind := a.search.kind
			if ui.Choice(c, &kind, kinds, ncm.SearchKind.String).Changed() {
				a.search.kind = kind
				a.searchResults()
			}
			ui.Spacer(c)
			ui.Textf(c, "共 %d 条", data.result.Total).FontSize(11).Muted()
		})
		switch a.search.kind {
		case ncm.SearchSongs:
			a.songList(c, songList{
				songs:     data.result.Songs,
				onPlay:    func(index int) { a.playSongs(data.result.Songs, index) },
				playing:   a.currentIndexIn(data.result.Songs),
				showAlbum: true,
				onLike:    func(index int) { a.like(data.result.Songs[index]) },
				liked:     func(index int) bool { return a.likedSet[data.result.Songs[index].ID] },
				onMore:    func(index int) {},
				onAlbum: func(index int) {
					album := data.result.Songs[index].Album
					if album.ID != 0 {
						a.push(route{kind: routeAlbum, id: album.ID, title: album.Name})
					}
				},
			})
		case ncm.SearchPlaylists:
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Flow(c).FillWidth().Pad(24).Gap(16).LineGap(18).Children(func() {
					for i := range data.result.Playlists {
						a.playlistCard(c, data.result.Playlists[i], 148)
					}
				})
			})
		case ncm.SearchAlbums:
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Flow(c).FillWidth().Pad(24).Gap(16).LineGap(18).Children(func() {
					for i := range data.result.Albums {
						ui.Column(c).Width(148).Gap(4).Children(func() {
							a.albumCard(c, data.result.Albums[i], 148)
						})
					}
				})
			})
		case ncm.SearchArtists:
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Flow(c).FillWidth().Pad(24).Gap(16).LineGap(18).Children(func() {
					for i := range data.result.Artists {
						artist := data.result.Artists[i]
						card := ui.Column(c).Width(124).Gap(6).Items(ui.Center)
						card.Hit(ui.ID(c), artist.Name)
						card.Hover(ui.Look{}.Opacity(0.85))
						card.Children(func() {
							ui.Row(c).Size(110, 110).Radius(55).Clip().Children(func() {
								if bitmap := a.coverBitmap(artist.PicURL, 220); bitmap.Width > 0 {
									ui.Image(c, bitmap).Size(110, 110)
								} else {
									ui.Row(c).Size(110, 110).Background(c.Theme().Muted).Center().Children(func() {
										ui.Icon(c, iconUser).Size(36, 36).Color(c.Theme().MutedForeground)
									})
								}
							})
							ui.Text(c, artist.Name).FontSize(12).TextCenter().Ellipsis().Width(124)
						})
						if card.Clicked() {
							a.push(route{kind: routeArtist, id: artist.ID, title: artist.Name})
						}
					}
				})
			})
		}
		if data.more && a.search.kind == ncm.SearchSongs {
			ui.Row(c).FillWidth().Pad(12).Justify(ui.Center).Children(func() {
				if ui.Button(c, "加载更多").Outline().Clicked() {
					a.runSearch(true)
				}
			})
		}
	})
}

// searchLanding is the search page before a keyword: the history, and the
// account's own shortcuts.
func (a *App) searchLanding(c ui.C) {
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Pad(24).Gap(16).Children(func() {
			ui.Text(c, "搜索音乐").FontSize(22).Weight(gfx.WeightMedium)
			ui.Text(c, "在标题栏里输入关键词，回车开始搜索。").FontSize(12).Muted()
			if len(a.config.SearchHistory) > 0 {
				ui.Row(c).FillWidth().Items(ui.Center).Children(func() {
					ui.Text(c, "最近搜索").FontSize(14).Weight(gfx.WeightMedium)
					ui.Spacer(c)
					if ui.Button(c, "清空").Link().XSmall().Clicked() {
						a.config.SearchHistory = nil
						a.saveConfig()
					}
				})
				ui.Flow(c).FillWidth().Gap(8).Children(func() {
					for _, keyword := range a.config.SearchHistory {
						label := keyword
						chip := ui.Text(c, label).FontSize(12).PadSides(6, 10, 6, 10).Radius(14).Background(theme.Muted)
						chip.Hit(ui.ID(c), label)
						chip.Hover(ui.Look{}.Background(theme.Hover))
						if chip.Clicked() {
							a.search.field = label
							a.openSearch(label)
						}
					}
				})
			}
			ui.Divider(c)
			ui.Text(c, "快捷入口").FontSize(14).Weight(gfx.WeightMedium)
			ui.Flow(c).FillWidth().Gap(12).Children(func() {
				a.shortcutCard(c, iconDiscover, "每日推荐", a.openDailyRecommendationPage)
				a.shortcutCard(c, iconRadio, "私人FM", a.openPersonalFM)
				a.shortcutCard(c, iconHeart, "我喜欢的音乐", a.openLikedSongs)
				a.shortcutCard(c, iconCloud, "云盘音乐", a.openCloudDisk)
			})
		})
	})
}

// shortcutCard is one of the landing page's buttons.
func (a *App) shortcutCard(c ui.C, icon gfx.Icon, label string, onClick func()) {
	theme := c.Theme()
	card := ui.Row(c).Width(160).Pad(12).Gap(10).Radius(theme.Metrics.RadiusControl).Background(theme.Card).Items(ui.Center)
	card.Hit(ui.ID(c), label)
	card.Hover(ui.Look{}.Background(theme.Hover))
	card.Children(func() {
		ui.Row(c).Size(32, 32).Radius(16).Background(theme.Accent).Center().Children(func() {
			ui.Icon(c, icon).Size(16, 16).Color(theme.AccentForeground)
		})
		ui.Text(c, label).FontSize(12).Bold()
	})
	if card.Clicked() && onClick != nil {
		onClick()
	}
}
