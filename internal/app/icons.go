package app

import "github.com/vivaui/viva/gfx"

// The application's glyphs. They are written as path commands rather than SVG
// sources because there are only a handful and they are all Lucide-shaped: a
// 24-unit box, a 2-unit round stroke, geometry that reads at 16 px. A glyph
// with Stroke zero is filled instead of stroked (the play triangle).

// kappa is the cubic control-point distance that makes a quarter circle.
const kappa = 0.5522847498

// circle is a closed circle as four cubic segments.
func circle(cx, cy, r float32) []gfx.PathCommand {
	k := r * kappa
	return []gfx.PathCommand{
		gfx.MoveTo(cx+r, cy),
		gfx.CubicTo(cx+r, cy+k, cx+k, cy+r, cx, cy+r),
		gfx.CubicTo(cx-k, cy+r, cx-r, cy+k, cx-r, cy),
		gfx.CubicTo(cx-r, cy-k, cx-k, cy-r, cx, cy-r),
		gfx.CubicTo(cx+k, cy-r, cx+r, cy-k, cx+r, cy),
		gfx.ClosePath(),
	}
}

// joinPaths concatenates path command slices.
func joinPaths(parts ...[]gfx.PathCommand) []gfx.PathCommand {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]gfx.PathCommand, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// stroked wraps commands in the 24-unit, 2-unit stroke every glyph here uses.
func stroked(commands ...gfx.PathCommand) gfx.Icon {
	return gfx.Icon{
		ViewBox:  24,
		Stroke:   2,
		Cap:      gfx.CapRound,
		Join:     gfx.JoinRound,
		Commands: commands,
	}
}

// filled wraps commands in a glyph painted as a solid shape.
func filled(commands ...gfx.PathCommand) gfx.Icon {
	return gfx.Icon{ViewBox: 24, Commands: commands}
}

var (
	iconPlay = filled(
		gfx.MoveTo(7, 4.5), gfx.LineTo(19, 12), gfx.LineTo(7, 19.5), gfx.ClosePath(),
	)
	iconPause = filled(
		gfx.MoveTo(7, 4.5), gfx.LineTo(10.5, 4.5), gfx.LineTo(10.5, 19.5), gfx.LineTo(7, 19.5), gfx.ClosePath(),
		gfx.MoveTo(13.5, 4.5), gfx.LineTo(17, 4.5), gfx.LineTo(17, 19.5), gfx.LineTo(13.5, 19.5), gfx.ClosePath(),
	)
	iconPrevious = filled(
		gfx.MoveTo(19, 5), gfx.LineTo(9, 12), gfx.LineTo(19, 19), gfx.ClosePath(),
		gfx.MoveTo(6, 5), gfx.LineTo(8, 5), gfx.LineTo(8, 19), gfx.LineTo(6, 19), gfx.ClosePath(),
	)
	iconNext = filled(
		gfx.MoveTo(5, 5), gfx.LineTo(15, 12), gfx.LineTo(5, 19), gfx.ClosePath(),
		gfx.MoveTo(16, 5), gfx.LineTo(18, 5), gfx.LineTo(18, 19), gfx.LineTo(16, 19), gfx.ClosePath(),
	)
	iconVolume = stroked(
		gfx.MoveTo(11, 5), gfx.LineTo(6, 9), gfx.LineTo(3, 9), gfx.LineTo(3, 15), gfx.LineTo(6, 15), gfx.LineTo(11, 19), gfx.ClosePath(),
	)
	iconVolumeWaves = stroked(
		gfx.MoveTo(11, 5), gfx.LineTo(6, 9), gfx.LineTo(3, 9), gfx.LineTo(3, 15), gfx.LineTo(6, 15), gfx.LineTo(11, 19), gfx.ClosePath(),
		gfx.MoveTo(15, 9), gfx.QuadTo(17.5, 12, 15, 15),
		gfx.MoveTo(18, 6), gfx.QuadTo(22, 12, 18, 18),
	)
	iconMute = stroked(
		gfx.MoveTo(11, 5), gfx.LineTo(6, 9), gfx.LineTo(3, 9), gfx.LineTo(3, 15), gfx.LineTo(6, 15), gfx.LineTo(11, 19), gfx.ClosePath(),
		gfx.MoveTo(16, 9), gfx.LineTo(22, 15),
		gfx.MoveTo(22, 9), gfx.LineTo(16, 15),
	)
	iconSearch = stroked(
		append(circle(10.5, 10.5, 6.5), gfx.MoveTo(15.5, 15.5), gfx.LineTo(21, 21))...,
	)
	iconHeart = stroked(
		gfx.MoveTo(12, 20),
		gfx.CubicTo(12, 20, 3, 14.5, 3, 8.6),
		gfx.CubicTo(3, 5.5, 5.4, 3.5, 8, 3.5),
		gfx.CubicTo(10, 3.5, 11.3, 4.8, 12, 6),
		gfx.CubicTo(12.7, 4.8, 14, 3.5, 16, 3.5),
		gfx.CubicTo(18.6, 3.5, 21, 5.5, 21, 8.6),
		gfx.CubicTo(21, 14.5, 12, 20, 12, 20),
		gfx.ClosePath(),
	)
	iconHeartFilled = filled(
		gfx.MoveTo(12, 20.5),
		gfx.CubicTo(12, 20.5, 2.5, 14.6, 2.5, 8.6),
		gfx.CubicTo(2.5, 5.3, 5.1, 3, 8, 3),
		gfx.CubicTo(10, 3, 11.4, 4.3, 12, 5.6),
		gfx.CubicTo(12.6, 4.3, 14, 3, 16, 3),
		gfx.CubicTo(18.9, 3, 21.5, 5.3, 21.5, 8.6),
		gfx.CubicTo(21.5, 14.6, 12, 20.5, 12, 20.5),
		gfx.ClosePath(),
	)
	iconDiscover = stroked(
		append(circle(12, 12, 9), gfx.MoveTo(15.5, 8.5), gfx.LineTo(13.5, 13.5), gfx.LineTo(8.5, 15.5), gfx.LineTo(10.5, 10.5), gfx.ClosePath())...,
	)
	iconChart = stroked(
		gfx.MoveTo(4, 20), gfx.LineTo(4, 12),
		gfx.MoveTo(10, 20), gfx.LineTo(10, 5),
		gfx.MoveTo(16, 20), gfx.LineTo(16, 9),
		gfx.MoveTo(22, 20), gfx.LineTo(2, 20),
	)
	iconLibrary = stroked(
		gfx.MoveTo(4, 5), gfx.LineTo(4, 19),
		gfx.MoveTo(9, 5), gfx.LineTo(9, 19),
		gfx.MoveTo(14, 8), gfx.LineTo(20, 6), gfx.LineTo(20, 18), gfx.LineTo(14, 20), gfx.ClosePath(),
	)
	iconUser = stroked(
		append(circle(12, 8.5, 4),
			gfx.MoveTo(4.5, 20.5), gfx.CubicTo(4.5, 16.6, 8, 14.5, 12, 14.5), gfx.CubicTo(16, 14.5, 19.5, 16.6, 19.5, 20.5))...,
	)
	iconSettings = stroked(
		append(circle(12, 12, 3.2),
			gfx.MoveTo(12, 2.5), gfx.LineTo(12, 6),
			gfx.MoveTo(12, 18), gfx.LineTo(12, 21.5),
			gfx.MoveTo(2.5, 12), gfx.LineTo(6, 12),
			gfx.MoveTo(18, 12), gfx.LineTo(21.5, 12),
			gfx.MoveTo(5.3, 5.3), gfx.LineTo(7.7, 7.7),
			gfx.MoveTo(16.3, 16.3), gfx.LineTo(18.7, 18.7),
			gfx.MoveTo(5.3, 18.7), gfx.LineTo(7.7, 16.3),
			gfx.MoveTo(16.3, 7.7), gfx.LineTo(18.7, 5.3))...,
	)
	iconQueue = stroked(
		append(circle(5, 7, 1.6),
			gfx.MoveTo(10, 7), gfx.LineTo(21, 7),
			gfx.MoveTo(10, 12), gfx.LineTo(21, 12),
			gfx.MoveTo(10, 17), gfx.LineTo(21, 17),
			gfx.MoveTo(3, 12), gfx.LineTo(6, 12),
			gfx.MoveTo(3.5, 17), gfx.LineTo(6.5, 17))...,
	)
	iconLyrics = stroked(joinPaths(
		circle(8, 18, 3),
		[]gfx.PathCommand{gfx.MoveTo(11, 18), gfx.LineTo(11, 5), gfx.LineTo(20, 3), gfx.LineTo(20, 15)},
		circle(17, 15, 3),
	)...)
	iconBack = stroked(
		gfx.MoveTo(15, 5), gfx.LineTo(8, 12), gfx.LineTo(15, 19),
	)
	iconClose = stroked(
		gfx.MoveTo(6, 6), gfx.LineTo(18, 18),
		gfx.MoveTo(18, 6), gfx.LineTo(6, 18),
	)
	iconRefresh = stroked(
		gfx.MoveTo(20, 12), gfx.CubicTo(20, 16.4, 16.4, 20, 12, 20), gfx.CubicTo(7.6, 20, 4, 16.4, 4, 12), gfx.CubicTo(4, 7.6, 7.6, 4, 12, 4),
		gfx.MoveTo(4, 4), gfx.LineTo(4, 9), gfx.LineTo(9, 9),
		gfx.MoveTo(20, 20), gfx.LineTo(20, 15), gfx.LineTo(15, 15),
	)
	iconDownload = stroked(
		gfx.MoveTo(12, 3.5), gfx.LineTo(12, 14), gfx.MoveTo(7.5, 9.5), gfx.LineTo(12, 14), gfx.LineTo(16.5, 9.5),
		gfx.MoveTo(4, 17), gfx.LineTo(4, 20), gfx.LineTo(20, 20), gfx.LineTo(20, 17),
	)
	iconShuffle = stroked(
		gfx.MoveTo(3, 6), gfx.LineTo(7, 6), gfx.CubicTo(13, 6, 12, 18, 18, 18), gfx.LineTo(21, 18),
		gfx.MoveTo(18, 15), gfx.LineTo(21, 18), gfx.LineTo(18, 21),
		gfx.MoveTo(3, 18), gfx.LineTo(7, 18), gfx.CubicTo(10, 18, 11, 14, 12.5, 12),
		gfx.MoveTo(18, 3), gfx.LineTo(21, 6), gfx.LineTo(18, 9),
		gfx.MoveTo(16, 6), gfx.LineTo(21, 6),
	)
	iconRepeat = stroked(
		gfx.MoveTo(6, 7), gfx.LineTo(18, 7), gfx.LineTo(21, 10),
		gfx.MoveTo(6, 7), gfx.LineTo(3, 10),
		gfx.MoveTo(18, 17), gfx.LineTo(6, 17), gfx.LineTo(3, 14),
		gfx.MoveTo(18, 17), gfx.LineTo(21, 14),
	)
	iconRepeatOne = stroked(
		gfx.MoveTo(6, 7), gfx.LineTo(18, 7), gfx.LineTo(21, 10),
		gfx.MoveTo(6, 7), gfx.LineTo(3, 10),
		gfx.MoveTo(18, 17), gfx.LineTo(6, 17), gfx.LineTo(3, 14),
		gfx.MoveTo(18, 17), gfx.LineTo(21, 14),
		gfx.MoveTo(11.5, 10), gfx.LineTo(12.5, 9.4), gfx.LineTo(12.5, 14.6),
	)
	iconPlus  = stroked(gfx.MoveTo(12, 5), gfx.LineTo(12, 19), gfx.MoveTo(5, 12), gfx.LineTo(19, 12))
	iconTrash = stroked(
		gfx.MoveTo(4, 7), gfx.LineTo(20, 7),
		gfx.MoveTo(9, 7), gfx.LineTo(9, 4), gfx.LineTo(15, 4), gfx.LineTo(15, 7),
		gfx.MoveTo(6, 7), gfx.LineTo(7.5, 20), gfx.LineTo(16.5, 20), gfx.LineTo(18, 7),
	)
	iconCloud = stroked(
		gfx.MoveTo(7, 18), gfx.CubicTo(3.5, 18, 2, 15.6, 2.6, 13.2), gfx.CubicTo(3.1, 11.2, 5, 10, 6.8, 10.2),
		gfx.CubicTo(7.4, 6.6, 10.4, 4.5, 13.6, 5.3), gfx.CubicTo(16.2, 6, 17.8, 8.2, 17.9, 10.6),
		gfx.CubicTo(20.3, 10.7, 22, 12.4, 22, 14.3), gfx.CubicTo(22, 16.4, 20.4, 18, 18.3, 18), gfx.ClosePath(),
	)
	iconRadio = stroked(joinPaths(
		circle(12, 12, 9),
		circle(12, 12, 3.4),
		[]gfx.PathCommand{gfx.MoveTo(12, 3), gfx.LineTo(20, 3), gfx.MoveTo(18.4, 5.6), gfx.LineTo(21, 3)},
	)...)
	iconFolder = stroked(
		gfx.MoveTo(3, 7), gfx.LineTo(3, 19), gfx.LineTo(21, 19), gfx.LineTo(21, 8), gfx.LineTo(12, 8), gfx.LineTo(10, 5), gfx.LineTo(3, 5), gfx.ClosePath(),
	)
	iconInfo = stroked(
		append(circle(12, 12, 9),
			gfx.MoveTo(12, 11), gfx.LineTo(12, 17),
			gfx.MoveTo(12, 7.5), gfx.LineTo(12, 7.6))...,
	)
	iconChevronDown  = stroked(gfx.MoveTo(6, 9), gfx.LineTo(12, 15), gfx.LineTo(18, 9))
	iconChevronRight = stroked(gfx.MoveTo(9, 6), gfx.LineTo(15, 12), gfx.LineTo(9, 18))
	iconList         = stroked(
		gfx.MoveTo(4, 6), gfx.LineTo(20, 6),
		gfx.MoveTo(4, 12), gfx.LineTo(20, 12),
		gfx.MoveTo(4, 18), gfx.LineTo(20, 18),
	)
	iconDisc = stroked(joinPaths(circle(12, 12, 9), circle(12, 12, 2))...)
	iconWave = stroked(
		gfx.MoveTo(3, 12), gfx.LineTo(3, 12.1),
		gfx.MoveTo(7.5, 8), gfx.LineTo(7.5, 16),
		gfx.MoveTo(12, 4.5), gfx.LineTo(12, 19.5),
		gfx.MoveTo(16.5, 8), gfx.LineTo(16.5, 16),
		gfx.MoveTo(21, 12), gfx.LineTo(21, 12.1),
	)
)

// iconPlayPause picks the transport glyph for a playing state.
func iconPlayPause(playing bool) gfx.Icon {
	if playing {
		return iconPause
	}
	return iconPlay
}
