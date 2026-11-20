package store

// Which cached file answers a request. "The best one on disk" is the wrong
// answer: it makes a quality setting look broken and prefers a large file over
// the one that was asked for.

import (
	"os"
	"path/filepath"
	"testing"

	"cetone/internal/ncm"
)

func cacheWith(t *testing.T, ids []ncm.Quality) *Cache {
	t.Helper()
	dir := t.TempDir()
	for _, q := range ids {
		path := filepath.Join(dir, Name(7, q))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Cache{Primary: dir}
}

func TestLookupPrefersTheExactTier(t *testing.T) {
	c := cacheWith(t, []ncm.Quality{ncm.QualityStandard, ncm.QualityLossless, ncm.QualityHiRes})
	_, got, ok := c.Lookup(7, ncm.QualityLossless)
	if !ok || got != ncm.QualityLossless {
		t.Fatalf("lookup for lossless answered %v (found %v)", got, ok)
	}
}

// A Hi-Res file on disk must not answer a request for lossless: that is the case
// that made the setting look ignored.
func TestLookupDoesNotJumpToTheHighestTier(t *testing.T) {
	c := cacheWith(t, []ncm.Quality{ncm.QualityLossless, ncm.QualityHiRes, ncm.QualitySurround})
	_, got, ok := c.Lookup(7, ncm.QualityLossless)
	if !ok || got != ncm.QualityLossless {
		t.Fatalf("lookup for lossless answered %v, want the lossless file", got)
	}
	// Asking for the standard tier finds the nearest above it, not the highest.
	_, got, ok = c.Lookup(7, ncm.QualityStandard)
	if !ok || got != ncm.QualityLossless {
		t.Fatalf("lookup for standard answered %v, want the nearest tier above", got)
	}
}

// With only a lower tier on disk, that file is the answer: it plays, which is
// what the cache is for.
func TestLookupFallsBackToALowerTier(t *testing.T) {
	c := cacheWith(t, []ncm.Quality{ncm.QualityStandard})
	_, got, ok := c.Lookup(7, ncm.QualityHiRes)
	if !ok || got != ncm.QualityStandard {
		t.Fatalf("lookup answered %v, want the standard file", got)
	}
}

// At the same distance the higher file wins: it is already on disk, so choosing
// it costs nothing and sounds better.
func TestLookupPrefersTheHigherOfTwoEquallyClose(t *testing.T) {
	c := cacheWith(t, []ncm.Quality{ncm.QualityHigher, ncm.QualityLossless})
	_, got, ok := c.Lookup(7, ncm.QualityExtreme)
	if !ok || got != ncm.QualityLossless {
		t.Fatalf("lookup answered %v, want the higher of the two", got)
	}
}

// Nothing cached is nothing found.
func TestLookupReportsNothing(t *testing.T) {
	c := cacheWith(t, nil)
	if _, _, ok := c.Lookup(7, ncm.QualityExtreme); ok {
		t.Fatal("an empty cache answered with a file")
	}
}
