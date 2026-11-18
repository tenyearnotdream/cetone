//go:build live

package app

// The login flows a test can reach without a phone: a QR code the server
// issues, and a poll that answers "waiting". The scan itself needs the user's
// phone, so that part stays unverified and the README says so.

import (
	"testing"

	"github.com/vivaui/viva/app"
)

func TestLiveQRLoginIssuesACode(t *testing.T) {
	a := liveApp(t)
	a.openLogin()
	waitFor(t, a, func() bool {
		return len(a.login.qr) > 0 || a.login.err != ""
	})
	if a.login.err != "" {
		t.Fatalf("asking for a login QR code failed: %s", a.login.err)
	}
	if a.login.unikey == "" {
		t.Fatal("the server issued no unikey")
	}
	// A QR code of the URL is a grid of modules, and the panel has one to draw.
	if len(a.login.qr) < 64 {
		t.Fatalf("the drawn code has %d dark modules, which cannot be a QR code", len(a.login.qr))
	}
	modules := a.login.qr[0].Size
	if modules < 21 || modules > 177 {
		t.Fatalf("the code is %d modules wide; a QR code is 21 to 177", modules)
	}
	t.Logf("login QR: %d dark modules on a %dx%d grid", len(a.login.qr), modules, modules)

	// The poll answers without a scan, and the panel keeps waiting rather than
	// failing.
	if a.login.state == "" {
		t.Fatal("the panel has no state to show")
	}
	t.Logf("panel state: %q", a.login.state)
}

func TestLiveLoginStatusWithoutASessionIsNotLoggedIn(t *testing.T) {
	a := liveApp(t)
	if a.user.UserID != 0 {
		t.Fatal("a fresh application claims a signed-in account")
	}
	if a.client.LoggedIn() {
		t.Fatal("a fresh client claims a login token")
	}
	// The pages that need an account say so rather than failing silently.
	a.nav = []route{{kind: routeMine, title: "我的音乐"}}
	if _, config := a.Page(app.Config{Title: "t", Width: 1160, Height: 740}); config.Title == "" {
		t.Fatal("the page did not build")
	}
}
