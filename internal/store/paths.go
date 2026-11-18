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

// Paths are the directories the application writes. They follow the XDG layout
// that the GTK client used, so its cache and cookies can be adopted.
type Paths struct {
	Config string // ~/.config/ncmviva
	Data   string // ~/.local/share/ncmviva
	Cache  string // ~/.cache/ncmviva
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
	return Paths{
		Config: filepath.Join(configDir, "ncmviva"),
		Data:   filepath.Join(dataDir, "ncmviva"),
		Cache:  filepath.Join(cacheDir, "ncmviva"),
	}
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

// Ensure creates the directories the application writes to.
func (p Paths) Ensure() error {
	for _, dir := range []string{p.Config, p.Data, p.Cache, filepath.Join(p.Cache, "lyrics"), filepath.Join(p.Cache, "covers")} {
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
