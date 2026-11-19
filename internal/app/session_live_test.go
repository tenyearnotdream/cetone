//go:build live

package app

// The login has to survive a restart. These tests use a real session file and
// the real API, because "remembered" is a claim about the server as much as
// about the disk.

import (
	"testing"
	"time"

	"cetone/internal/store"
)

// TestTheDeviceIdentitySurvivesRestarts is the regression: the client
// regenerated its device id on every launch, so the server saw a new device each
// time and the login was not remembered against anything.
func TestTheDeviceIdentitySurvivesRestarts(t *testing.T) {
	paths := store.DefaultPaths()
	saved, ok, err := store.NewSessions(paths).Load()
	if err != nil || !ok {
		t.Skip("this machine has no saved session")
	}
	before := saved.Cookies["_ntes_nuid"]
	if before == "" {
		t.Skip("the saved session carries no device id")
	}

	// Two starts in a row, each making a request, as two launches would.
	var seen []string
	for run := 0; run < 2; run++ {
		a, err := New(paths)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.client.LoginStatus(t.Context()); err != nil {
			t.Fatalf("run %d: LoginStatus: %v", run, err)
		}
		got := a.client.Cookies()["_ntes_nuid"]
		seen = append(seen, got)
		a.engine.Close()
	}
	t.Logf("saved device id: %s", before)
	t.Logf("run 1: %s", seen[0])
	t.Logf("run 2: %s", seen[1])
	if seen[0] != before || seen[1] != before {
		t.Fatalf("the device id changed across starts: saved %s, runs %v", before, seen)
	}
}

// TestTheSessionFileKeepsOnlySessionCookies checks that the per-request fields
// (the transport's os/appver/osver) are not written into the session file: they
// describe one request, and a file that carries them outlives their meaning.
func TestTheSessionFileKeepsOnlySessionCookies(t *testing.T) {
	paths := store.DefaultPaths()
	saved, ok, err := store.NewSessions(paths).Load()
	if err != nil || !ok {
		t.Skip("this machine has no saved session")
	}
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer a.engine.Close()
	if _, err := a.client.LoginStatus(t.Context()); err != nil {
		t.Fatalf("LoginStatus: %v", err)
	}
	// Save what a quit would save, then read it back.
	a.persistSession()
	reloaded, ok, err := store.NewSessions(paths).Load()
	if err != nil || !ok {
		t.Fatalf("reload: %v %v", ok, err)
	}
	for _, name := range []string{"os", "appver", "osver", "deviceId"} {
		if _, present := reloaded.Cookies[name]; present {
			t.Errorf("the session file carries the per-request field %q", name)
		}
	}
	if reloaded.Cookies["MUSIC_U"] == "" {
		t.Error("the session file lost the login token")
	}
	if reloaded.Cookies["_ntes_nuid"] != saved.Cookies["_ntes_nuid"] {
		t.Error("the session file lost the device id")
	}
	t.Logf("saved %d cookies, login token %d bytes", len(reloaded.Cookies), len(reloaded.Cookies["MUSIC_U"]))
}

// TestASessionRefreshKeepsTheLogin runs the startup validation: it must keep a
// working login and refresh what the server answers.
func TestASessionRefreshKeepsTheLogin(t *testing.T) {
	paths := store.DefaultPaths()
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer a.engine.Close()
	if a.user.UserID == 0 {
		t.Skip("this machine has no saved session")
	}
	before := a.user.UserID
	a.refreshSession()
	waitFor(t, a, func() bool { return a.work.quiet() })
	// The refresh runs on a worker; give it the moment it needs to land.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if a.user.UserID == before {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("after refresh: user=%q uid=%d", a.user.Nickname, a.user.UserID)
	if a.user.UserID != before {
		t.Fatalf("the refresh dropped a working login: %d -> %d", before, a.user.UserID)
	}
}
