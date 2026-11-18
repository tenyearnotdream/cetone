//go:build vivashots && live

package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/headless"
	"ncmviva/internal/store"
)

func TestLivePagesRender(t *testing.T) {
	root := t.TempDir()
	a, err := New(store.Paths{Config: root + "/c", Data: root + "/d", Cache: root + "/k"})
	if err != nil {
		t.Fatal(err)
	}
	_, config := a.Page(app.Config{Title: "t", Width: 1160, Height: 740})
	session, err := headless.Open(config, nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && !a.data.discover.loaded {
		a.sync()
		session.Driver().Pump()
		time.Sleep(30 * time.Millisecond)
	}
	// Let the cover requests come back too.
	for i := 0; i < 100; i++ {
		a.sync()
		session.Driver().Pump()
		time.Sleep(30 * time.Millisecond)
	}
	t.Logf("banners=%d playlists=%d albums=%d covers=%d", len(a.data.discover.banners), len(a.data.discover.playlists), len(a.data.discover.newAlbums), len(a.bitmaps))
	if err := session.Screenshot("/tmp/shots/live-discover.png"); err != nil {
		t.Fatal(err)
	}
	problems := headless.CheckLayout(session.Snapshot())
	fmt.Printf("problems: %d\n", len(problems))
	for _, p := range problems {
		fmt.Printf("  %s %q %v\n", p.Kind, p.Text, p.Bounds)
	}
}
