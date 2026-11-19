package app

import (
	"time"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// lyricsPage is the full-window lyric view: the song's words on the left, the
// art and the facts on the right, with the current line highlighted and the
// view following playback.
func (a *App) lyricsPage(c ui.C, songID int64) {
	data := a.data.lyrics[songID]
	if data == nil {
		a.loadLyric(songID)
		loadingState(c, "歌词")
		return
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, func() { a.loadLyric(songID) })
			// A song with no words still gets its facts on screen.
			a.lyricAside(c, data.song)
			return
		}
		loadingState(c, "歌词")
		return
	}
	theme := c.Theme()
	position := a.engine.Snapshot().Position
	current, hasCurrent := data.lyric.LineAt(position)

	ui.Row(c).Fill().Children(func() {
		// The words.
		ui.Column(c).Grow(1).FillHeight().Children(func() {
			if len(data.lyric.Lines) == 0 {
				emptyState(c, iconLyrics, "没有歌词", "这首歌暂时没有歌词")
				return
			}
			list := ui.List(c, &a.lyricList, len(data.lyric.Lines), func(row ui.ListRow) {
				line := data.lyric.Lines[row.Index]
				highlighted := hasCurrent && row.Index == current
				row.Element.Pad(10).PadX(24)
				if highlighted {
					row.Element.Background(theme.Accent)
				}
				ui.Column(row.C).Gap(2).Children(func() {
					text := ui.Text(row.C, line.Text).FontSize(17)
					if highlighted {
						text.Bold().Color(theme.AccentForeground)
					} else {
						text.Muted()
					}
					if line.Translation != "" {
						translation := ui.Text(row.C, line.Translation).FontSize(13)
						if highlighted {
							translation.Color(theme.AccentForeground).Opacity(0.85)
						} else {
							translation.Muted().Opacity(0.75)
						}
					}
				})
			})
			list.Label("歌词").Grow(1)
			// Follow the song: the highlighted line is kept in the middle. The
			// list places a row it has not measured from the rows it has, so
			// asking again over the next few paints settles it.
			if hasCurrent {
				a.lyricList.ScrollTo(current, ui.ListCenter)
			}
		})
		ui.Divider(c)
		a.lyricAside(c, data.song)
	})
}

// lyricAside is the right-hand panel of the lyric view: art, title, facts.
func (a *App) lyricAside(c ui.C, song ncm.Song) {
	theme := c.Theme()
	glassPanel(ui.Column(c).Width(320).FillHeight().Pad(24).Gap(12), *theme).Items(ui.Center).Children(func() {
		a.coverBox(c, ncm.CoverURL(song.Album.PicURL, 500), song.Name, 240)
		ui.Text(c, song.Name).FontSize(16).Bold().TextCenter().MaxLines(2)
		ui.Text(c, song.Singer()).FontSize(12).Muted().TextCenter()
		ui.Text(c, song.Album.Name).FontSize(11).Muted().TextCenter().MaxLines(1)
		if quality, ok := a.cached.quality(song.ID); ok {
			badge(c, "离线可用 · "+quality.String(), theme.Success)
		}
		ui.Spacer(c)
		if ui.Button(c, "在歌单中查看").Outline().Small().Clicked() {
			if song.Album.ID != 0 {
				a.push(route{kind: routeAlbum, id: song.Album.ID, title: song.Album.Name})
			}
		}
	})
}

// queuePage is the full-page play queue (the side panel is the quick one).
func (a *App) queuePage(c ui.C) {
	theme := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().Pad(24).Gap(12).Items(ui.Center).Children(func() {
			ui.Textf(c, "播放列表 · %d 首", len(a.queue)).FontSize(18).Weight(gfx.WeightMedium)
			ui.Spacer(c)
			if ui.Button(c, "清空").Outline().Clicked() {
				a.clearQueue()
			}
		})
		ui.Divider(c)
		if len(a.queue) == 0 {
			emptyState(c, iconQueue, "列表是空的", "从发现页或歌单里挑一首歌")
			return
		}
		ui.List(c, &a.queueList, len(a.queue), a.queueRow).Label("播放列表").Grow(1)
	})
	_ = theme
}

// queueRow is one row of the queue: it shows what is playing and lets a row be
// dragged to a new place.
func (a *App) queueRow(row ui.ListRow) {
	theme := row.C.Theme()
	index := row.Index
	if index < 0 || index >= len(a.queue) {
		return
	}
	song := a.queue[index]
	current := index == a.queueAt
	row.Element.PadX(12).PadY(6).Height(52).Gap(8)
	row.Element.Hover(ui.Look{}.Background(theme.Hover))
	if current {
		row.Element.Background(theme.Accent)
	}
	row.Element.Children(func() {
		if current {
			ui.Icon(row.C, iconWave).Size(14, 14).Color(theme.Primary).Width(20)
		} else {
			ui.Textf(row.C, "%d", index+1).FontSize(12).Muted().Mono().Width(20)
		}
		ui.Column(row.C).Grow(1).Gap(0).Children(func() {
			ui.Row(row.C).FillWidth().Gap(8).Items(ui.Center).Children(func() {
				text := ui.Text(row.C, song.Name).FontSize(13).Ellipsis().Grow(1)
				if current {
					text.Bold()
				}
				ui.Text(row.C, ncm.DurationString(song.Duration)).FontSize(11).Muted().Mono().Shrink(0)
			})
			ui.Text(row.C, song.Singer()).FontSize(11).Muted().Ellipsis()
		})
		if ui.Button(row.C, "").Icon(iconClose).IconOnly().XSmall().Ghost().Tooltip("移出列表").Clicked() {
			a.removeFromQueue(index)
		}
	})
	if row.Selected() && a.queueList.Changed() {
		a.playAt(index)
	}
	ui.ContextMenu(row.C, row.Element, func(menu *ui.MenuItems) {
		if menu.Item("立即播放").Clicked() {
			a.playAt(index)
		}
		if menu.Item("移出列表").Clicked() {
			a.removeFromQueue(index)
		}
		menu.Separator()
		if menu.Item("查看歌词").Clicked() {
			a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
		}
	})
}

// lyricList is the scroll position of the lyric view.
var _ = time.Second
var _ = gfx.Rect{}
