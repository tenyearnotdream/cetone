//go:build live

package ncm

import (
	"testing"
)

// TestLivePlaylistDetail exercises the eapi playlist endpoint, which is the
// call the whole application hangs off.
func TestLivePlaylistDetail(t *testing.T) {
	c, ctx := liveClient(t)
	list, err := c.ToplistIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := c.PlaylistDetail(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tracks) == 0 {
		t.Fatalf("playlist %q has no tracks", detail.Name)
	}
	s := detail.Tracks[0]
	t.Logf("%q: %d tracks; first %q - %s (%v) %v", detail.Name, len(detail.Tracks), s.Name, s.Singer(), s.Duration, s.Copyright())
}
