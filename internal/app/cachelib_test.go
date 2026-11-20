package app

// What the cache keeps. Only the best tier is worth storing: it is the file that
// plays after the subscription ends, and a lower one would be a worse answer to
// the same question.

import (
	"testing"
	"time"

	"cetone/internal/ncm"
	"cetone/internal/store"
)

func storeEntry(id int64, quality, best ncm.Quality) store.CacheEntry {
	return store.CacheEntry{SongID: id, Quality: quality, Best: best, SavedAt: time.Now()}
}

func TestOnlyTheBestTierIsCached(t *testing.T) {
	a := testApp(t)
	// Nothing chosen: the request was for the highest tier, so whatever the server
	// answered is the best it will give and is worth keeping.
	song := ncm.Song{ID: 1, Name: "s"}
	if !a.isBestTier(song, ncm.QualitySurround) {
		t.Fatal("the answer to an unqualified request is not considered the best")
	}
	// A tier the user picked for this play: kept only if it is what the server
	// actually served, which is the case that matters when the pick was the top.
	song.Selected, song.SelectedSet = ncm.QualityHiRes, true
	if a.isBestTier(song, ncm.QualityStandard) {
		t.Fatal("a lower tier than the one chosen would be cached")
	}
	if !a.isBestTier(song, ncm.QualityHiRes) {
		t.Fatal("the chosen tier is not cached")
	}
	// A pick the server could not honour is not the best file either.
	if a.isBestTier(song, ncm.QualityLossless) {
		t.Fatal("a tier below the choice counts as the best")
	}
}

// The library learns the ceiling the server reported, which is what later says a
// better file is worth fetching.
func TestPlayingRecordsTheCeilingItLearned(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 2, Name: "s"}
	a.noteBestTier(song, ncm.QualityHiRes)
	best, ok := a.library.Get(song.ID)
	if !ok {
		t.Fatal("nothing was recorded")
	}
	if best.Best != ncm.QualityHiRes {
		t.Fatalf("recorded best %v", best.Best)
	}
	// A lower ceiling reported later does not lower it.
	a.noteBestTier(song, ncm.QualityStandard)
	if entry, _ := a.library.Get(song.ID); entry.Best != ncm.QualityHiRes {
		t.Fatalf("the ceiling went down to %v", entry.Best)
	}
}

// A file at a lower tier than a known-better one is wanted better: the case the
// background fetch exists for.
func TestALowerFileWantsTheBetterOne(t *testing.T) {
	a := testApp(t)
	song := ncm.Song{ID: 3, Name: "s"}
	a.library.Put(storeEntry(song.ID, ncm.QualityStandard, ncm.QualityStandard))
	if _, ok := a.library.WantsBetter(song.ID); ok {
		t.Fatal("nothing better is known yet")
	}
	a.library.NoteBest(song.ID, ncm.QualityLossless)
	if best, ok := a.library.WantsBetter(song.ID); !ok || best != ncm.QualityLossless {
		t.Fatalf("wants %v (found %v)", best, ok)
	}
}
