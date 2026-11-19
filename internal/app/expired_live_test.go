//go:build live

package app

import (
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/store"
)

// TestAnExpiredSessionIsReportedAsExpired is the other half of "remembered": a
// session the server no longer accepts must stop looking like a login, instead of
// leaving an account whose every request is refused.
func TestAnExpiredSessionIsReportedAsExpired(t *testing.T) {
	root := t.TempDir()
	paths := store.Paths{Config: root + "/config", Data: root + "/data", Cache: root + "/cache"}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	// A session file that claims an account, with a token the server rejects.
	sessions := store.NewSessions(paths)
	err := sessions.Save(store.Session{
		Cookies: map[string]string{
			"MUSIC_U":       "this-is-not-a-valid-token-0000000000",
			"_ntes_nuid":    "3d1bf8a54a9cfeee9924553844c889d1",
			"_ntes_nnid":    "3d1bf8a54a9cfeee9924553844c889d1,1794990000000",
			"NMTID":         "925cbb580a57bef2",
			"__remember_me": "true",
		},
		User: storeSessionUser(),
	})
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Close)
	if a.user.UserID == 0 {
		t.Fatal("the fixture session was not loaded")
	}

	a.refreshSession()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.sync()
		if a.user.UserID == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.user.UserID != 0 {
		t.Fatalf("an invalid token kept the account: %q uid=%d", a.user.Nickname, a.user.UserID)
	}
	if _, ok, _ := sessions.Load(); ok {
		t.Fatal("the expired session was left on disk")
	}
	t.Logf("the window now offers login again, and the file is gone")
}

func storeSessionUser() ncm.UserInfo {
	return ncm.UserInfo{UserID: 131093126, Nickname: "自由之石"}
}
