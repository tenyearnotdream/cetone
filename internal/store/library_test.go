package store

// The table beside the audio files: what it records, and that it survives being
// written and read back.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cetone/internal/ncm"
)

func newTestLibrary(t *testing.T) (*Library, Paths) {
	t.Helper()
	root := t.TempDir()
	paths := Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")}
	return NewLibrary(paths), paths
}

func TestTheLibraryKeepsAnEntryAcrossRestarts(t *testing.T) {
	library, paths := newTestLibrary(t)
	when := time.Now().Truncate(time.Second)
	if err := library.Put(CacheEntry{
		SongID: 42, Quality: ncm.QualityLossless, File: "music_42_1136020",
		Size: 20147655, SavedAt: when, Best: ncm.QualityLossless,
	}); err != nil {
		t.Fatal(err)
	}

	// A second process.
	again := NewLibrary(paths)
	entry, ok := again.Get(42)
	if !ok {
		t.Fatal("the entry did not survive")
	}
	if entry.Quality != ncm.QualityLossless || entry.Size != 20147655 {
		t.Fatalf("read back %+v", entry)
	}
	if !entry.SavedAt.Equal(when) {
		t.Fatalf("saved at %v, want %v", entry.SavedAt, when)
	}
}

// Knowing that a better tier exists is separate from having it, or the cache
// could never decide to fetch it.
func TestTheLibraryRemembersABetterTierIsWorthFetching(t *testing.T) {
	library, _ := newTestLibrary(t)
	if err := library.Put(CacheEntry{
		SongID: 7, Quality: ncm.QualityStandard, File: "music_7_128000", SavedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := library.WantsBetter(7); ok {
		t.Fatal("nothing is known to be better yet")
	}
	if err := library.NoteBest(7, ncm.QualityHiRes); err != nil {
		t.Fatal(err)
	}
	want, ok := library.WantsBetter(7)
	if !ok || want != ncm.QualityHiRes {
		t.Fatalf("wants %v (found %v), want Hi-Res", want, ok)
	}

	// Once the better file is written, nothing is wanted any more.
	if err := library.Put(CacheEntry{
		SongID: 7, Quality: ncm.QualityHiRes, File: "music_7_1904597", SavedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := library.WantsBetter(7); ok {
		t.Fatal("the better file is on disk but still wanted")
	}
}

// A play at a lower tier must not erase the fact that a better one exists.
func TestWritingALowerTierKeepsTheBestKnown(t *testing.T) {
	library, _ := newTestLibrary(t)
	if err := library.Put(CacheEntry{SongID: 9, Quality: ncm.QualityLossless, Best: ncm.QualityHiRes, SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := library.Put(CacheEntry{SongID: 9, Quality: ncm.QualityStandard, SavedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	entry, ok := library.Get(9)
	if !ok {
		t.Fatal("the entry went away")
	}
	if entry.Best != ncm.QualityHiRes {
		t.Fatalf("best is %v, want Hi-Res kept", entry.Best)
	}
}

// A missing table is an empty one, not an error: a fresh install has none.
func TestAMissingTableLoadsEmpty(t *testing.T) {
	library, _ := newTestLibrary(t)
	if err := library.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := library.Get(1); ok {
		t.Fatal("an empty table answered with an entry")
	}
	if len(library.Entries()) != 0 {
		t.Fatal("an empty table has entries")
	}
}

// A table that cannot be parsed is discarded rather than fatal: the audio files
// are still there, and refusing to start over a broken index is worse.
func TestABrokenTableIsIgnored(t *testing.T) {
	library, paths := newTestLibrary(t)
	if err := os.MkdirAll(paths.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Cache, "library.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := library.Load(); err != nil {
		t.Fatalf("a broken table stopped the load: %v", err)
	}
	if _, ok := library.Get(1); ok {
		t.Fatal("a broken table answered with an entry")
	}
	// And it can still be written.
	if err := library.Put(CacheEntry{SongID: 1, Quality: ncm.QualityStandard, SavedAt: time.Now()}); err != nil {
		t.Fatalf("writing after a broken table: %v", err)
	}
}
