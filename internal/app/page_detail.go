package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// toplistPage is 排行榜: the chart index as a grid of cards.
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

// playlistPage renders a playlist header and its tracks.
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
	playlist := data.playlist
	ui.Column(c).Fill().Children(func() {
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
						a.heartbeatPlaylist = playlist.ID
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
			onPlay:    func(index int) { a.heartbeatPlaylist = playlist.ID; a.playSongs(playlist.Tracks, index) },
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
}

// albumPage renders an album header and its tracks.
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
					if ui.Button(c, "缓存全部").Outline().Icon(iconDownload).Clicked() {
						a.cacheSongs(album.Songs)
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

// artistPage renders an artist's profile and hot songs.
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
