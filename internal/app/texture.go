package app

// The window's background: the sea the icon is drawn in.
//
// It is a picture, not a page of elements. The texture is a few thousand
// contour segments and a few thousand dots; declaring those as page elements
// would be re-tessellated on every paint, and a page repaints on every hover and
// every scroll. Instead the texture is drawn once into a bitmap when the
// appearance changes, and each paint draws that bitmap as one image.
//
// Everything here is deterministic: the same appearance and size give the same
// pixels, on every machine and in every run.

import (
	"math"

	"github.com/vivaui/viva/gfx"
)

const (
	// textureWidth and textureHeight are the picture's own size. It is drawn
	// scaled to the window, so it does not have to be rebuilt when the window is
	// resized: a background texture is not something a resize should stall on.
	textureWidth  = 1280
	textureHeight = 800

	// contourStep is the spacing of the sampling grid, in pixels. The contour
	// lines are traced with marching squares on this grid; a smaller step follows
	// the field more closely and costs more to trace.
	contourStep = 7.0

	// contourSpacing is how far apart the levels are in the field's own units.
	// Smaller means more rings.
	contourSpacing = 0.055

	// dotStep is the pitch of the micro dot grid.
	dotStep = 13.0
)

// oceanTexture is the background picture of one appearance.
type oceanTexture struct {
	bitmap gfx.Bitmap
	// dark records which appearance it was drawn for.
	dark bool
	// id is the identity the renderer uses to upload it once.
	id uint64
}

// oceanBitmap returns the texture for the current appearance, drawing it the
// first time it is asked for and after a theme change.
func (a *App) oceanBitmap() gfx.Bitmap {
	dark := isDark(a.theme)
	if a.ocean.id != 0 && a.ocean.dark == dark {
		return a.ocean.bitmap
	}
	a.ocean.bitmap = buildOceanTexture(a.theme, dark)
	a.ocean.dark = dark
	a.ocean.id = nextBitmapID()
	a.ocean.bitmap.ID = a.ocean.id
	return a.ocean.bitmap
}

// palette is the set of colors one appearance draws the texture with.
type texturePalette struct {
	base     gfx.Color // the flat sea
	glow     gfx.Color // the ambient light in the corners
	glowAt   [][3]float64
	contour  gfx.Color
	dot      gfx.Color
	contourA float64
	dotA     float64
}

func textureColors(theme gfx.Theme, dark bool) texturePalette {
	if dark {
		return texturePalette{
			base: gfx.RGB24(0x05080E),
			glow: gfx.RGB24(0x0EA5E9),
			glowAt: [][3]float64{
				// x, y, radius (in fractions of the picture) and strength.
				{0.06, 0.04, 0.62},
				{0.97, 0.10, 0.55},
				{0.88, 0.98, 0.50},
			},
			contour:  gfx.RGB24(0x1E88B8),
			dot:      gfx.RGB24(0x2E9BC4),
			contourA: 0.16,
			dotA:     0.13,
		}
	}
	return texturePalette{
		base: gfx.RGB24(0xF0F4F8),
		glow: gfx.RGB24(0x38BDF8),
		glowAt: [][3]float64{
			{0.06, 0.04, 0.62},
			{0.97, 0.10, 0.55},
			{0.88, 0.98, 0.50},
		},
		contour:  gfx.RGB24(0x64748B),
		dot:      gfx.RGB24(0x7C8FA6),
		contourA: 0.11,
		dotA:     0.10,
	}
}

// buildOceanTexture draws the background.
//
// The order is the picture's depth: the flat sea, the ambient light, the
// topographic contours, then the dot grid, so the dots sit on top of the lines
// the way a measured grid does on a map.
func buildOceanTexture(theme gfx.Theme, dark bool) gfx.Bitmap {
	const w, h = textureWidth, textureHeight
	colors := textureColors(theme, dark)

	// Straight (not premultiplied) RGBA while drawing, premultiplied at the end:
	// blending straight colors is what makes a low alpha line look like a line
	// rather than like darkness.
	buf := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		// The base is a vertical wash, slightly deeper at the bottom, so the
		// window has a floor.
		t := float64(y) / float64(h-1)
		r, g, b := mixRGB(colors.base, colors.base, 0)
		if dark {
			r = mix(r, 0.0, t*0.35)
			g = mix(g, 0.0, t*0.35)
			b = mix(b, 0.01, t*0.35)
		} else {
			// A pale wash toward the ice blue at the bottom.
			r = mix(r, 0.898, t*0.55)
			g = mix(g, 0.933, t*0.55)
			b = mix(b, 0.957, t*0.55)
		}
		row := buf[y*w*4:]
		for x := 0; x < w; x++ {
			row[x*4+0] = uint8(b * 255)
			row[x*4+1] = uint8(g * 255)
			row[x*4+2] = uint8(r * 255)
			row[x*4+3] = 255
		}
	}

	addGlow(buf, w, h, colors)

	traceContours(buf, w, h, colors)
	drawDots(buf, w, h, colors)

	// Premultiply, which is the format the renderer takes.
	for i := 0; i < len(buf); i += 4 {
		a := uint32(buf[i+3])
		if a == 255 {
			continue
		}
		buf[i+0] = uint8(uint32(buf[i+0]) * a / 255)
		buf[i+1] = uint8(uint32(buf[i+1]) * a / 255)
		buf[i+2] = uint8(uint32(buf[i+2]) * a / 255)
	}
	return gfx.Bitmap{Pix: buf, Width: w, Height: h, Stride: w * 4}
}

// mixRGB splits a color into 0..1 components.
func mixRGB(from, to gfx.Color, _ float64) (float64, float64, float64) {
	return float64(from.Red), float64(from.Green), float64(from.Blue)
}

// mix moves a from b toward t (0..1).
func mix(a, b, t float64) float64 { return a + (b-a)*t }

// addGlow paints the ambient light: a broad, soft falloff from a few points,
// brightening the corners the way light enters deep water.
func addGlow(buf []byte, w, h int, colors texturePalette) {
	gr := float64(colors.glow.Red)
	gg := float64(colors.glow.Green)
	gb := float64(colors.glow.Blue)
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w)
			var added float64
			for _, at := range colors.glowAt {
				d := math.Hypot(fx-at[0], (fy-at[1])*float64(w)/float64(h))
				if d > at[2] {
					continue
				}
				// Ease the falloff so the centre is not a hot spot and the edge
				// does not end in a visible ring.
				t := 1 - d/at[2]
				added += t * t * 0.5
			}
			if added <= 0 {
				continue
			}
			if added > 0.42 {
				added = 0.42
			}
			o := (y*w + x) * 4
			buf[o+0] = blendChannel(buf[o+0], gb, added)
			buf[o+1] = blendChannel(buf[o+1], gg, added)
			buf[o+2] = blendChannel(buf[o+2], gr, added)
		}
	}
}

// blendChannel mixes two 0..1 colors and returns the byte.
func blendChannel(current byte, tint float64, alpha float64) byte {
	c := float64(current) / 255
	return uint8(math.Min(255, (c*(1-alpha)+tint*alpha)*255))
}

// contourField is the scalar field the contour lines follow: rings around a
// focus, warped so they read as topography rather than as a target.
func contourField(x, y float64) float64 {
	u := x / textureWidth
	v := y / textureHeight
	dx := u - 0.22
	dy := (v - 0.30) * 0.78
	r := math.Hypot(dx, dy)
	wobble := 0.052*math.Sin(u*7.3+1.1) +
		0.040*math.Sin(v*9.1+0.35) +
		0.030*math.Sin((u+v)*5.3+2.2) +
		0.022*math.Sin((u-v)*11.0+0.8)
	// A gentle pull toward the upper right keeps the rings from being concentric
	// everywhere, which is what makes a contour map look drawn rather than
	// generated.
	r += 0.018 * math.Sin(u*3.1+v*2.7)
	return r + wobble
}

// traceContours draws the contour lines with marching squares: the field is
// sampled on a grid, and each cell whose corners straddle a level contributes the
// segment the level crosses it on.
func traceContours(buf []byte, w, h int, colors texturePalette) {
	cols := int(math.Ceil(float64(w)/contourStep)) + 1
	rows := int(math.Ceil(float64(h)/contourStep)) + 1
	values := make([]float64, cols*rows)
	for gy := 0; gy < rows; gy++ {
		y := math.Min(float64(gy)*contourStep, float64(h-1))
		for gx := 0; gx < cols; gx++ {
			x := math.Min(float64(gx)*contourStep, float64(w-1))
			values[gy*cols+gx] = contourField(x, y)
		}
	}

	cr := float64(colors.contour.Red)
	cg := float64(colors.contour.Green)
	cb := float64(colors.contour.Blue)

	for gy := 0; gy < rows-1; gy++ {
		for gx := 0; gx < cols-1; gx++ {
			v00 := values[gy*cols+gx]
			v10 := values[gy*cols+gx+1]
			v01 := values[(gy+1)*cols+gx]
			v11 := values[(gy+1)*cols+gx+1]
			lo := math.Min(math.Min(v00, v10), math.Min(v01, v11))
			hi := math.Max(math.Max(v00, v10), math.Max(v01, v11))
			// Only the levels this cell straddles can cross it.
			first := int(math.Ceil(lo / contourSpacing))
			last := int(math.Floor(hi / contourSpacing))
			if first > last {
				continue
			}
			x0 := float64(gx) * contourStep
			y0 := float64(gy) * contourStep
			x1 := x0 + contourStep
			y1 := y0 + contourStep
			for k := first; k <= last; k++ {
				level := float64(k) * contourSpacing
				// Where the level crosses each of the four edges, if it does.
				var px, py [4]float64
				var has [4]bool
				if (v00 < level) != (v10 < level) {
					px[0] = x0 + contourStep*(level-v00)/(v10-v00)
					py[0] = y0
					has[0] = true
				}
				if (v10 < level) != (v11 < level) {
					px[1] = x1
					py[1] = y0 + contourStep*(level-v10)/(v11-v10)
					has[1] = true
				}
				if (v01 < level) != (v11 < level) {
					px[2] = x0 + contourStep*(level-v01)/(v11-v01)
					py[2] = y1
					has[2] = true
				}
				if (v00 < level) != (v01 < level) {
					px[3] = x0
					py[3] = y0 + contourStep*(level-v00)/(v01-v00)
					has[3] = true
				}
				// Connect the crossing edges in pairs.
				var found [4]int
				n := 0
				for e := 0; e < 4; e++ {
					if has[e] {
						found[n] = e
						n++
					}
				}
				if n < 2 {
					continue
				}
				for pair := 0; pair+1 < n; pair += 2 {
					a, b := found[pair], found[pair+1]
					rasterLine(buf, w, h, px[a], py[a], px[b], py[b], cr, cg, cb, colors.contourA)
				}
			}
		}
	}
}

// drawDots paints the micro grid: one dot on a fixed pitch, faint enough to read
// as texture and not as content.
func drawDots(buf []byte, w, h int, colors texturePalette) {
	cr := float64(colors.dot.Red)
	cg := float64(colors.dot.Green)
	cb := float64(colors.dot.Blue)
	for y := dotStep / 2; y < float64(h); y += dotStep {
		for x := dotStep / 2; x < float64(w); x += dotStep {
			// One pixel of coverage is a dot at this pitch; a disc would blur
			// into the lines behind it.
			o := (int(y)*w + int(x)) * 4
			if o+3 >= len(buf) {
				continue
			}
			buf[o+0] = blendChannel(buf[o+0], cb, colors.dotA)
			buf[o+1] = blendChannel(buf[o+1], cg, colors.dotA)
			buf[o+2] = blendChannel(buf[o+2], cr, colors.dotA)
		}
	}
}

// rasterLine draws one anti-aliased hairline into the straight RGBA buffer.
// Coverage comes from the distance to the segment, so a diagonal line is as
// smooth as a horizontal one.
func rasterLine(buf []byte, w, h int, x0, y0, x1, y1, r, g, b, alpha float64) {
	dx := x1 - x0
	dy := y1 - y0
	length2 := dx*dx + dy*dy
	if length2 < 1e-9 {
		return
	}
	minX := int(math.Floor(math.Min(x0, x1))) - 1
	maxX := int(math.Ceil(math.Max(x0, x1))) + 1
	minY := int(math.Floor(math.Min(y0, y1))) - 1
	maxY := int(math.Ceil(math.Max(y0, y1))) + 1
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= w {
		maxX = w - 1
	}
	if maxY >= h {
		maxY = h - 1
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			// Distance from the pixel centre to the segment.
			px := float64(x) + 0.5
			py := float64(y) + 0.5
			t := ((px-x0)*dx + (py-y0)*dy) / length2
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			nx := x0 + t*dx
			ny := y0 + t*dy
			d := math.Hypot(px-nx, py-ny)
			coverage := 1 - d
			if coverage <= 0 {
				continue
			}
			cov := coverage * alpha
			o := (y*w + x) * 4
			buf[o+0] = blendChannel(buf[o+0], b, cov)
			buf[o+1] = blendChannel(buf[o+1], g, cov)
			buf[o+2] = blendChannel(buf[o+2], r, cov)
		}
	}
}
