//go:build live

package app

import (
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/headless"

	"cetone/internal/store"
)

// TestFreshInstallDiscoverLoads opens the window exactly as the release binary
// does, on a brand-new HOME, and waits for the discover page to fill.
func TestFreshInstallDiscoverLoads(t *testing.T) {
	root := t.TempDir()
	a, err := New(store.Paths{Config: root + "/config", Data: root + "/data", Cache: root + "/cache"})
	if err != nil {
		t.Fatal(err)
	}
	// Cover art keeps arriving after the frame is measured; the directory must
	// be quiescent before t.TempDir's cleanup runs, so the downloads are given a
	// moment and the removal tolerates what is left.
	t.Cleanup(func() {
		a.engine.Close()
		time.Sleep(500 * time.Millisecond)
	})

	theme := gfx.LightTheme()
	session, err := headless.Open(uiNewPage(a, &theme), nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	start := time.Now()
	// Only Pump: it applies the window's wake (Config.OnWake = a.sync) and then
	// repaints, which is the order a real window runs in. Draining the queue by
	// hand first would consume the wake's work and leave the repaint stale.
	for i := 0; i < 600; i++ {
		if err := session.Driver().Pump(); err != nil {
			t.Fatal(err)
		}
		if a.data.discover.loaded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)
	d := a.data.discover
	t.Logf("after %v: loaded=%v busy=%v err=%q banners=%d playlists=%d albums=%d",
		elapsed, d.loaded, d.busy, d.err, len(d.banners), len(d.playlists), len(d.newAlbums))
	if !d.loaded {
		t.Fatalf("the discover page never finished loading")
	}
	texts := session.Texts()
	t.Logf("frame texts: %v", texts)
	if session.HasText("正在加载推荐内容") {
		t.Fatal("the page still shows the loading state after loading finished")
	}
	if !session.HasText("推荐歌单") {
		t.Fatalf("the discover content is not on screen; it has %v", texts)
	}
}

// uiNewPage builds the config a headless session opens the window with. It is
// the one Run uses, not a variant: a test that built its own config could pass
// while the shipped window failed (it did, before Page set OnWake).
func uiNewPage(a *App, theme *gfx.Theme) app.Config {
	_, config := a.Page(app.Config{Title: "cetone", Width: 1160, Height: 740})
	return config
}
