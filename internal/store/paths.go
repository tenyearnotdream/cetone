// Package store keeps what the application must remember between runs: the
// settings, the session cookies, the play queue and the audio cache.
//
// Nothing here touches the UI or a network connection; the files are plain
// JSON and the cache is plain files, so a user can read, copy or delete them.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppName is the application's directory and identity name. The GTK client this
// one replaces is read separately, by the functions below, so its cache is
// adopted without being touched.
const AppName = "cetone"

// legacyAppNames are the directory names earlier builds of this program used.
// They are migrated on first run, so a cache filled before the rename still
// plays rather than looking empty.
var legacyAppNames = []string{"ncmviva"}

// Paths are the directories the application writes. They follow the XDG layout
// that the GTK client used, so its cache and cookies can be adopted.
type Paths struct {
	Config string // ~/.config/cetone
	Data   string // ~/.local/share/cetone
	Cache  string // ~/.cache/cetone
}

// DefaultPaths resolves the application's directories.
func DefaultPaths() Paths {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	dataDir := filepath.Join(homeDir(), ".local", "share")
	paths := Paths{
		Config: filepath.Join(configDir, AppName),
		Data:   filepath.Join(dataDir, AppName),
		Cache:  filepath.Join(cacheDir, AppName),
	}
	paths.migrateLegacy(configDir, dataDir, cacheDir)
	return paths
}

// migrateLegacy moves the directories of an earlier build into place, once. It
// runs before anything reads them, so the cache, the session and the settings
// carry over: a renamed program that lost the user's offline library would be
// worse than one that kept its old name.
//
// A directory that already exists is left alone, and the earlier one stays where
// it is rather than being merged into it; a rename that cannot happen is not an
// error the user should see.
func (p Paths) migrateLegacy(configDir, dataDir, cacheDir string) {
	moves := []struct{ from, to string }{
		{filepath.Join(configDir, legacyAppNames[0]), p.Config},
		{filepath.Join(dataDir, legacyAppNames[0]), p.Data},
		{filepath.Join(cacheDir, legacyAppNames[0]), p.Cache},
	}
	for _, move := range moves {
		if _, err := os.Stat(move.from); err != nil {
			continue
		}
		if _, err := os.Stat(move.to); err == nil {
			continue
		}
		// Rename first: it is atomic and keeps the mtimes. Across file systems it
		// fails, and a copy is the fallback.
		if err := os.Rename(move.from, move.to); err == nil {
			continue
		}
		copyTree(move.from, move.to)
	}
}

// copyTree copies a directory tree, for the migration that cannot rename.
func copyTree(from, to string) {
	walkErr := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relative, relErr := filepath.Rel(from, path)
		if relErr != nil {
			return nil
		}
		target := filepath.Join(to, relative)
		if info.IsDir() {
			os.MkdirAll(target, 0o755)
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil
		}
		os.WriteFile(target, data, 0o644)
		return nil
	})
	if walkErr != nil {
		return
	}
	os.RemoveAll(from)
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

// LegacyGTKCacheDir is where the GTK client has been writing its audio cache
// since 2022. It is read as a fallback so a VIP song cached before the
// subscription lapsed still plays here.
func LegacyGTKCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cacheDir, "netease-cloud-music-gtk4")
}

// LegacyGTKDataDir holds the GTK client's cookies.
func LegacyGTKDataDir() string {
	return filepath.Join(homeDir(), ".local", "share", "netease-cloud-music-gtk4")
}

// LegacyLyricsDir is the GTK client's lyric directory (~/.lyrics).
func LegacyLyricsDir() string { return filepath.Join(homeDir(), ".lyrics") }

// Ensure creates every directory the application writes to: the configuration
// and session files, the audio cache, and the lyric and cover caches. A
// directory missing here is a first play that cannot write its file.
func (p Paths) Ensure() error {
	directories := []string{
		p.Config,
		p.Data,
		p.Cache,
		filepath.Join(p.Cache, "music"),
		filepath.Join(p.Cache, "lyrics"),
		filepath.Join(p.Cache, "covers"),
	}
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("store: create %s: %w", dir, err)
		}
	}
	return nil
}

// writeJSON writes a file atomically: a crash mid-write leaves the previous
// contents rather than a truncated file.
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readJSON reads a file into value, reporting whether it existed. A file that
// does not parse is reported as an error with the parse failure, so the caller
// can start from defaults and say so.
func readJSON(path string, value any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return true, fmt.Errorf("store: %s: %w", path, err)
	}
	return true, nil
}
