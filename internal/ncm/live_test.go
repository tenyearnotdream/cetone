//go:build live

package ncm

// A live smoke test of the protocol layer against the real API. It is behind
// the "live" build tag because it needs the network and the API's answers
// change daily:
//
//	go test ./internal/ncm -tags live -run Live -v

import (
	"context"
	"testing"
	"time"
)

func liveClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return NewClient(), ctx
}

func TestLiveToplist(t *testing.T) {
	c, ctx := liveClient(t)
	list, err := c.ToplistIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("the toplist index is empty")
	}
	t.Logf("%d charts, first is %q (id %d)", len(list), list[0].Name, list[0].ID)
}

func TestLiveSearchAndURLs(t *testing.T) {
	c, ctx := liveClient(t)
	result, err := c.Search(ctx, "海阔天空", SearchSongs, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Songs) == 0 {
		t.Fatal("search returned no songs")
	}
	song := result.Songs[0]
	t.Logf("first hit: %d %q - %s, %v, fee %d, %v", song.ID, song.Name, song.Singer(), song.Duration, song.Fee, song.Copyright())

	for _, q := range []Quality{QualityStandard, QualityExtreme, QualityLossless} {
		urls, err := c.SongURLs(ctx, []int64{song.ID}, q)
		if err != nil {
			t.Fatalf("%v: %v", q, err)
		}
		for _, u := range urls {
			t.Logf("%v: br=%d type=%s level=%s size=%d url=%.70s", q, u.Bitrate, u.Type, u.Level, u.Size, u.URL)
		}
	}
}

func TestLiveLyric(t *testing.T) {
	c, ctx := liveClient(t)
	lyric, err := c.Lyric(ctx, 347230)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d lines; first: %v %q / %q", len(lyric.Lines), lyric.Lines[0].At, lyric.Lines[0].Text, lyric.Lines[0].Translation)
}

// TestLiveFreeSongQualities checks what container the API answers with at each
// tier for a song an anonymous account may stream; the decoder is chosen from
// this, so it is worth seeing.
func TestLiveFreeSongQualities(t *testing.T) {
	c, ctx := liveClient(t)
	result, err := c.Search(ctx, "Yesterday", SearchSongs, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, song := range result.Songs {
		if song.Fee != 0 || song.Privilege.Pl > 0 && song.Privilege.Pl < 100000 {
			continue
		}
		for _, q := range []Quality{QualityStandard, QualityExtreme, QualityLossless, QualityHiRes} {
			urls, err := c.SongURLs(ctx, []int64{song.ID}, q)
			if err != nil {
				t.Fatalf("%v: %v", q, err)
			}
			for _, u := range urls {
				t.Logf("%q fee=%d: %v -> type=%s level=%s br=%d size=%d trial=%v", song.Name, song.Fee, q, u.Type, u.Level, u.Bitrate, u.Size, u.FreeTrial)
			}
		}
		return
	}
	t.Skip("no free song in the page")
}
