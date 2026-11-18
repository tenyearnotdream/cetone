package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"ncmviva/internal/ncm"
)

// minePage is 我的音乐: the account's playlists, and the three lists that are
// not playlists (我喜欢, 每日推荐, 云盘).
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

// mineTile is one of the three special lists.
func (a *App) mineTile(c ui.C, icon gfx.Icon, label string, count int, onClick func()) {
	theme := c.Theme()
	tile := ui.Row(c).Width(220).Pad(14).Gap(12).Radius(theme.Metrics.RadiusSurface).
		Background(theme.Card).Border(1, theme.Border).Items(ui.Center)
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

// settingsPage is 设置: playback, appearance, cache and account, in cards.
func (a *App) settingsPage(c ui.C) {
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Pad(24).Gap(16).MaxWidth(760).Children(func() {
			// Playback.
			a.settingsCard(c, "播放", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "音质").FontSize(13)
						ui.Text(c, "会员失效后，已缓存的歌曲仍按缓存文件的音质播放").FontSize(11).Muted()
					})
					quality := a.config.PreferredQuality()
					choices := ncm.Qualities()
					qualitySelect := ui.Select(c, &quality, choices, ncm.Quality.String).Width(160)
					qualitySelect.Label("音质")
					if qualitySelect.Changed() {
						a.config.Quality = int(quality)
						a.saveConfig()
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "跳过无版权歌曲").FontSize(13)
						ui.Text(c, "灰歌自动跳过；关闭后仍会尝试播放并使用本地缓存").FontSize(11).Muted()
					})
					if ui.Switch(c, &a.config.IgnoreUnplayable, "").Label("跳过无版权歌曲").Changed() {
						a.saveConfig()
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "淡出淡入").FontSize(13)
						ui.Text(c, "切换歌曲时不做交叉淡化（与 GTK 版一致）").FontSize(11).Muted()
					})
					disabled := false
					ui.Switch(c, &disabled, "").Label("淡出淡入").Disabled(true)
				})
			})

			// The status item and background running.
			a.settingsCard(c, "系统托盘", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "关闭窗口时最小化到托盘").FontSize(13)
						switch {
						case a.trayAvailable:
							ui.Text(c, "关闭窗口后音乐继续播放；从托盘菜单可以打开窗口或退出").FontSize(11).Muted()
						case a.trayUnavailable:
							ui.Text(c, "当前桌面没有可用的状态栏区域，关闭窗口会直接退出").FontSize(11).Muted()
						default:
							ui.Text(c, a.trayProblemText()).FontSize(11).Muted()
						}
					})
					closeToTray := a.config.CloseToTray
					toggle := ui.Switch(c, &closeToTray, "").Label("关闭窗口时最小化到托盘")
					toggle.Disabled(!a.trayAvailable)
					if toggle.Changed() {
						a.config.CloseToTray = closeToTray
						a.saveConfig()
						a.invalidate()
					}
				})
				if a.trayAvailable {
					ui.Divider(c)
					ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
						ui.Column(c).Gap(2).Grow(1).Children(func() {
							ui.Text(c, "托盘菜单").FontSize(13)
							ui.Text(c, "包含上一首 / 播放暂停 / 下一首、显示或隐藏窗口，以及退出").FontSize(11).Muted()
						})
						if ui.Button(c, "隐藏到托盘").Outline().Clicked() {
							if a.window != nil && !a.window.Closed() {
								a.window.Hide()
							}
						}
					})
				}
			})

			// Appearance.
			a.settingsCard(c, "外观", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "主题").FontSize(13)
						ui.Text(c, "跟随系统亮暗，或固定一种").FontSize(11).Muted()
					})
					themes := []string{"system", "light", "dark"}
					labels := map[string]string{"system": "跟随系统", "light": "浅色", "dark": "深色"}
					theme := a.config.Theme
					themeSelect := ui.Select(c, &theme, themes, func(t string) string { return labels[t] }).Width(160)
					themeSelect.Label("主题")
					if themeSelect.Changed() {
						a.config.Theme = theme
						a.saveConfig()
						a.invalidate()
					}
				})
			})

			// Cache: the feature this client exists for.
			bytes, count := a.cache.Total()
			a.settingsCard(c, "缓存", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Textf(c, "已缓存 %d 首，占用 %s", count, humanBytes(bytes)).FontSize(13)
						ui.Text(c, a.cache.Dir()).FontSize(11).Muted().Mono().Ellipsis()
					})
					if ui.Button(c, "打开目录").Outline().Clicked() {
						a.openPath(a.cache.Dir())
					}
					if ui.Button(c, "清理未完成").Outline().Clicked() {
						a.cache.ClearPartFiles()
						a.toast("已清理", "移除了未完成的缓存文件", ui.ToastSuccess)
					}
				})
				for _, dir := range a.cache.Extra {
					ui.Row(c).FillWidth().Gap(8).Items(ui.Center).Children(func() {
						ui.Icon(c, iconFolder).Size(14, 14).Color(theme.MutedForeground)
						ui.Text(c, dir).FontSize(11).Muted().Mono().Ellipsis().Grow(1)
						badge(c, "只读搜索", theme.MutedForeground)
					})
				}
				if len(a.cache.Extra) > 0 {
					ui.Text(c, "上面这些目录来自旧客户端或你的设置，只读取不写入：VIP 失效前缓存的歌曲在这里依然能播放。").FontSize(11).Muted().MaxLines(2)
				}
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "额外缓存目录").FontSize(13)
						ui.Text(c, "每行一个目录，例如 GTK 版的 ~/.cache/netease-cloud-music-gtk4").FontSize(11).Muted()
					})
				})
				cacheDirField := ui.TextArea(c, &a.cacheDirsText, "额外缓存目录", "一行一个绝对路径")
				cacheDirField.Height(80)
				if a.cacheDirsText == "" && len(a.config.CacheDirs) > 0 {
					a.cacheDirsText = joinLines(a.config.CacheDirs)
				}
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "保存目录").Outline().Clicked() {
						a.config.CacheDirs = splitLines(a.cacheDirsText)
						a.cache = newCacheWith(a.paths, a.config.CacheDirs)
						a.saveConfig()
						a.toast("已保存", "缓存目录已更新", ui.ToastSuccess)
						a.invalidate()
					}
				})
			})

			// Network.
			a.settingsCard(c, "网络", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "代理").FontSize(13)
						ui.Text(c, "http:// 或 socks5://，留空则跟随系统环境变量").FontSize(11).Muted()
					})
				})
				ui.Row(c).FillWidth().Gap(8).Items(ui.Center).Children(func() {
					proxyField := ui.TextField(c, &a.proxyText, "代理地址", "http://127.0.0.1:7890")
					proxyField.Grow(1)
					if ui.Button(c, "应用").Outline().Clicked() {
						a.config.Proxy = a.proxyText
						if err := a.client.SetProxy(a.config.Proxy); err != nil {
							a.toast("代理设置失败", errText(err), ui.ToastError)
						} else {
							a.saveConfig()
							a.toast("已应用", "新的代理设置在下次请求生效", ui.ToastSuccess)
						}
						a.invalidate()
					}
				})
				if a.proxyText == "" && a.config.Proxy != "" {
					a.proxyText = a.config.Proxy
				}
			})

			// Account and files.
			a.settingsCard(c, "账号与数据", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						if a.user.UserID != 0 {
							ui.Textf(c, "已登录：%s（%d）", a.user.Nickname, a.user.UserID).FontSize(13)
						} else {
							ui.Text(c, "未登录").FontSize(13)
						}
						ui.Text(c, a.sessions.Path()).FontSize(11).Muted().Mono().Ellipsis()
					})
					if a.user.UserID != 0 {
						if ui.Button(c, "退出登录").Outline().Clicked() {
							a.logout()
						}
					} else if ui.Button(c, "扫码登录").Clicked() {
						a.openLogin()
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "配置目录").FontSize(13)
						ui.Text(c, a.paths.Config).FontSize(11).Muted().Mono().Ellipsis()
					})
					if ui.Button(c, "打开").Outline().Clicked() {
						a.openPath(a.paths.Config)
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "关于").FontSize(13)
						ui.Text(c, "ncmviva · Go + Viva 重写的网易云音乐客户端 · GPL-3.0").FontSize(11).Muted()
					})
				})
			})
		})
	})
}

// trayProblemText is what the settings page says when the status item could not
// be created for a reason other than the desktop having no status area.
func (a *App) trayProblemText() string {
	if a.trayErr != nil {
		return "状态栏图标创建失败：" + a.trayErr.Error()
	}
	return "正在创建状态栏图标…"
}

// settingsCard is one titled group of settings. The card is a surface with a
// line around it: the theme's Card and Background are the same white in the
// light theme, so the line is what makes the group read as a group.
func (a *App) settingsCard(c ui.C, title string, body func()) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Pad(16).Gap(12).Radius(theme.Metrics.RadiusSurface).
		Background(theme.Card).Border(1, theme.Border).Children(func() {
		ui.Text(c, title).FontSize(14).Weight(gfx.WeightMedium)
		body()
	})
}
