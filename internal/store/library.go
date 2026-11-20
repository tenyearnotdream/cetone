package store

// The cache's own record of what it holds.
//
// The files alone are not enough: a name says which song and which tier, but not
// when it was written, whether a better tier exists for that song, or whether the
// song was ever heard at its best. Those are questions about the library, and the
// answers have to survive a restart, so they live in a table beside the audio.
//
// The table is a JSON file: small, human-readable, and replaced atomically so a
// machine that loses power mid-write keeps the previous one rather than half of
// both.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"cetone/internal/ncm"
)

// CacheEntry is one song's cached file.
type CacheEntry struct {
	SongID  int64       `json:"songId"`
	Quality ncm.Quality `json:"quality"`
	// File is the name inside the music directory, not an absolute path: the cache
	// can be moved, and a table of absolute paths would break with it.
	File string `json:"file"`
	Size int64  `json:"size"`
	// SavedAt is when the file was last written.
	SavedAt time.Time `json:"savedAt"`
	// Best is the highest tier the server offered for this song when it was last
	// played. It is what says whether a better file could still be fetched.
	Best ncm.Quality `json:"best"`
}

// Library is the table, kept in memory and written through to disk.
type Library struct {
	mu      sync.Mutex
	path    string
	entries map[int64]CacheEntry
	loaded  bool
}

// NewLibrary points at the table under the paths.
func NewLibrary(paths Paths) *Library {
	return &Library{path: filepath.Join(paths.Cache, "library.json")}
}

// Load reads the table, or starts an empty one when there is none.
func (l *Library) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadLocked()
}

func (l *Library) loadLocked() error {
	if l.loaded {
		return nil
	}
	l.entries = make(map[int64]CacheEntry)
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.loaded = true
			return nil
		}
		return fmt.Errorf("store: library: %w", err)
	}
	var file struct {
		Entries []CacheEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		// A table that cannot be read is not fatal: the files are still there, and
		// the index is rebuilt from them. Losing the record is better than refusing
		// to start.
		l.loaded = true
		return nil
	}
	for _, entry := range file.Entries {
		if entry.SongID != 0 {
			l.entries[entry.SongID] = entry
		}
	}
	l.loaded = true
	return nil
}

// Get is one song's entry.
func (l *Library) Get(songID int64) (CacheEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return CacheEntry{}, false
	}
	entry, ok := l.entries[songID]
	return entry, ok
}

// Put records a file, and writes the table.
func (l *Library) Put(entry CacheEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	// Keep the best tier already known: a play at a lower tier must not erase the
	// knowledge that a better one exists.
	if previous, ok := l.entries[entry.SongID]; ok && entry.Best < previous.Best {
		entry.Best = previous.Best
	}
	l.entries[entry.SongID] = entry
	return l.saveLocked()
}

// NoteBest records the best tier the server offered for a song, without claiming
// it is on disk. It is how the cache learns that a better file is worth fetching.
func (l *Library) NoteBest(songID int64, best ncm.Quality) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	entry := l.entries[songID]
	if entry.SongID == 0 {
		entry.SongID = songID
	}
	if best <= entry.Best {
		return nil
	}
	entry.Best = best
	l.entries[songID] = entry
	return l.saveLocked()
}

// Remove forgets a song, for a file that went away.
func (l *Library) Remove(songID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return err
	}
	if _, ok := l.entries[songID]; !ok {
		return nil
	}
	delete(l.entries, songID)
	return l.saveLocked()
}

// Entries is the whole table, newest first.
func (l *Library) Entries() []CacheEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil
	}
	out := make([]CacheEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt.After(out[j].SavedAt) })
	return out
}

// WantsBetter reports whether a song is cached at less than the best tier known,
// which is the case the cache upgrades in the background.
func (l *Library) WantsBetter(songID int64) (ncm.Quality, bool) {
	entry, ok := l.Get(songID)
	if !ok || entry.Best <= entry.Quality {
		return 0, false
	}
	return entry.Best, true
}

// saveLocked writes the table. The file is written beside the real one and
// renamed, so a crash leaves either the old table or the new one.
func (l *Library) saveLocked() error {
	entries := make([]CacheEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].SongID < entries[j].SongID })
	data, err := json.MarshalIndent(struct {
		Version int          `json:"version"`
		Entries []CacheEntry `json:"entries"`
	}{Version: 1, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	temporary := l.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, l.path)
}
