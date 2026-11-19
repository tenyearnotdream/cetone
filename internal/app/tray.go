package app

// The status item, and background running with it.
//
// A music player that quits when its window closes stops the music, which is
// what the tray is for: closing the window hides it, the player keeps playing
// and the tray still offers transport controls. The window can always be
// brought back, from the tray or by starting the program again.
//
// The item is a StatusNotifierItem, which is what a Linux desktop's tray shows.
// A desktop without a host for one (GNOME without its extension, say) reports
// app.ErrNoStatusArea. Then the window is NOT trapped: closing it quits, as
// it would in any other program, because a hidden window with no icon anywhere
// is an application the user cannot get back.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/png"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"

	"cetone/internal/assets"
)

// trayIconSize is the icon the status area is given: 32 pixels stays sharp on
// the densest bars, and the platform scales it down to its own size.
const trayIconSize = 32

// iconPNG is the artwork for the current appearance.
func (a *App) iconPNG() []byte {
	if a.theme.Background.Red+a.theme.Background.Green+a.theme.Background.Blue > 1.5 {
		return assets.IconLightPNG
	}
	return assets.IconPNG
}

// startTray exports the status item. It is called on the application's startup
// thread, and a machine with no status area is not an error: the program simply
// has no tray, and closing the window quits.
func (a *App) startTray() {
	icon, err := statusIconFromPNG(a.iconPNG(), trayIconSize)
	if err != nil {
		a.trayErr = err
		return
	}
	item, err := a.application.AddStatusItem(app.StatusItemConfig{
		Tooltip: a.trayTooltip(),
		Icon:    icon,
		// A primary press brings the window back, which is what a click on a
		// player's tray icon does on every desktop that sends it here.
		OnClick: func(app.StatusClick) { a.trayShowWindow() },
		// The menu is asked for each time it opens, so it shows the song and
		// the transport state at that moment.
		Menu: a.trayMenu,
	})
	if err != nil {
		a.trayErr = err
		a.trayUnavailable = errors.Is(err, app.ErrNoStatusArea)
		if !a.trayUnavailable {
			a.trayErr = fmt.Errorf("状态栏图标：%w", err)
		}
		return
	}
	a.tray = item
	a.trayAvailable = true
	a.updateTray()
}

// updateTray refreshes what the item shows without opening it.
func (a *App) updateTray() {
	if a.tray == nil {
		return
	}
	a.tray.SetTooltip(a.trayTooltip())
	// The tile follows the appearance, as the window icon does.
	if icon, err := statusIconFromPNG(a.iconPNG(), trayIconSize); err == nil {
		_ = a.tray.SetIcon(icon)
	}
}

// trayTooltip is the song the pointer resting on the icon reads.
func (a *App) trayTooltip() string {
	song, ok := a.current()
	if !ok {
		return "Cetone"
	}
	status := a.engine.Snapshot().Status.String()
	state := "已暂停"
	switch status {
	case "playing", "buffering":
		state = "正在播放"
	case "failed":
		state = "播放失败"
	}
	return fmt.Sprintf("Cetone · %s - %s（%s）", song.Name, song.Singer(), state)
}

// trayMenu is the item's menu, built as it opens.
func (a *App) trayMenu() []app.MenuItem {
	song, hasSong := a.current()
	playing := a.engine.Snapshot().Status.String() == "playing" ||
		a.engine.Snapshot().Status.String() == "buffering"

	now := "未在播放"
	if hasSong {
		now = song.Name + " - " + song.Singer()
	}
	playLabel := "播放"
	if playing {
		playLabel = "暂停"
	}
	windowLabel := "显示主窗口"
	if a.window != nil && !a.window.Hidden() {
		windowLabel = "隐藏到托盘"
	}

	// The entries run on the event-loop thread. Each one publishes work for the
	// window thread, which is where the model lives, and wakes it.
	return []app.MenuItem{
		{Title: now, Disabled: true, ID: "now"},
		{Separator: true},
		{Title: "上一首", OnSelect: a.trayDo(a.previous), ID: "previous"},
		{Title: playLabel, OnSelect: a.trayDo(a.togglePlay), ID: "playpause"},
		{Title: "下一首", OnSelect: a.trayDo(a.next), ID: "next"},
		{Separator: true},
		{Title: windowLabel, OnSelect: a.trayToggleWindow, ID: "window"},
		{Separator: true},
		{Title: "退出", OnSelect: a.trayDo(a.quit), ID: "quit"},
	}
}

// trayDo wraps a model change as a menu entry's action.
//
// The menu runs on the window's event-loop thread, which is the thread that
// paints, so the change is applied and the queue drained here rather than left
// for a repaint: a hidden window is not painted, and waiting for one would make
// a tray action do nothing until the window was shown by other means.
func (a *App) trayDo(action func()) func() {
	return func() {
		a.work.push(action)
		a.sync()
		a.invalidate()
	}
}

// trayShowWindow brings the window back, from a menu entry or a press on the
// icon. It acts at once: the status item's callbacks run on the window's own
// thread, and a hidden window cannot wait for a repaint to be shown.
func (a *App) trayShowWindow() {
	a.sync()
	if a.window == nil || a.window.Closed() {
		// The window was really closed (the tray was turned off since): the
		// program has nothing to show and no reason to keep running.
		a.quit()
		return
	}
	a.window.Show()
	a.invalidate()
}

// trayToggleWindow hides a visible window and shows a hidden one.
func (a *App) trayToggleWindow() {
	a.sync()
	if a.window == nil || a.window.Closed() {
		return
	}
	if a.window.Hidden() {
		a.window.Show()
	} else {
		a.window.Hide()
	}
	a.invalidate()
}

// handleCloseRequest intercepts the window's close: with a tray to come back
// from, closing hides the window and the music keeps playing. Without one, the
// window closes and the program ends, which is what a user expects of a window
// that has nowhere to hide.
func (a *App) handleCloseRequest(allow func()) {
	if a.trayAvailable && a.config.CloseToTray {
		// The request runs on the window thread, so hiding happens here; the
		// window is not painted once hidden, so nothing may wait for a repaint.
		if a.window != nil && !a.window.Closed() {
			a.window.Hide()
		}
		return
	}
	allow()
}

// stopTray removes the icon, so a quit leaves nothing behind in the status area.
func (a *App) stopTray() {
	if a.tray != nil {
		a.tray.Remove()
		a.tray = nil
	}
	a.trayAvailable = false
}

// brandBitmap is the application's icon as a bitmap for the page's own header.
// It is decoded once: the header is declared on every frame.
func (a *App) brandBitmap() gfx.Bitmap {
	// The header shows the icon of the current appearance, so a theme change
	// drops the decoded copy.
	dark := isDark(a.theme)
	if a.brand.Decoded && a.brand.Dark == dark {
		return a.brand.Bitmap
	}
	a.brand.Decoded = true
	a.brand.Dark = dark
	src, _, err := image.Decode(bytes.NewReader(a.iconPNG()))
	if err != nil {
		return gfx.Bitmap{}
	}
	bitmap := bitmapFromImage(src, 56, 56)
	bitmap.ID = nextBitmapID()
	a.brand.Bitmap = bitmap
	return bitmap
}

// windowIcon is the application's picture for the taskbar and Alt+Tab: the same
// artwork as the status item, at the size a desktop shows large icons. A failure
// to read it is not fatal: the window opens with the desktop's default icon.
func (a *App) windowIcon() (app.WindowIcon, error) {
	src, _, err := image.Decode(bytes.NewReader(a.iconPNG()))
	if err != nil {
		return app.WindowIcon{}, fmt.Errorf("窗口图标无法解码：%w", err)
	}
	pixels, err := straightRGBA(src, 512)
	if err != nil {
		return app.WindowIcon{}, err
	}
	return app.WindowIcon{Size: 512, RGBA: pixels}, nil
}

// statusIconFromPNG decodes an icon and scales it to a square of the given size
// as the straight (not premultiplied) RGBA a status area expects.
func statusIconFromPNG(data []byte, size int) (app.StatusIcon, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return app.StatusIcon{}, fmt.Errorf("状态栏图标无法解码：%w", err)
	}
	pixels, err := straightRGBA(src, size)
	if err != nil {
		return app.StatusIcon{}, err
	}
	return app.StatusIcon{Size: size, RGBA: pixels}, nil
}

// straightRGBA scales an image to size×size straight RGBA. The scaling is an
// area average, the same one the covers use, so a rounded icon keeps its corners
// soft instead of jagged.
func straightRGBA(src image.Image, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("状态栏图标尺寸无效：%d", size)
	}
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return nil, fmt.Errorf("状态栏图标是空的")
	}
	out := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		y0 := y * srcHeight / size
		y1 := (y + 1) * srcHeight / size
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < size; x++ {
			x0 := x * srcWidth / size
			x1 := (x + 1) * srcWidth / size
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha uint64
			var count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, a := src.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
					red += uint64(r >> 8)
					green += uint64(g >> 8)
					blue += uint64(b >> 8)
					alpha += uint64(a >> 8)
					count++
				}
			}
			if count == 0 {
				continue
			}
			dst := (y*size + x) * 4
			out[dst] = uint8(red / count)
			out[dst+1] = uint8(green / count)
			out[dst+2] = uint8(blue / count)
			out[dst+3] = uint8(alpha / count)
		}
	}
	return out, nil
}
