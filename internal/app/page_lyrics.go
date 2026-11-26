package app

import (
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// lyricsPage is the full-window lyric view: timed lines on the left, album art
// and track details on the right.
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
			a.lyricAside(c, data.song)
			return
		}
		loadingState(c, "歌词")
		return
	}
	theme := c.Theme()
	snap := a.engine.Snapshot()
	position := snap.Position
	if snap.SongID != 0 && snap.SongID != songID {
		position = 0
	}
	current, hasCurrent := data.lyric.LineAt(position)

	ui.Row(c).Fill().Children(func() {
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
			// Only auto-scroll when the active lyric line changes so the user
			// can still scroll up and down freely without being snapped back
			// every 100 ms.
			if hasCurrent && (a.lastLyricSong != songID || a.lastLyricLine != current) {
				a.lastLyricSong = songID
				a.lastLyricLine = current
				a.lyricList.ScrollTo(current, ui.ListCenter)
			}
		})
		ui.Divider(c)
		a.lyricAside(c, data.song)
	})
}

func (a *App) lyricAside(c ui.C, song ncm.Song) {
	theme := c.Theme()
	glassPanel(ui.Column(c).Width(320).FillHeight().Pad(24).Gap(12), *theme).Items(ui.Center).Children(func() {
		a.coverBox(c, ncm.CoverURL(song.Album.PicURL, 500), song.Name, 240)
		ui.Text(c, song.Name).FontSize(16).Bold().TextCenter().MaxLines(2)
		ui.Text(c, song.Singer()).FontSize(12).Muted().TextCenter()
		ui.Text(c, song.Album.Name).FontSize(11).Muted().TextCenter().MaxLines(1)
		if quality, ok := a.cached.BestQuality(song.ID); ok {
			badge(c, "离线可用 · "+quality.String(), theme.Success)
		}
		ui.Spacer(c)
		if song.Album.ID != 0 {
			if ui.Button(c, "查看专辑").Outline().Small().Clicked() {
				a.push(route{kind: routeAlbum, id: song.Album.ID, title: song.Album.Name})
			}
		}
	})
}
