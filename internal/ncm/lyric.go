package ncm

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lrcTime matches the start of a timed LRC line: [mm:ss.xx], [mm:ss.xxx] or the
// broken [mm:ss:xx] some lyric files use.
var lrcTime = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})[.:](\d{1,3})\]`)

// lrcOffset matches the file's own global offset line, in milliseconds.
var lrcOffset = regexp.MustCompile(`^\[offset:([+-]?\d+)\]`)

// ParseLRC turns LRC text and its translation into timed lines. A line with
// several timestamps becomes one line per timestamp, as a player needs. Lines
// that carry no timestamp (the title, the credits) are dropped, and an
// [offset:n] line moves every line by n milliseconds.
func ParseLRC(raw, translation string) []LyricLine {
	main, offset := parseLRCTimed(raw)
	if len(main) == 0 {
		return nil
	}
	var trans []timedLine
	if translation != "" {
		trans, _ = parseLRCTimed(translation)
	}
	// Index the translation by the millisecond it starts on: a translation line
	// is written to share its original's timestamp exactly. The file's offset
	// line applies to both, since the two are timed against the same audio.
	byTime := make(map[int64][]string, len(trans))
	for _, line := range trans {
		key := (line.at + offset).Milliseconds()
		byTime[key] = append(byTime[key], line.text)
	}
	out := make([]LyricLine, 0, len(main))
	for _, line := range main {
		if line.at+offset < 0 {
			continue
		}
		at := line.at + offset
		ll := LyricLine{At: at, Text: line.text}
		if texts, ok := byTime[at.Milliseconds()]; ok && len(texts) > 0 {
			ll.Translation = strings.Join(texts, "\n")
			delete(byTime, at.Milliseconds())
		}
		out = append(out, ll)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

type timedLine struct {
	at   time.Duration
	text string
}

// parseLRCTimed extracts the timed lines and the file's offset.
func parseLRCTimed(raw string) ([]timedLine, time.Duration) {
	var offset time.Duration
	var lines []timedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if m := lrcOffset.FindStringSubmatch(line); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				offset = time.Duration(n) * time.Millisecond
			}
			continue
		}
		// Collect every timestamp at the start of the line, then the text that
		// follows them.
		rest := line
		var times []time.Duration
		for {
			m := lrcTime.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			minutes, _ := strconv.Atoi(m[1])
			seconds, _ := strconv.Atoi(m[2])
			fraction := m[3]
			millis := 0
			switch len(fraction) {
			case 1:
				millis = atoiDefault(fraction) * 100
			case 2:
				millis = atoiDefault(fraction) * 10
			default:
				millis = atoiDefault(fraction)
			}
			times = append(times, time.Duration(minutes)*time.Minute+time.Duration(seconds)*time.Second+time.Duration(millis)*time.Millisecond)
			rest = rest[len(m[0]):]
		}
		if len(times) == 0 {
			continue
		}
		text := strings.TrimSpace(rest)
		if text == "" {
			continue
		}
		for _, at := range times {
			lines = append(lines, timedLine{at: at, text: text})
		}
	}
	return lines, offset
}

func atoiDefault(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// LineAt returns the line that is playing at d, and its index, or (-1, false)
// before the first line.
func (l Lyric) LineAt(d time.Duration) (int, bool) {
	if len(l.Lines) == 0 {
		return 0, false
	}
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].At > d }) - 1
	if i < 0 {
		return 0, false
	}
	return i, true
}
