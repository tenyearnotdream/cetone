package app

// The lyric page is a view of what is playing. Switching songs while it is open
// has to switch the words, or it shows the lyrics of a song that has ended.

import (
	"testing"

	"cetone/internal/ncm"
)

func TestTheLyricPageFollowsTheSong(t *testing.T) {
	a := testApp(t)
	first := ncm.Song{ID: 501, Name: "first"}
	second := ncm.Song{ID: 502, Name: "second"}
	a.queue = []ncm.Song{first, second}
	a.nav = []route{{kind: routeLyrics, id: first.ID, title: first.Name}}

	a.followPlayingSong(second)
	top := a.top()
	if top.kind != routeLyrics {
		t.Fatalf("the page changed to %v", top.kind)
	}
	if top.id != second.ID {
		t.Fatalf("the lyric page still shows song %d, want %d", top.id, second.ID)
	}
	if top.title != second.Name {
		t.Fatalf("the title is %q, want %q", top.title, second.Name)
	}

	// Playing the song the page already shows changes nothing, and no other page
	// is touched by a song change.
	a.nav = []route{
		{kind: routeDiscover, title: "发现"},
		{kind: routePlaylist, id: 7, title: "list"},
	}
	a.followPlayingSong(first)
	if top := a.top(); top.kind != routePlaylist || top.id != 7 {
		t.Fatalf("a song change altered another page: %+v", top)
	}
}
