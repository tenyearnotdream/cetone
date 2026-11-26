package app

import (
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// settingsPage is 设置: playback, system tray, appearance, cache, network and
// account settings.
func (a *App) settingsPage(c ui.C) {
	theme := c.Theme()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Pad(24).Gap(16).MaxWidth(760).Children(func() {
			// Playback.
			a.settingsCard(c, "播放", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "跳过无版权歌曲").FontSize(13)
						ui.Text(c, "灰歌自动跳过；已缓存的歌曲始终可离线播放").FontSize(11).Muted()
					})
					if ui.Switch(c, &a.config.IgnoreUnplayable, "").Label("跳过无版权歌曲").Changed() {
						a.saveConfig()
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "音质与缓存策略").FontSize(13)
						ui.Text(c, "默认自动获取并优先缓存当前账号可用的最高音质；播放时可点底栏音质标签实时切换").FontSize(11).Muted().MaxLines(2)
					})
				})
			})

			// System tray.
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
					selectedTheme := a.config.Theme
					themeSelect := ui.Select(c, &selectedTheme, themes, func(t string) string { return labels[t] }).Width(160)
					themeSelect.Label("主题")
					if themeSelect.Changed() {
						a.config.Theme = selectedTheme
						a.applyTheme()
						a.updateTray()
						a.saveConfig()
						a.invalidate()
					}
				})
			})

			// Cache (reads cached index stats — never walks the disk on every frame).
			bytes, count := a.cached.Stats()
			a.settingsCard(c, "缓存", func() {
				ui.Row(c).FillWidth().Items(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Textf(c, "已缓存 %d 个文件，占用 %s", count, humanBytes(bytes)).FontSize(13)
						ui.Text(c, a.cache.Dir()).FontSize(11).Muted().Mono().Ellipsis()
					})
					if ui.Button(c, "打开目录").Outline().Clicked() {
						a.openPath(a.cache.Dir())
					}
					if ui.Button(c, "清理未完成").Outline().Clicked() {
						a.cache.ClearPartFiles()
						a.cached.SetDirty()
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
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "保存目录").Outline().Clicked() {
						a.config.CacheDirs = splitLines(a.cacheDirsText)
						a.cache = newCacheWith(a.paths, a.config.CacheDirs)
						a.cached.SetCache(a.cache)
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
						ui.Text(c, "Cetone · 基于 Go + Viva 重写的网易云音乐客户端 · GPL-3.0").FontSize(11).Muted()
					})
				})
			})
		})
	})
}

func (a *App) trayProblemText() string {
	if a.trayErr != nil {
		return "状态栏图标创建失败：" + a.trayErr.Error()
	}
	return "正在创建状态栏图标…"
}

func (a *App) settingsCard(c ui.C, title string, body func()) {
	theme := c.Theme()
	card := ui.Column(c).FillWidth().Pad(16).Gap(12).Radius(theme.Metrics.RadiusSurface)
	card.Background(glass(*theme))
	card.Border(1, glassBorder(*theme))
	if isDark(*theme) {
		card.Shadow(gfx.ShadowSM)
	} else {
		card.Shadow(gfx.ShadowXS)
	}
	card.Children(func() {
		ui.Text(c, title).FontSize(14).Weight(gfx.WeightMedium)
		body()
	})
}
