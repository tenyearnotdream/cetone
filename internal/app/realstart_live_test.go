//go:build live

package app

import (
	"testing"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/headless"

	"cetone/internal/store"
)

// TestRealHomeStartsWithTheSavedAccount opens the window on this machine's real
// directories and reads the frame: what a user sees when they start the program
// after having logged in once.
func TestRealHomeStartsWithTheSavedAccount(t *testing.T) {
	paths := store.DefaultPaths()
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Close)

	theme := gfx.LightTheme()
	session, err := headless.Open(uiNewPage(a, &theme), nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for i := 0; i < 300; i++ {
		if err := session.Driver().Pump(); err != nil {
			t.Fatal(err)
		}
		if a.data.discover.loaded {
			break
		}
	}
	texts := session.Texts()
	joined := ""
	for _, x := range texts {
		joined += x + " | "
	}
	t.Logf("state: user=%q uid=%d loggedIn=%v", a.user.Nickname, a.user.UserID, a.client.LoggedIn())
	t.Logf("discover err: %q", a.data.discover.err)
	t.Logf("frame: %s", joined)
	if a.user.UserID == 0 {
		t.Fatal("the saved account was not restored")
	}
	if !session.HasText(a.user.Nickname) {
		t.Fatalf("the sidebar does not show the account %q", a.user.Nickname)
	}
	if session.HasText("扫码登录") {
		t.Fatal("the sidebar offers 扫码登录 although an account is restored")
	}
}
