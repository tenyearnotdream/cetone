package store

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cetone/internal/ncm"
)

// Cache is the audio cache. Its file names are the ones the GTK client uses
// (music_<song id>_<bitrate>), so a directory written by that client plays
// here as it is, including the VIP songs it stored before the subscription
// lapsed. That naming is the compatibility contract this type exists for.
type Cache struct {
	// Primary is where new downloads land.
	Primary string
	// Extra are searched after Primary, in order.
	Extra []string
}

// NewCache builds a cache rooted at the application's cache directory, plus
// the directories the user added and the GTK client's own when it exists.
func NewCache(paths Paths, extra []string) *Cache {
	c := &Cache{Primary: filepath.Join(paths.Cache, "music")}
	seen := map[string]bool{c.Primary: true}
	for _, dir := range extra {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		c.Extra = append(c.Extra, dir)
	}
	if legacy := LegacyGTKCacheDir(); legacy != "" && !seen[legacy] {
		if info, err := os.Stat(legacy); err == nil && info.IsDir() {
			c.Extra = append(c.Extra, legacy)
		}
	}
	return c
}

// Name is the cache file name of a song at a quality.
func Name(songID int64, quality ncm.Quality) string {
	return "music_" + strconv.FormatInt(songID, 10) + "_" + strconv.Itoa(quality.Bitrate())
}

// Path is where a new download of the song is written.
func (c *Cache) Path(songID int64, quality ncm.Quality) string {
	return filepath.Join(c.Primary, Name(songID, quality))
}

// PartPath is the in-progress name beside the final one, so an interrupted
// download never looks like a playable cache entry.
func (c *Cache) PartPath(songID int64, quality ncm.Quality) string {
	return c.Path(songID, quality) + ".part"
}

// LookupExact finds a cached file for the song at exactly the requested quality.
func (c *Cache) LookupExact(songID int64, quality ncm.Quality) (string, bool) {
	return c.find(Name(songID, quality))
}

// LookupBest finds the highest-quality cached file for the song across all
// cache directories.
func (c *Cache) LookupBest(songID int64) (string, ncm.Quality, bool) {
	all := ncm.Qualities()
	for i := len(all) - 1; i >= 0; i-- {
		q := all[i]
		if path, ok := c.find(Name(songID, q)); ok {
			return path, q, true
		}
	}
	return "", 0, false
}

// CachedQualities returns every quality cached on disk for the song, ordered
// from lowest to highest.
func (c *Cache) CachedQualities(songID int64) []ncm.Quality {
	all := ncm.Qualities()
	var found []ncm.Quality
	for _, q := range all {
		if _, ok := c.find(Name(songID, q)); ok {
			found = append(found, q)
		}
	}
	return found
}

// Lookup finds a cached file for the song at exactly the quality, then at the
// closest other quality. The second result is the quality that was found.
//
// "Closest" rather than "best" is what keeps the answer predictable: a directory
// holding a Hi-Res file must not answer a request for lossless with it, or the
// setting would appear not to work and a 34 MB file would be preferred over the
// 20 MB one that was asked for. The walk is the tiers at or above the request
// from the nearest, then the tiers below it from the nearest, so the choice is
// the least surprising file that plays.
func (c *Cache) Lookup(songID int64, quality ncm.Quality) (string, ncm.Quality, bool) {
	if path, ok := c.find(Name(songID, quality)); ok {
		return path, quality, true
	}
	for _, q := range byCloseness(quality) {
		if path, ok := c.find(Name(songID, q)); ok {
			return path, q, true
		}
	}
	return "", 0, false
}

// byCloseness lists every other tier in the order Lookup tries them: nearer
// before farther, and at equal distance the higher one, since a better file that
// is already on disk costs nothing to play.
func byCloseness(quality ncm.Quality) []ncm.Quality {
	all := ncm.Qualities()
	order := make([]ncm.Quality, 0, len(all)-1)
	for distance := 1; distance < len(all); distance++ {
		higher := int(quality) + distance
		if higher < len(all) {
			order = append(order, ncm.Quality(higher))
		}
		lower := int(quality) - distance
		if lower >= 0 {
			order = append(order, ncm.Quality(lower))
		}
	}
	return order
}

// find looks for one exact file name, in the primary directory first.
func (c *Cache) find(name string) (string, bool) {
	if c.Primary != "" {
		if path := filepath.Join(c.Primary, name); fileExists(path) {
			return path, true
		}
	}
	for _, dir := range c.Extra {
		if path := filepath.Join(dir, name); fileExists(path) {
			return path, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// Export copies a cached song out to a path the user chose.
func (c *Cache) Export(songID int64, quality ncm.Quality, destination string) error {
	source, _, ok := c.Lookup(songID, quality)
	if !ok {
		return os.ErrNotExist
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o644)
}

// Dir is the directory to offer in a file manager or a settings page.
func (c *Cache) Dir() string { return c.Primary }

// Directories is every directory the cache reads, primary first.
func (c *Cache) Directories() []string {
	return append([]string{c.Primary}, c.Extra...)
}

// Entries lists what the cache holds, newest first.
type Entry struct {
	Path  string
	Song  int64
	Rate  int
	Size  int64
	MTime int64
}

// List reads the cache directories.
func (c *Cache) List() ([]Entry, error) {
	var entries []Entry
	for _, dir := range append([]string{c.Primary}, c.Extra...) {
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasPrefix(item.Name(), "music_") || strings.HasSuffix(item.Name(), ".part") {
				continue
			}
			songID, rate, ok := parseCacheName(item.Name())
			if !ok {
				continue
			}
			info, err := item.Info()
			if err != nil {
				continue
			}
			entries = append(entries, Entry{
				Path:  filepath.Join(dir, item.Name()),
				Song:  songID,
				Rate:  rate,
				Size:  info.Size(),
				MTime: info.ModTime().Unix(),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].MTime > entries[j].MTime })
	return entries, nil
}

// parseCacheName splits music_<id>_<rate>.
func parseCacheName(name string) (songID int64, rate int, ok bool) {
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

// Total reports the bytes and the number of cached songs.
func (c *Cache) Total() (bytes int64, count int) {
	entries, _ := c.List()
	for _, entry := range entries {
		bytes += entry.Size
	}
	return bytes, len(entries)
}

// Remove deletes one cached file.
func (c *Cache) Remove(path string) error {
	// Only a file that belongs to one of this cache's directories is removed:
	// the path can come from the UI, and the cache is not a delete-anything
	// API.
	if !c.Owns(path) {
		return os.ErrPermission
	}
	return os.Remove(path)
}

// Owns reports whether a path is inside one of the cache directories.
func (c *Cache) Owns(path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, dir := range append([]string{c.Primary}, c.Extra...) {
		if dir == "" {
			continue
		}
		base, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(base, absolute)
		if err != nil {
			continue
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ClearPartFiles removes the leftovers of interrupted downloads.
func (c *Cache) ClearPartFiles() {
	for _, dir := range append([]string{c.Primary}, c.Extra...) {
		items, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range items {
			if strings.HasSuffix(item.Name(), ".part") {
				os.Remove(filepath.Join(dir, item.Name()))
			}
		}
	}
}

// ParseName reads a cache file name back into the song and the tier it holds.
//
// The name is the contract with the GTK client, so reading it is how a file that
// appeared on disk becomes an entry in the library: a directory of files from
// another client is adopted rather than ignored.
func ParseName(name string) (songID int64, quality ncm.Quality, ok bool) {
	rest, found := strings.CutPrefix(name, "music_")
	if !found {
		return 0, 0, false
	}
	idPart, bitratePart, found := strings.Cut(rest, "_")
	if !found {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id == 0 {
		return 0, 0, false
	}
	bitrate, err := strconv.Atoi(bitratePart)
	if err != nil || bitrate <= 0 {
		return 0, 0, false
	}
	// The bit rate in the name is the one Quality.Bitrate writes, so the match is
	// exact; a name from a client that used another number is not decodable and is
	// left alone rather than guessed at.
	for q := ncm.QualityStandard; q < ncm.Quality(len(ncm.Qualities())); q++ {
		if q.Bitrate() == bitrate {
			return id, q, true
		}
	}
	return 0, 0, false
}
