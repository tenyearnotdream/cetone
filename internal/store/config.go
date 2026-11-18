package store

import (
	"path/filepath"

	"ncmviva/internal/ncm"
)

// Config is every preference the window edits. It is one flat struct so the
// settings page can bind fields directly and the file stays readable.
type Config struct {
	// Quality is the tier to ask for, as ncm.Quality's index.
	Quality int `json:"quality"`
	// Volume is 0..1 and persists between runs.
	Volume float64 `json:"volume"`
	// Muted remembers the mute state separately from the volume.
	Muted bool `json:"muted"`
	// Repeat is "list", "one" or "off".
	Repeat string `json:"repeat"`
	// Theme is "system", "light" or "dark".
	Theme string `json:"theme"`
	// IgnoreUnplayable greys out (and skips) songs whose rights no longer
	// allow streaming. It is off by default: a cached VIP song is playable even
	// when the account can no longer stream it.
	IgnoreUnplayable bool `json:"ignoreUnplayable"`
	// Proxy is an http/https/socks5 URL, empty for the environment's.
	Proxy string `json:"proxy"`
	// CacheDirs are extra directories to search for cached audio, before the
	// application's own. The GTK client's cache is added automatically when it
	// exists.
	CacheDirs []string `json:"cacheDirs"`
	// KeepAwake asks the desktop to stay awake while music plays.
	KeepAwake bool `json:"keepAwake"`
	// SearchHistory is the last handful of searches, newest first.
	SearchHistory []string `json:"searchHistory"`
}

const (
	RepeatOff  = "off"
	RepeatList = "list"
	RepeatOne  = "one"
)

// DefaultConfig is the state before the user changes anything: extreme
// quality, list repeat, follow the desktop's appearance, and skip songs the
// account cannot stream.
func DefaultConfig() Config {
	return Config{
		Quality:          int(ncm.QualityExtreme),
		Volume:           0.7,
		Repeat:           RepeatList,
		Theme:            "system",
		IgnoreUnplayable: true,
		KeepAwake:        true,
	}
}

// RepeatMode maps the stored string to a mode, defaulting to list repeat.
func (c Config) RepeatMode() string {
	switch c.Repeat {
	case RepeatOff, RepeatOne, RepeatList:
		return c.Repeat
	}
	return RepeatList
}

// PreferredQuality is the stored tier.
func (c Config) PreferredQuality() ncm.Quality {
	if c.Quality < 0 || c.Quality >= len(ncm.Qualities()) {
		return ncm.QualityExtreme
	}
	return ncm.Quality(c.Quality)
}

// Settings reads and writes the config file.
type Settings struct {
	path string
}

// NewSettings points at the config file under the paths.
func NewSettings(paths Paths) *Settings {
	return &Settings{path: filepath.Join(paths.Config, "config.json")}
}

// Load reads the config, falling back to the defaults for a missing or broken
// file; the second result reports that a broken file was replaced.
func (s *Settings) Load() (Config, error) {
	config := DefaultConfig()
	existed, err := readJSON(s.path, &config)
	if err != nil {
		return DefaultConfig(), err
	}
	if !existed {
		return config, nil
	}
	if config.Quality < 0 || config.Quality > int(ncm.QualityMaster) {
		config.Quality = int(ncm.QualityExtreme)
	}
	if config.Volume < 0 || config.Volume > 1 {
		config.Volume = 0.7
	}
	if config.Theme != "light" && config.Theme != "dark" {
		config.Theme = "system"
	}
	return config, nil
}

// Save writes the config.
func (s *Settings) Save(config Config) error { return writeJSON(s.path, config) }
