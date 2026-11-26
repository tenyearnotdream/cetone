package store

import (
	"os"
	"strings"
	"sync"

	"cetone/internal/ncm"
)

// CacheIndex caches a directory scan of all audio cache folders in memory so
// the UI can show cached badges, available qualities and total cache size on
// every paint without touching the filesystem.
type CacheIndex struct {
	mu         sync.Mutex
	cache      *Cache
	best       map[int64]ncm.Quality
	all        map[int64][]ncm.Quality
	totalBytes int64
	totalCount int
	dirty      bool
	loaded     bool
}

// NewCacheIndex builds an index over cache.
func NewCacheIndex(cache *Cache) *CacheIndex {
	return &CacheIndex{cache: cache, dirty: true}
}

// SetCache swaps the underlying cache (for example after the user edits extra
// cache directories in settings) and marks the index dirty.
func (idx *CacheIndex) SetCache(cache *Cache) {
	idx.mu.Lock()
	idx.cache = cache
	idx.dirty = true
	idx.mu.Unlock()
}

// SetDirty marks the index stale so the next read rescans disk once.
func (idx *CacheIndex) SetDirty() {
	idx.mu.Lock()
	idx.dirty = true
	idx.mu.Unlock()
}

// Has reports whether songID has any cached audio file on disk.
func (idx *CacheIndex) Has(songID int64) bool {
	_, ok := idx.BestQuality(songID)
	return ok
}

// BestQuality returns the highest quality cached on disk for songID.
func (idx *CacheIndex) BestQuality(songID int64) (ncm.Quality, bool) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.ensureLocked()
	q, ok := idx.best[songID]
	return q, ok
}

// Qualities returns every cached quality on disk for songID, sorted low to high.
func (idx *CacheIndex) Qualities(songID int64) []ncm.Quality {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.ensureLocked()
	src := idx.all[songID]
	if len(src) == 0 {
		return nil
	}
	out := make([]ncm.Quality, len(src))
	copy(out, src)
	return out
}

// Stats returns the cached total size in bytes and file count.
func (idx *CacheIndex) Stats() (bytes int64, count int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.ensureLocked()
	return idx.totalBytes, idx.totalCount
}

func (idx *CacheIndex) ensureLocked() {
	if !idx.dirty && idx.loaded {
		return
	}
	best := make(map[int64]ncm.Quality, len(idx.best))
	allSet := make(map[int64]map[ncm.Quality]bool, len(idx.best))
	var totalBytes int64
	totalCount := 0

	if idx.cache != nil {
		for _, dir := range idx.cache.Directories() {
			if dir == "" {
				continue
			}
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
				songID, q, ok := ParseName(name)
				if !ok {
					// Fallback for non-standard legacy bitrates written by other tools.
					var rate int
					songID, rate, ok = parseCacheName(name)
					if !ok {
						continue
					}
					q = qualityFromLegacyBitrate(rate)
				}
				info, err := item.Info()
				if err != nil || info.Size() <= 0 {
					continue
				}
				totalBytes += info.Size()
				totalCount++
				if existing, seen := best[songID]; !seen || q > existing {
					best[songID] = q
				}
				if allSet[songID] == nil {
					allSet[songID] = make(map[ncm.Quality]bool, 2)
				}
				allSet[songID][q] = true
			}
		}
	}

	all := make(map[int64][]ncm.Quality, len(allSet))
	ordered := ncm.Qualities()
	for songID, set := range allSet {
		list := make([]ncm.Quality, 0, len(set))
		for _, q := range ordered {
			if set[q] {
				list = append(list, q)
			}
		}
		all[songID] = list
	}

	idx.best = best
	idx.all = all
	idx.totalBytes = totalBytes
	idx.totalCount = totalCount
	idx.loaded = true
	idx.dirty = false
}

// qualityFromLegacyBitrate maps a non-standard bitrate suffix to the closest tier.
func qualityFromLegacyBitrate(rate int) ncm.Quality {
	switch {
	case rate == ncm.QualitySurround.Bitrate():
		return ncm.QualitySurround
	case rate <= 128000:
		return ncm.QualityStandard
	case rate <= 192000:
		return ncm.QualityHigher
	case rate <= 320000:
		return ncm.QualityExtreme
	case rate <= 999000:
		return ncm.QualityLossless
	case rate <= 1900000:
		return ncm.QualityHiRes
	case rate <= 2695684:
		return ncm.QualityAudioVivid
	default:
		return ncm.QualityMaster
	}
}
