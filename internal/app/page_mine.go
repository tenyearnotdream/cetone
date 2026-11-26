package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// minePage is 我的音乐: the account's playlists and three special collections
// (我喜欢的音乐, 每日推荐, 云盘音乐).
func (a *App) minePage(c ui.C) {
	data := &a.data.mine
	if !data.loaded && !data.busy {
		a.loadMine()
	}
	if !data.loaded {
		if data.err != "" {
			errorState(c, data.err, a.loadMine)
			return
		}
		loadingState(c, "我的音乐")
		return
	}
	if a.user.UserID == 0 {
		emptyState(c, iconUser, "还没有登录", "扫码登录后，你的歌单、每日推荐和云盘都在这里")
		ui.Row(c).Justify(ui.Center).Children(func() {
			if ui.Button(c, "扫码登录").Clicked() {
				a.openLogin()
			}
		})
		return
	}
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Pad(24).Gap(20).Children(func() {
			ui.Row(c).FillWidth().Gap(16).Items(ui.Center).Children(func() {
				a.avatar(c, a.user.AvatarURL, a.user.Nickname, 64)
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, a.user.Nickname).FontSize(22).Weight(gfx.WeightMedium)
					ui.Row(c).Gap(10).Children(func() {
						if a.user.VIPType > 0 {
							badge(c, "VIP", theme.Primary)
						}
						ui.Textf(c, "关注 %d · 粉丝 %d", a.user.Follows, a.user.Followeds).FontSize(12).Muted()
					})
					if a.user.Signature != "" {
						ui.Text(c, a.user.Signature).FontSize(12).Muted().MaxLines(2)
					}
				})
				ui.Spacer(c)
			})

			ui.Flow(c).FillWidth().Gap(16).Children(func() {
				a.mineTile(c, iconHeart, "我喜欢的音乐", len(data.liked), a.openLikedSongs)
				a.mineTile(c, iconDiscover, "每日推荐", len(data.daily), a.openDailyRecommendationPage)
				a.mineTile(c, iconCloud, "云盘音乐", len(data.cloud), a.openCloudDisk)
			})

			ui.Divider(c)
			sectionTitle(c, "我的歌单", "", nil)
			if len(data.playlists) == 0 {
				emptyState(c, iconLibrary, "没有歌单", "在网站上收藏歌单后会显示在这里")
				return
			}
			ui.Flow(c).FillWidth().Gap(16).LineGap(18).Children(func() {
				for i := range data.playlists {
					a.playlistCard(c, data.playlists[i], 148)
				}
			})
		})
	})
}

func (a *App) mineTile(c ui.C, icon gfx.Icon, label string, count int, onClick func()) {
	theme := c.Theme()
	tile := ui.Row(c).Width(220).Pad(14).Gap(12).Radius(theme.Metrics.RadiusSurface).
		Background(glass(*theme)).Border(1, glassBorder(*theme)).Items(ui.Center)
	tile.Hit(ui.ID(c), label)
	tile.Hover(ui.Look{}.Background(theme.Hover))
	tile.Children(func() {
		ui.Row(c).Size(40, 40).Radius(10).Background(theme.Accent).Center().Children(func() {
			ui.Icon(c, icon).Size(20, 20).Color(theme.AccentForeground)
		})
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(13).Bold()
			ui.Textf(c, "%d 首", count).FontSize(11).Muted()
		})
	})
	if tile.Clicked() && onClick != nil {
		onClick()
	}
}
