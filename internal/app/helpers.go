package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vivaui/viva/app"

	"cetone/internal/ncm"
	"cetone/internal/store"
)

// humanBytes formats a byte size into a human-readable unit string.
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

func strconvInt(v int64) string { return strconv.FormatInt(v, 10) }

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

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

func newCacheWith(paths store.Paths, extra []string) *store.Cache {
	return store.NewCache(paths, extra)
}

func storeSessionFrom(client *ncm.Client, user ncm.UserInfo) store.Session {
	return store.Session{Cookies: client.Cookies(), User: user}
}

// exportPath builds a target file path under ~/Music with an extension sniffed
// from the cached file's magic header.
func (a *App) exportPath(song ncm.Song) string {
	dir := filepath.Join(homeOrDefault(), "Music")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = a.paths.Data
	}
	name := sanitizeFileName(song.Name + " - " + song.Singer())
	extension := ".mp3"
	if path, _, ok := a.cache.LookupBest(song.ID); ok {
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
			_ = file.Close()
		}
	}
	return filepath.Join(dir, name+extension)
}

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
		_ = out.Close()
		return err
	}
	return out.Close()
}

func appSetClipboard(text string) error { return app.SetClipboardText(text) }
func appOpenURL(rawURL string) error    { return app.OpenURL(rawURL) }

func appOpenPath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return app.OpenPath(path)
}

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
		return int(a.queue[i].ID & 0x7FFFFFFF)
	}
	a.queueList.Reorder = a.reorderQueue
}

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
