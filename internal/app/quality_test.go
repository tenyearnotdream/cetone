package app

// The quality setting is the user's decision about bandwidth: 320 kb/s is 5.7 MB
// for a three-minute song and Hi-Res is 34 MB. It must not be raised on their
// behalf, and the transport must say what is actually playing.

import (
	"testing"

	"cetone/internal/ncm"
)

func TestTheBestTierIsAskedForByDefault(t *testing.T) {
	a := testApp(t)
	// No per-song choice: the client asks for the highest tier it can name and
	// lets the server answer with what the account holds. There is no account-wide
	// setting, because the best tier depends on the song.
	song := ncm.Song{ID: 6001, Name: "song"}
	if got := a.songQuality(song); got != ncm.QualityMaster {
		t.Fatalf("with no choice the song asks for %v", got)
	}

	// An explicit choice for this play wins over that.
	song.Selected, song.SelectedSet = ncm.QualityHigher, true
	if got := a.songQuality(song); got != ncm.QualityHigher {
		t.Fatalf("a per-song choice was ignored: %v", got)
	}
	// Including lower than the default, which is the point of offering it.
	song.Selected = ncm.QualityStandard
	if got := a.songQuality(song); got != ncm.QualityStandard {
		t.Fatalf("a lower per-song choice was ignored: %v", got)
	}
}

func TestTheTransportNamesTheTierThatIsPlaying(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 6002, Name: "song"}
	a.queue = []ncm.Song{song}
	a.queueAt = 0
	// Before anything played, the transport claims no tier.
	if got := a.playingQuality(song); got != "" {
		t.Fatalf("the transport named %q before a song played", got)
	}
	// The tier the server answered with is what is shown, not the one asked for.
	a.noteCachedQuality(song.ID, ncm.QualityLossless)
	if got := a.playingQuality(song); got != ncm.QualityLossless.String() {
		t.Fatalf("the transport names %q, want %q", got, ncm.QualityLossless.String())
	}
	// A song that is not the current one says nothing.
	if got := a.playingQuality(ncm.Song{ID: 9999}); got != "" {
		t.Fatalf("the transport named %q for a song that is not playing", got)
	}
}
