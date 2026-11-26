package app

import (
	"fmt"
	"time"

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
						ui.Textf(c, "%s，%s", greetingByHour(time.Now().Hour()), a.user.Nickname).FontSize(20).Weight(gfx.WeightMedium)
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

func greetingByHour(hour int) string {
	switch {
	case hour < 6:
		return "夜深了"
	case hour < 12:
		return "早上好"
	case hour < 18:
		return "下午好"
	default:
		return "晚上好"
	}
}

func (a *App) carousel(c ui.C, data *discoverData) {
	theme := c.Theme()
	if data.bannerIndex < 0 || data.bannerIndex >= len(data.banners) {
		data.bannerIndex = 0
	}
	banner := data.banners[data.bannerIndex]
	const bannerWidth, bannerHeight = 1080, 216
	strip := ui.Stack(c).FillWidth().Height(180).Radius(theme.Metrics.RadiusSurface).Clip()
	strip.Hit(ui.ID(c), "推荐位")
	strip.Children(func() {
		if bitmap := a.coverBitmapBox(ncm.CoverBox(banner.ImageURL, bannerWidth, bannerHeight), bannerWidth, bannerHeight); bitmap.Width > 0 {
			ui.Image(c, bitmap).Fill()
		} else {
			from, to := gfx.RGB24(colorCyan), gfx.RGB24(colorDeep)
			if isDark(*theme) {
				from, to = gfx.RGB24(0x0E4E73), gfx.RGB24(0x081B2A)
			}
			ui.Row(c).Fill().Gradient(ui.ToRight, from, to)
		}
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

func (a *App) dailyCard(c ui.C, index int) {
	song := a.data.mine.daily[index]
	theme := c.Theme()
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
