//go:build linux

package mpris

// The media-controls interface is tested against a real session bus: the whole
// point of MPRIS is that another program can find and drive the player, and a
// fake bus would not prove that.

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func sessionBus(t *testing.T) *dbus.Conn {
	t.Helper()
	conn, err := dbus.SessionBus()
	if err != nil {
		t.Skipf("no session bus: %v", err)
	}
	return conn
}

// getProperty reads one property and stores its value into out.
func getProperty(t *testing.T, bus *dbus.Conn, name, iface, property string, out any) {
	t.Helper()
	object := bus.Object("org.mpris.MediaPlayer2."+name, "/org/mpris/MediaPlayer2")
	var variant dbus.Variant
	if err := object.Call("org.freedesktop.DBus.Properties.Get", 0, iface, property).Store(&variant); err != nil {
		t.Fatalf("%s.%s: %v", iface, property, err)
	}
	if err := dbus.Store([]any{variant.Value()}, out); err != nil {
		t.Fatalf("%s.%s: decode: %v", iface, property, err)
	}
}

func TestServerExportsThePlayerInterface(t *testing.T) {
	bus := sessionBus(t)
	called := make(chan string, 4)
	server, err := New(
		Identity{Name: "ncmviva-test", Description: "test", DesktopEntry: "ncmviva"},
		Handlers{
			PlayPause: func() { called <- "playpause" },
			Next:      func() { called <- "next" },
			Seek:      func(offset Duration) { called <- "seek" },
		},
	)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer server.Close()

	// The name the desktop's widget looks for.
	var owner string
	if err := bus.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		Call("org.freedesktop.DBus.GetNameOwner", 0, "org.mpris.MediaPlayer2.ncmviva-test").Store(&owner); err != nil {
		t.Fatalf("the player name is not on the bus: %v", err)
	}

	// The properties a client reads before it shows anything.
	var identity string
	getProperty(t, bus, "ncmviva-test", "org.mpris.MediaPlayer2", "Identity", &identity)
	if identity != "ncmviva-test" {
		t.Fatalf("Identity is %q", identity)
	}
	var status string
	getProperty(t, bus, "ncmviva-test", "org.mpris.MediaPlayer2.Player", "PlaybackStatus", &status)
	if status != "Stopped" {
		t.Fatalf("PlaybackStatus starts as %q", status)
	}

	// The controls reach the handlers.
	player := bus.Object("org.mpris.MediaPlayer2.ncmviva-test", "/org/mpris/MediaPlayer2")
	if err := player.Call("org.mpris.MediaPlayer2.Player.PlayPause", 0).Err; err != nil {
		t.Fatalf("PlayPause: %v", err)
	}
	if err := player.Call("org.mpris.MediaPlayer2.Player.Next", 0).Err; err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := player.Call("org.mpris.MediaPlayer2.Player.Seek", 0, int64(5_000_000)).Err; err != nil {
		t.Fatalf("Seek: %v", err)
	}
	for _, want := range []string{"playpause", "next", "seek"} {
		select {
		case got := <-called:
			if got != want {
				t.Fatalf("the handler for %q ran as %q", want, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("the %q handler did not run", want)
		}
	}
}

func TestUpdatePublishesMetadata(t *testing.T) {
	bus := sessionBus(t)
	server, err := New(Identity{Name: "ncmviva-meta-test"}, Handlers{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer server.Close()
	server.Update(Track{ID: "347230", Title: "海阔天空", Artist: "Beyond", Album: "乐与怒", Length: 325 * Duration(time.Second)}, Playing, 42*Duration(time.Second), 0.7)

	var metadata map[string]dbus.Variant
	getProperty(t, bus, "ncmviva-meta-test", "org.mpris.MediaPlayer2.Player", "Metadata", &metadata)
	if got := metadata["xesam:title"].Value(); got != "海阔天空" {
		t.Fatalf("title is %v", got)
	}
	artists, ok := metadata["xesam:artist"].Value().([]string)
	if !ok || len(artists) != 1 || artists[0] != "Beyond" {
		t.Fatalf("artists are %v", metadata["xesam:artist"].Value())
	}
	if got := metadata["mpris:length"].Value(); got != int64(325*time.Second) {
		t.Fatalf("length is %v", got)
	}

	var status string
	getProperty(t, bus, "ncmviva-meta-test", "org.mpris.MediaPlayer2.Player", "PlaybackStatus", &status)
	if status != "Playing" {
		t.Fatalf("PlaybackStatus is %q after Update", status)
	}
}

func TestVolumePropertyReachesTheHandler(t *testing.T) {
	bus := sessionBus(t)
	volumes := make(chan float64, 2)
	server, err := New(Identity{Name: "ncmviva-vol-test"}, Handlers{
		SetVolume: func(volume float64) { volumes <- volume },
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer server.Close()

	player := bus.Object("org.mpris.MediaPlayer2.ncmviva-vol-test", "/org/mpris/MediaPlayer2")
	if err := player.Call("org.freedesktop.DBus.Properties.Set", 0,
		"org.mpris.MediaPlayer2.Player", "Volume", dbus.MakeVariant(0.25)).Err; err != nil {
		t.Fatalf("set volume: %v", err)
	}
	select {
	case got := <-volumes:
		if got != 0.25 {
			t.Fatalf("the handler got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the volume handler did not run")
	}
}
