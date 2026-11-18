//go:build vivashots

// Command shots renders the application's pages without opening a window, so a
// change can be looked at in both themes. It is a development tool: it fills the
// model with a fixed fixture (no network, no audio device) and writes PNGs.
//
//	go run ./cmd/shots /tmp/shots
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/headless"

	ncmapp "cetone/internal/app"
	"cetone/internal/store"
)

func main() {
	dir := "/tmp/cetone-shots"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		suffix := "light"
		if dark {
			suffix = "dark"
		}
		shoot(dir, suffix, dark)
	}
}

// shoot renders each page of one theme.
func shoot(dir, suffix string, dark bool) {
	root, err := os.MkdirTemp("", "cetone-shots")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)
	paths := store.Paths{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		Cache:  filepath.Join(root, "cache"),
	}
	a := ncmapp.NewWithFixture(paths)
	if dark {
		a.SetThemeForShots("dark")
	} else {
		a.SetThemeForShots("light")
	}
	page, config := a.Page(app.Config{Title: "网易云音乐", Width: 1160, Height: 740})
	session, err := headless.Open(config, nil, headless.Options{Scale: 1})
	if err != nil {
		log.Fatalf("%s: %v", suffix, err)
	}
	defer session.Close()
	_ = page

	pages := []struct {
		name string
		open func()
	}{
		{"discover", func() { a.ShowForShots("discover") }},
		{"toplist", func() { a.ShowForShots("toplist") }},
		{"playlist", func() { a.ShowForShots("playlist") }},
		{"settings", func() { a.ShowForShots("settings") }},
		{"lyrics", func() { a.ShowForShots("lyrics") }},
		{"search", func() { a.ShowForShots("search") }},
		{"queue", func() { a.ShowForShots("queue") }},
		{"login", func() { a.ShowForShots("login") }},
	}
	for _, p := range pages {
		p.open()
		a.SyncForShots()
		// A driver only repaints when it is asked to; a page change is not an
		// event it sees.
		if err := session.Frame(); err != nil {
			log.Fatalf("%s/%s: %v", suffix, p.name, err)
		}
		// A dialog and a hover fade in, so the frame is settled before it is
		// captured.
		for i := 0; i < 15; i++ {
			if err := session.Advance(20 * time.Millisecond); err != nil {
				log.Fatalf("%s/%s: %v", suffix, p.name, err)
			}
			a.SyncForShots()
		}
		if err := session.Frame(); err != nil {
			log.Fatalf("%s/%s: %v", suffix, p.name, err)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%s.png", p.name, suffix))
		if err := session.Screenshot(path); err != nil {
			log.Fatalf("%s/%s: %v", suffix, p.name, err)
		}
		fmt.Println(path)
	}
}
