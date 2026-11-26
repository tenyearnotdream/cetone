package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// queueSidebar is the slide-in play queue panel on the right edge.
func (a *App) queueSidebar(c ui.C) {
	theme := c.Theme()
	panel := glassPanel(ui.Column(c).Width(320).FillHeight(), *theme)
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

// queuePage is the full-page play queue view.
func (a *App) queuePage(c ui.C) {
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
}

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
			row.C.After(func() { a.removeFromQueue(index) })
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
			row.C.After(func() { a.removeFromQueue(index) })
		}
		menu.Separator()
		if menu.Item("查看歌词").Clicked() {
			a.push(route{kind: routeLyrics, id: song.ID, title: song.Name})
		}
	})
}
