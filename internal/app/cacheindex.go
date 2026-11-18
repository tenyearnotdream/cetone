package app

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"cetone/internal/ncm"
)

// cacheIndex remembers which songs have a cached file, so a list of a thousand
// rows costs one directory scan instead of a thousand stat calls per paint. It
// is rebuilt when a download finishes, when the user changes the cache
// directories, and when it is older than a minute, which is soon enough for a
// file that appeared behind the application's back.
type cacheIndex struct {
	mu     sync.Mutex
	cache  cacheSource
	hits   map[int64]ncm.Quality
	dirty  bool
	loaded bool
}

// cacheSource is the part of the cache the index reads: its directories and
// the name format it lists.
type cacheSource interface {
	Dir() string
}

// directCache is a cache that also knows its read-only directories.
type directCache interface {
	Directories() []string
}

func newCacheIndex(cache cacheSource) *cacheIndex {
	return &cacheIndex{cache: cache, dirty: true}
}

// setDirty marks the index stale: the next read rescans.
func (c *cacheIndex) setDirty() {
	c.mu.Lock()
	c.dirty = true
	c.mu.Unlock()
}

// has reports whether a song has any cached file.
func (c *cacheIndex) has(songID int64) bool {
	_, ok := c.quality(songID)
	return ok
}

// quality is the best cached tier of a song.
func (c *cacheIndex) quality(songID int64) (ncm.Quality, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirty || !c.loaded {
		c.rescanLocked()
	}
	quality, ok := c.hits[songID]
	return quality, ok
}

// rescanLocked reads the cache directories. It runs on whichever goroutine asks
// first, which is the window thread; a scan of a few thousand names is a
// millisecond, and it happens once per change rather than once per row.
func (c *cacheIndex) rescanLocked() {
	hits := make(map[int64]ncm.Quality, len(c.hits))
	directories := cacheDirectories(c.cache)
	for _, dir := range directories {
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.IsDir() {
				continue
			}
			name := item.Name()
			if !strings.HasPrefix(name, "music_") || strings.HasSuffix(name, ".part") {
				continue
			}
			songID, rate, ok := parseCacheFile(name)
			if !ok {
				continue
			}
			quality := qualityFromBitrate(rate)
			if existing, seen := hits[songID]; !seen || quality > existing {
				hits[songID] = quality
			}
		}
	}
	c.hits = hits
	c.loaded = true
	c.dirty = false
}

// cacheDirectories is every directory a cache may hold: the interface gives the
// primary one, and the concrete cache adds the extras.
func cacheDirectories(cache cacheSource) []string {
	if real, ok := cache.(directCache); ok {
		return real.Directories()
	}
	if dir := cache.Dir(); dir != "" {
		return []string{dir}
	}
	return nil
}

// parseCacheFile splits music_<id>_<rate>.
func parseCacheFile(name string) (songID int64, rate int, ok bool) {
	rest := strings.TrimPrefix(name, "music_")
	underscore := strings.LastIndexByte(rest, '_')
	if underscore <= 0 {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(rest[:underscore], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	bitrate, err := strconv.Atoi(rest[underscore+1:])
	if err != nil {
		return 0, 0, false
	}
	return id, bitrate, true
}

// qualityFromBitrate maps a cache file's rate to the tier it was saved at.
func qualityFromBitrate(rate int) ncm.Quality {
	switch rate {
	case 128000:
		return ncm.QualityStandard
	case 192000:
		return ncm.QualityHigher
	case 320000:
		return ncm.QualityExtreme
	case 999000:
		return ncm.QualityLossless
	case 1900000:
		return ncm.QualityHiRes
	case 804505:
		return ncm.QualitySurround
	case 2695684:
		return ncm.QualityAudioVivid
	}
	return ncm.QualityMaster
}
