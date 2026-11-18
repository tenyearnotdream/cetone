package store

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
)

// CoverStore keeps album art on disk, so the discover page and the player bar
// draw from the cache instead of the network on every run.
type CoverStore struct {
	dir string
}

// NewCoverStore points at the cover directory under the paths.
func NewCoverStore(paths Paths) *CoverStore {
	return &CoverStore{dir: filepath.Join(paths.Cache, "covers")}
}

// Path is the file for one URL at one pixel size. The name is the URL's hash so
// two albums can never collide, and the size is in the name so a larger cover
// does not overwrite a small one.
func (c *CoverStore) Path(rawURL string, size int) string {
	sum := sha1.Sum([]byte(rawURL))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:8])+"_"+strconv.Itoa(size)+".img")
}

// Load reads a cached cover, or reports ok=false.
func (c *CoverStore) Load(rawURL string, size int) ([]byte, bool) {
	data, err := os.ReadFile(c.Path(rawURL, size))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// Save writes a cover.
func (c *CoverStore) Save(rawURL string, size int, data []byte) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.Path(rawURL, size), data, 0o644)
}

// Dir is the cover directory.
func (c *CoverStore) Dir() string { return c.dir }
