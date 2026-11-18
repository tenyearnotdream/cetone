package store

import (
	"os"
	"path/filepath"
	"strconv"

	"ncmviva/internal/ncm"
)

// LyricStore caches lyric files so a song heard before shows its words with no
// request. The naming accepts the GTK client's directory too: it wrote
// "<name>-<singer>-<album>.lrc" under ~/.lyrics, and a translation as
// "<song id>.tlrc"; both are read here so a lyric cache from that client is not
// thrown away.
type LyricStore struct {
	dir       string
	legacyDir string
}

// NewLyricStore points at the lyric directory under the paths.
func NewLyricStore(paths Paths) *LyricStore {
	return &LyricStore{dir: filepath.Join(paths.Cache, "lyrics"), legacyDir: LegacyLyricsDir()}
}

// Dir is where new lyric files are written.
func (l *LyricStore) Dir() string { return l.dir }

// Load returns the cached lyric for a song, or ok=false.
func (l *LyricStore) Load(song ncm.Song) (raw, trans string, ok bool) {
	main := filepath.Join(l.dir, strconv.FormatInt(song.ID, 10)+".lrc")
	if data, err := os.ReadFile(main); err == nil && len(data) > 0 {
		transData, _ := os.ReadFile(filepath.Join(l.dir, strconv.FormatInt(song.ID, 10)+".tlrc"))
		return string(data), string(transData), true
	}
	if l.legacyDir == "" {
		return "", "", false
	}
	name := legacyName(song)
	if data, err := os.ReadFile(filepath.Join(l.legacyDir, name+".lrc")); err == nil && len(data) > 0 {
		transData, _ := os.ReadFile(filepath.Join(l.legacyDir, strconv.FormatInt(song.ID, 10)+".tlrc"))
		return string(data), string(transData), true
	}
	return "", "", false
}

// Save writes a lyric and its translation.
func (l *LyricStore) Save(song ncm.Song, raw, trans string) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(l.dir, strconv.FormatInt(song.ID, 10)+".lrc"), []byte(raw), 0o644); err != nil {
		return err
	}
	if trans != "" {
		os.WriteFile(filepath.Join(l.dir, strconv.FormatInt(song.ID, 10)+".tlrc"), []byte(trans), 0o644)
	}
	return nil
}

// legacyName is the GTK client's lyric file name for a song.
func legacyName(song ncm.Song) string {
	// The GTK client replaced "/" in the song name, and used the first artist
	// and the album untouched.
	name := song.Name
	cleaned := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' {
			cleaned = append(cleaned, '／')
			continue
		}
		cleaned = append(cleaned, r)
	}
	artist := ""
	if len(song.Artists) > 0 {
		artist = song.Artists[0].Name
	}
	return string(cleaned) + "-" + artist + "-" + song.Album.Name
}
