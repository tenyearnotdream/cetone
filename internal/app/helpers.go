package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vivaui/viva/app"

	"ncmviva/internal/ncm"
	"ncmviva/internal/store"
)

// The small helpers the pages and actions share: formatting, file copying, the
// desktop's clipboard and file manager, and the cache the settings page can
// rebuild when the user edits its directories.

// humanBytes is a file size as a person reads it.
func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// strconvInt is the string form of an id, for a metadata field.
func strconvInt(v int64) string { return strconv.FormatInt(v, 10) }

// joinLines and splitLines convert between a list and the text area that edits
// it.
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func splitLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// newCacheWith rebuilds the cache when the user changes its directories.
func newCacheWith(paths store.Paths, extra []string) *store.Cache {
	return store.NewCache(paths, extra)
}

// storeSessionFrom is the session file's contents for a signed-in account.
func storeSessionFrom(client *ncm.Client, user ncm.UserInfo) store.Session {
	return store.Session{Cookies: client.Cookies(), User: user}
}

// exportPath is where a cached song is written when the user exports it: the
// account's music folder, named after the song and its singer. The extension
// follows the file's own container, sniffed from its first bytes rather than
// assumed from the quality tier.
func (a *App) exportPath(song ncm.Song) string {
	dir := filepath.Join(homeOrDefault(), "Music")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = a.paths.Data
	}
	name := sanitizeFileName(song.Name + " - " + song.Singer())
	extension := ".mp3"
	if path, _, ok := a.cache.Lookup(song.ID, a.config.PreferredQuality()); ok {
		if file, err := os.Open(path); err == nil {
			var head [4]byte
			if _, err := io.ReadFull(file, head[:]); err == nil {
				switch {
				case string(head[:]) == "fLaC":
					extension = ".flac"
				case string(head[:]) == "RIFF":
					extension = ".wav"
				}
			}
			file.Close()
		}
	}
	return filepath.Join(dir, name+extension)
}

// sanitizeFileName removes the characters a file system refuses.
func sanitizeFileName(name string) string {
	replacer := strings.NewReplacer("/", "／", "\\", "＼", ":", "：", "*", "＊", "?", "？", "\"", "＂", "<", "＜", ">", "＞", "|", "｜", "\n", " ")
	cleaned := strings.TrimSpace(replacer.Replace(name))
	if len([]rune(cleaned)) > 120 {
		cleaned = string([]rune(cleaned)[:120])
	}
	if cleaned == "" {
		cleaned = "song"
	}
	return cleaned
}

func homeOrDefault() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return os.TempDir()
}

// copyFile copies one file, leaving the destination's directory created.
func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// downloadTo fetches a URL into a cache path, through a temporary file so a
// partial transfer is never mistaken for a finished one.
func downloadTo(ctx context.Context, rawURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", userAgentHeader)
	request.Header.Set("Referer", "https://music.163.com")
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	part := destination + ".part"
	file, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		file.Close()
		os.Remove(part)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, destination)
}

// appSetClipboard puts text on the system clipboard.
func appSetClipboard(text string) error { return app.SetClipboardText(text) }

// appOpenURL asks the desktop to open a link in its browser.
func appOpenURL(rawURL string) error { return app.OpenURL(rawURL) }

// appOpenPath asks the desktop to show a file or directory.
func appOpenPath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return app.OpenPath(path)
}

// listState helpers: every list keeps its state in the model so a repaint does
// not lose the scroll or the choice.
func (a *App) initLists() {
	if a.lyricList.Key == nil {
		a.lyricList.Key = func(i int) int { return i }
		a.lyricList.Label = func(i int) string {
			top := a.top()
			data := a.data.lyrics[top.id]
			if data == nil || i < 0 || i >= len(data.lyric.Lines) {
				return ""
			}
			return data.lyric.Lines[i].Text
		}
		a.lyricList.NoTypeahead = true
	}
}

// initQueueList gives the queue its row names, keys and drag-reorder, once.
func (a *App) initQueueList() {
	if a.queueList.Label != nil {
		return
	}
	a.queueList.Label = func(i int) string {
		if i < 0 || i >= len(a.queue) {
			return ""
		}
		return a.queue[i].Name
	}
	a.queueList.Key = func(i int) int {
		if i < 0 || i >= len(a.queue) {
			return i
		}
		// A song id fits the list's 31-bit key range without hashing, so two
		// different songs never share a row identity.
		return int(a.queue[i].ID & 0x7FFFFFFF)
	}
	a.queueList.Reorder = a.reorderQueue
}

// reorderQueue moves the dragged rows to their new place, keeping the song that
// is playing playing.
func (a *App) reorderQueue(rows []int, to int) {
	if len(rows) == 0 {
		return
	}
	playingID := int64(0)
	if song, ok := a.current(); ok {
		playingID = song.ID
	}
	moving := make([]ncm.Song, 0, len(rows))
	rest := make([]ncm.Song, 0, len(a.queue))
	for i, song := range a.queue {
		inRows := false
		for _, row := range rows {
			if row == i {
				inRows = true
				break
			}
		}
		if inRows {
			moving = append(moving, song)
		} else {
			rest = append(rest, song)
		}
	}
	// The drop index counts the original rows: translate it into the remaining
	// rows' own numbering before inserting.
	insertAt := to
	for _, row := range rows {
		if row < to {
			insertAt--
		}
	}
	if insertAt < 0 {
		insertAt = 0
	}
	if insertAt > len(rest) {
		insertAt = len(rest)
	}
	next := make([]ncm.Song, 0, len(a.queue))
	next = append(next, rest[:insertAt]...)
	next = append(next, moving...)
	next = append(next, rest[insertAt:]...)
	a.queue = next
	for i, song := range a.queue {
		if song.ID == playingID {
			a.queueAt = i
			break
		}
	}
	a.persistQueue()
	a.invalidate()
}
