//go:build live

package app

import (
	"testing"
	"time"

	"cetone/internal/store"
)

// TestSavedSessionStillAuthorises uses the session a real run saved and asks the
// API whether it is still a login, which is what a restart does.
func TestSavedSessionStillAuthorises(t *testing.T) {
	paths := store.DefaultPaths()
	sessions := store.NewSessions(paths)
	session, ok, err := sessions.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ok {
		t.Skip("this machine has no saved session")
	}
	t.Logf("saved session: %q uid=%d, %d cookies, saved %v",
		session.User.Nickname, session.User.UserID, len(session.Cookies), session.SavedAt)

	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.engine.Close)
	t.Logf("after New: user=%q uid=%d clientLoggedIn=%v",
		a.user.Nickname, a.user.UserID, a.client.LoggedIn())

	user, err := a.client.LoginStatus(t.Context())
	if err != nil {
		t.Fatalf("the saved session does not authorise: %v", err)
	}
	t.Logf("LoginStatus -> %q uid=%d", user.Nickname, user.UserID)
	if user.UserID == 0 {
		t.Fatal("LoginStatus returned no user")
	}

	// What the UI would load next.
	playlists, err := a.client.UserPlaylists(t.Context(), user.UserID, 0, 5)
	if err != nil {
		t.Fatalf("user playlists: %v", err)
	}
	t.Logf("%d playlists", len(playlists))
	_ = time.Second
}
