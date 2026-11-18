package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"

	"cetone/internal/ncm"
)

// Actions the pages call: opening the special lists, logging in and out,
// exporting a cached song, opening a path. Each starts the work a page asked for
// and leaves the model for the window thread.

// The special lists are playlists the account has but the API does not serve
// under a playlist id: they are registered under negative ids, which no real
// playlist can have, so the playlist page renders them like any other.
const (
	likedSongsID int64 = -1
	cloudDiskID  int64 = -2
	dailyRecID   int64 = -3
)

// openLikedSongs shows 我喜欢的音乐, loaded from the account's like list.
func (a *App) openLikedSongs() {
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	if len(a.data.mine.liked) == 0 {
		a.toast("正在加载", "我喜欢的音乐", ui.ToastDefault)
		a.data.mine.loaded = false
		a.loadMine()
		return
	}
	a.registerSpecial(likedSongsID, "我喜欢的音乐", "你收藏过的每一首歌。", a.data.mine.liked)
}

// registerSpecial makes a loaded song list a page and opens it.
func (a *App) registerSpecial(id int64, name, description string, songs []ncm.Song) {
	a.data.init()
	playlist := ncm.Playlist{
		ID:          id,
		Name:        name,
		Description: description,
		TrackCount:  len(songs),
		Subscribed:  true,
		Tracks:      songs,
	}
	a.data.playlists[id] = &playlistData{playlist: playlist, loading: loading{loaded: true}}
	a.push(route{kind: routePlaylist, id: id, title: name})
	a.invalidate()
}

// openDailyRecommendation plays today's recommendation.
func (a *App) openDailyRecommendation() {
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	a.playSongs(a.data.mine.daily, 0)
	if len(a.data.mine.daily) > 0 {
		a.toast("每日推荐", fmt.Sprintf("已开始播放 %d 首", len(a.data.mine.daily)), ui.ToastSuccess)
	}
}

// openPersonalFM starts the private radio.
func (a *App) openPersonalFM() {
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		songs, err := a.client.PersonalFM(ctx)
		a.work.push(func() {
			if err != nil {
				a.toast("私人FM", errText(err), ui.ToastError)
				return
			}
			if len(songs) == 0 {
				a.toast("私人FM", "没有新的推荐", ui.ToastDefault)
				return
			}
			a.playSongs(songs, 0)
		})
		a.invalidate()
		return nil
	}, nil)
}

// openCloudDisk shows the account's cloud drive.
func (a *App) openCloudDisk() {
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	a.registerSpecial(cloudDiskID, "云盘音乐", "上传到网易云音乐云盘的歌曲。", a.data.mine.cloud)
}

// openDailyRecommendationPage shows today's recommendation as a list, rather
// than starting it.
func (a *App) openDailyRecommendationPage() {
	if a.user.UserID == 0 {
		a.openLogin()
		return
	}
	a.registerSpecial(dailyRecID, "每日推荐", "根据你的口味每天生成。", a.data.mine.daily)
}

// openSearch starts a search for a keyword.
func (a *App) openSearch(keyword string) {
	if keyword == "" {
		return
	}
	a.search.field = keyword
	a.nav = []route{{kind: routeSearch, title: "搜索"}}
	a.runSearch(false)
	a.invalidate()
}

// searchResults re-runs the search for a new kind.
func (a *App) searchResults() {
	a.runSearch(false)
	a.invalidate()
}

// openLogin shows the login panel and starts a code.
func (a *App) openLogin() {
	a.login.open = true
	a.login.err = ""
	if a.login.mode == "" {
		a.login.mode = loginModeQR
	}
	if a.login.mode == loginModeQR && a.login.unikey == "" {
		a.login.state = ""
		a.startQRLogin()
	}
	a.invalidate()
}

// setLoginMode switches between the QR code and the phone number.
func (a *App) setLoginMode(mode string) {
	a.login.mode = mode
	a.login.err = ""
	if mode == loginModeQR && a.login.unikey == "" {
		a.startQRLogin()
	}
	a.invalidate()
}

// sendLoginCaptcha asks for a texted login code.
func (a *App) sendLoginCaptcha() {
	phone := strings.TrimSpace(a.login.phone)
	if len(phone) < 6 {
		a.login.err = "请先填写手机号"
		a.invalidate()
		return
	}
	a.login.busy = true
	a.login.err = ""
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := a.client.CaptchaSent(ctx, a.login.country, phone)
		a.work.push(func() {
			a.login.busy = false
			if err != nil {
				a.login.err = errText(err)
				return
			}
			a.login.sentAt = time.Now()
			a.login.method = "captcha"
			a.toast("验证码已发送", phone, ui.ToastSuccess)
		})
		a.invalidate()
		return nil
	}, nil)
}

// loginWithCaptcha signs in with the texted code.
func (a *App) loginWithCaptcha() {
	code := strings.TrimSpace(a.login.captcha)
	phone := strings.TrimSpace(a.login.phone)
	if code == "" || phone == "" {
		a.login.err = "请填写手机号和验证码"
		a.invalidate()
		return
	}
	a.login.busy = true
	a.login.err = ""
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := a.client.LoginCellphone(ctx, a.login.country, phone, code)
		a.work.push(func() {
			a.login.busy = false
			if err != nil {
				a.login.err = errText(err)
				return
			}
			a.login.open = false
			a.afterLogin()
		})
		a.invalidate()
		return nil
	}, nil)
}

// startQRLogin asks for a fresh QR code and starts polling it.
func (a *App) startQRLogin() {
	if a.login.busy {
		return
	}
	a.login.busy = true
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ticket, err := a.client.LoginQR(ctx)
		if err != nil {
			a.work.push(func() {
				a.login.busy = false
				a.login.err = errText(err)
			})
			a.invalidate()
			return nil
		}
		modules, qrErr := qrModules(ticket.QRURL)
		a.work.push(func() {
			a.login.busy = false
			if qrErr != nil {
				a.login.err = "二维码生成失败"
				return
			}
			a.login.qr = modules
			a.login.unikey = ticket.Unikey
			a.login.state = "等待扫码"
			a.pollQRLogin(ticket.Unikey, 0)
		})
		a.invalidate()
		return nil
	}, nil)
}

// pollQRLogin waits for the phone to confirm. The poll runs on a worker and
// republishes its own next step, so the window is never blocked and the loop
// ends when the panel closes or the code expires.
func (a *App) pollQRLogin(unikey string, attempts int) {
	if unikey == "" || attempts > 120 {
		return
	}
	a.background(func() error {
		time.Sleep(2 * time.Second)
		if !a.login.open || a.login.unikey != unikey {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		code, _, err := a.client.LoginQRCheck(ctx, unikey)
		a.work.push(func() {
			switch {
			case err != nil:
				a.login.state = "网络异常，重试中"
			case code == ncm.QRScanned:
				a.login.state = "已扫码，请在手机上确认"
			case code == ncm.QRSuccess:
				a.login.state = "登录成功"
				a.login.open = false
				a.afterLogin()
				return
			case code == ncm.QRExpired:
				a.login.state = "二维码已过期"
				a.login.err = "二维码已过期，请刷新"
				return
			}
			a.pollQRLogin(unikey, attempts+1)
		})
		a.invalidate()
		return nil
	}, nil)
}

// afterLogin reads the profile and refreshes what depends on the account.
func (a *App) afterLogin() {
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		user, err := a.client.LoginStatus(ctx)
		a.work.push(func() {
			if err != nil {
				a.toast("登录失败", errText(err), ui.ToastError)
				return
			}
			a.user = user
			_ = a.sessions.Save(storeSessionFrom(a.client, user))
			a.toast("登录成功", user.Nickname, ui.ToastSuccess)
			// Everything the account affects is now stale.
			a.data.mine = mineData{}
			a.data.discover.loaded = false
			a.likedSet = make(map[int64]bool)
			a.loadMine()
			a.loadLikedIDs()
			a.invalidate()
		})
		a.invalidate()
		return nil
	}, nil)
}

// logout forgets the session.
func (a *App) logout() {
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.client.Logout(ctx)
		a.work.push(func() {
			a.user = ncm.UserInfo{}
			a.likedSet = make(map[int64]bool)
			a.data.mine = mineData{}
			a.data.discover.loaded = false
			_ = a.sessions.Clear()
			a.toast("已退出登录", "", ui.ToastDefault)
			a.loadDiscover()
			a.invalidate()
		})
		a.invalidate()
		return nil
	}, nil)
}

// loginModal is the login panel, with a QR code and a phone number.
func (a *App) loginModal(c ui.C) {
	if !a.login.open {
		return
	}
	if a.login.mode == "" {
		a.login.mode = loginModeQR
	}
	theme := c.Theme()
	title := "扫码登录"
	if a.login.mode == loginModePhone {
		title = "手机号登录"
	}
	ui.Modal(c, &a.login.open, title, func() {
		// The mode is a tab strip: it writes the value it is declared with, so
		// the switch happens here rather than in a click handler.
		mode := a.login.mode
		tabs := []string{loginModeQR, loginModePhone}
		labels := map[string]string{loginModeQR: "扫码登录", loginModePhone: "手机验证码"}
		if ui.Tabs(c, &mode, tabs, func(m string) string { return labels[m] }).Changed() {
			a.setLoginMode(mode)
			return
		}
		if a.login.mode == loginModePhone {
			a.loginPhoneForm(c)
			return
		}
		a.loginQRPanel(c)
	}, func() {
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if a.login.mode == loginModeQR {
				if ui.Button(c, "刷新二维码").Outline().Clicked() {
					a.login.qr = nil
					a.login.unikey = ""
					a.startQRLogin()
				}
			}
			if ui.Button(c, "关闭").Clicked() {
				a.login.open = false
			}
		})
	})
	_ = theme
}

// loginQRPanel is the QR half of the panel.
func (a *App) loginQRPanel(c ui.C) {
	theme := c.Theme()
	ui.Column(c).Gap(14).Items(ui.Center).Children(func() {
		// The code itself, drawn from the module grid.
		ui.Row(c).Size(200, 200).Radius(8).Background(gfx.RGB(255, 255, 255)).Center().Children(func() {
			if len(a.login.qr) == 0 {
				if a.login.busy {
					ui.Spinner(c)
				} else {
					ui.Text(c, "二维码加载失败").FontSize(12).Muted()
				}
				return
			}
			ui.Slot(c).Size(184, 184).Draw(func(d *ui.DrawContext) {
				drawQR(d, a.login.qr, d.Rect)
			})
		})
		if a.login.state != "" {
			ui.Text(c, a.login.state).FontSize(12).Muted()
		}
		if a.login.err != "" {
			ui.Text(c, a.login.err).FontSize(12).Color(theme.Danger)
		}
		ui.Text(c, "用网易云音乐 App 扫描二维码登录").FontSize(11).Muted()
	})
}

// loginPhoneForm is the phone-number half of the panel.
func (a *App) loginPhoneForm(c ui.C) {
	theme := c.Theme()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
			ui.TextField(c, &a.login.country, "国家码", "86").Width(72)
			phone := ui.TextField(c, &a.login.phone, "手机号", "手机号码")
			phone.Grow(1)
			phone.AutoFocus()
		})
		ui.Row(c).Gap(8).Items(ui.Center).Children(func() {
			code := ui.TextField(c, &a.login.captcha, "验证码", "短信验证码")
			code.Grow(1)
			if code.Submitted() {
				a.loginWithCaptcha()
			}
			remaining := 0
			if !a.login.sentAt.IsZero() {
				remaining = 60 - int(time.Since(a.login.sentAt).Seconds())
			}
			label := "获取验证码"
			if remaining > 0 {
				label = fmt.Sprintf("%d 秒后重发", remaining)
			}
			button := ui.Button(c, label).Outline().Disabled(a.login.busy || remaining > 0)
			if button.Clicked() {
				a.sendLoginCaptcha()
			}
		})
		if a.login.err != "" {
			ui.Text(c, a.login.err).FontSize(12).Color(theme.Danger).MaxLines(2)
		}
		ui.Text(c, "验证码登录需要先在手机号所属账户上开启短信登录。").FontSize(11).Muted()
		if ui.Button(c, "登录").FillWidth().Disabled(a.login.busy).Clicked() {
			a.loginWithCaptcha()
		}
	})
}

// exportCached copies a cached song to a path the user picks. Keyboard-free
// dialogs would need a file picker Viva does not offer, so the destination is
// the account's music folder with the song's own name.
func (a *App) exportCached(song ncm.Song, path string, quality ncm.Quality) {
	destination := a.exportPath(song)
	a.background(func() error {
		source, _, ok := a.cache.Lookup(song.ID, quality)
		if !ok {
			return fmt.Errorf("缓存文件已不存在")
		}
		return copyFile(source, destination)
	}, func(err error) {
		if err != nil {
			a.toast("导出失败", errText(err), ui.ToastError)
			return
		}
		a.toast("已导出", destination, ui.ToastSuccess)
	})
	a.invalidate()
}

// copySongLink puts the web link of a song on the clipboard.
func (a *App) copySongLink(song ncm.Song) {
	link := fmt.Sprintf("https://music.163.com/#/song?id=%d 《%s》 - %s", song.ID, song.Name, song.Singer())
	if err := appSetClipboard(link); err != nil {
		a.toast("复制失败", errText(err), ui.ToastError)
		return
	}
	a.toast("已复制链接", link, ui.ToastSuccess)
	a.invalidate()
}

// cachePlaylist asks for every song's URL so each is stored as it plays; a
// background worker downloads them one at a time into the cache.
func (a *App) cachePlaylist(playlist ncm.Playlist) {
	if len(playlist.Tracks) == 0 {
		return
	}
	a.toast("开始缓存", fmt.Sprintf("%d 首，下载完成后可离线播放", len(playlist.Tracks)), ui.ToastDefault)
	quality := a.config.PreferredQuality()
	songs := append([]ncm.Song(nil), playlist.Tracks...)
	a.background(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		done := 0
		for _, song := range songs {
			if _, _, ok := a.cache.Lookup(song.ID, quality); ok {
				continue
			}
			urls, err := a.client.SongURLs(ctx, []int64{song.ID}, quality)
			if err != nil || len(urls) == 0 || urls[0].URL == "" {
				continue
			}
			if err := downloadTo(ctx, urls[0].URL, a.cache.Path(song.ID, urls[0].Quality)); err != nil {
				continue
			}
			done++
			a.work.push(func() {
				a.toast("缓存中", fmt.Sprintf("已完成 %d/%d", done, len(songs)), ui.ToastDefault)
			})
			a.invalidate()
		}
		a.work.push(func() {
			a.toast("缓存完成", fmt.Sprintf("%d 首已保存到本地", done), ui.ToastSuccess)
		})
		a.invalidate()
		return nil
	}, nil)
}

// openPath asks the desktop to show a directory.
func (a *App) openPath(path string) {
	if err := appOpenPath(path); err != nil {
		a.toast("打不开", errText(err), ui.ToastError)
	}
}

// quit closes the window.
func (a *App) quit() {
	a.persistQueue()
	a.stopMPRIS()
	if a.window != nil {
		a.window.Close()
		return
	}
	if a.application != nil {
		a.application.Quit()
	}
}

// restoreQueue resumes the queue from the last run, paused at the position it
// stopped at. It runs once the window is open.
func (a *App) restoreQueue() {
	queue, ok, err := a.queues.Load()
	if err != nil || !ok || len(queue.Songs) == 0 {
		return
	}
	a.queue = queue.Songs
	a.queueAt = queue.Index
	if queue.Position > 0 {
		// The position is restored when the user presses play: starting audio
		// by itself when a window opens is not what a music player should do.
		a.resumeAt = queue.Position
	}
	a.invalidate()
}

// openBanner follows a carousel slide to what it points at.
func (a *App) openBanner(banner ncm.Banner) {
	switch banner.TargetType {
	case 1, 4:
		// A song: play it in place of the queue's current context.
		a.background(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			songs, err := a.client.SongsDetail(ctx, []int64{banner.TargetID})
			a.work.push(func() {
				if err != nil || len(songs) == 0 {
					a.toast("打不开推荐位", errText(err), ui.ToastError)
					return
				}
				a.playSongs(songs, 0)
			})
			a.invalidate()
			return nil
		}, nil)
	case 10:
		a.push(route{kind: routeAlbum, id: banner.TargetID, title: banner.Title})
	default:
		// An activity or an unknown target has no page of its own here; the
		// link opens in the system browser.
		if banner.URL != "" {
			if err := appOpenURL(banner.URL); err != nil {
				a.toast("打不开链接", errText(err), ui.ToastError)
			}
		}
	}
}
