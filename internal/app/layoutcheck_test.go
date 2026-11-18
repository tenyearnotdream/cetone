//go:build vivashots

package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/headless"
	"ncmviva/internal/store"
)

func TestLayoutReport(t *testing.T) {
	root := t.TempDir()
	a := NewWithFixture(store.Paths{Config: root + "/c", Data: root + "/d", Cache: root + "/k"})
	for _, page := range []string{"discover", "toplist", "playlist", "settings", "lyrics"} {
		a.ShowForShots(page)
		a.SyncForShots()
		_, config := a.Page(app.Config{Title: "t", Width: 1160, Height: 740})
		config.Invalidator = &app.Invalidator{}
		session, err := headless.Open(config, nil, headless.Options{Scale: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Advance(20 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		problems := headless.CheckLayout(session.Snapshot())
		fmt.Printf("== %s: %d problems\n", page, len(problems))
		for _, p := range problems {
			fmt.Printf("   %s %q %v %s\n", p.Kind, p.Text, p.Bounds, p.Detail)
		}
		session.Close()
	}
}
