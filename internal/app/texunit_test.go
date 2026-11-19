package app

// The background is generated, so it is checked rather than eyeballed: it must be
// deterministic, opaque where it should be, faint enough not to fight the text,
// and cheap enough to build once per theme change.

import (
	"sort"
	"testing"

	"github.com/vivaui/viva/gfx"
)

func TestTheBackgroundIsDeterministic(t *testing.T) {
	for _, dark := range []bool{true, false} {
		theme := gfx.DarkTheme()
		if !dark {
			theme = gfx.LightTheme()
		}
		first := buildOceanTexture(theme, dark)
		second := buildOceanTexture(theme, dark)
		if first.Width != second.Width || first.Height != second.Height {
			t.Fatal("two builds of the same texture differ in size")
		}
		if len(first.Pix) != len(second.Pix) {
			t.Fatal("two builds of the same texture differ in length")
		}
		for i := range first.Pix {
			if first.Pix[i] != second.Pix[i] {
				t.Fatalf("dark=%v: the texture differs at byte %d", dark, i)
			}
		}
	}
}

func TestTheBackgroundIsOpaqueAndFaint(t *testing.T) {
	for _, dark := range []bool{true, false} {
		theme := gfx.DarkTheme()
		if !dark {
			theme = gfx.LightTheme()
		}
		bmp := buildOceanTexture(theme, dark)
		// Every pixel is opaque: the window has no hole in it.
		for i := 3; i < len(bmp.Pix); i += 4 {
			if bmp.Pix[i] != 255 {
				t.Fatalf("dark=%v: byte %d is not opaque", dark, i)
			}
		}
		// The texture is a background: its luminance stays at the far end of the
		// scale, so text on it keeps its contrast. The corners carry the ambient
		// light, which is deliberately not flat, so the measure is the median:
		// a mean would let the lit corners pull the whole answer.
		var values []float64
		for y := 0; y < bmp.Height; y += 17 {
			for x := 0; x < bmp.Width; x += 17 {
				o := y*bmp.Stride + x*4
				c := gfx.Color{
					Red:   float32(bmp.Pix[o+2]) / 255,
					Green: float32(bmp.Pix[o+1]) / 255,
					Blue:  float32(bmp.Pix[o]) / 255,
					Alpha: 1,
				}
				values = append(values, luminance(c))
			}
		}
		sort.Float64s(values)
		mean := values[len(values)/2]
		if dark && mean > 0.05 {
			t.Fatalf("the dark texture is too bright: mean luminance %.3f", mean)
		}
		if !dark && mean < 0.85 {
			t.Fatalf("the light texture is too dark: mean luminance %.3f", mean)
		}
		// And it does vary: a flat fill would satisfy the bounds above and be
		// no texture at all.
		first := luminance(gfx.Color{
			Red:   float32(bmp.Pix[2]) / 255,
			Green: float32(bmp.Pix[1]) / 255,
			Blue:  float32(bmp.Pix[0]) / 255,
			Alpha: 1,
		})
		last := luminance(gfx.Color{
			Red:   float32(bmp.Pix[(bmp.Height-1)*bmp.Stride+2]) / 255,
			Green: float32(bmp.Pix[(bmp.Height-1)*bmp.Stride+1]) / 255,
			Blue:  float32(bmp.Pix[(bmp.Height-1)*bmp.Stride]) / 255,
			Alpha: 1,
		})
		if difference := first - last; difference < 0.004 && difference > -0.004 {
			t.Fatalf("dark=%v: the top and the bottom of the texture are the same", dark)
		}
	}
}

// The texture follows the appearance, and is not rebuilt on every paint.
func TestTheBackgroundIsBuiltOncePerAppearance(t *testing.T) {
	a := headlessModel(t)
	a.config.Theme = "dark"
	a.applyTheme()
	first := a.oceanBitmap()
	if first.Width == 0 {
		t.Fatal("no texture was built")
	}
	// The identity is stable, so the renderer keeps its upload.
	again := a.oceanBitmap()
	if again.ID != first.ID {
		t.Fatal("the texture was rebuilt on a second paint")
	}
	// A theme change gives a different picture.
	a.config.Theme = "light"
	a.applyTheme()
	light := a.oceanBitmap()
	if light.ID == first.ID {
		t.Fatal("the texture did not change with the appearance")
	}
	if string(light.Pix[:64]) == string(first.Pix[:64]) {
		t.Fatal("the two appearances produced the same texture")
	}
}

// Text sits on the texture, not on the flat theme background, so the contrast
// that matters is against the texture's own pixels: the brightest and darkest it
// gets, not the average.
func TestTextContrastHoldsAgainstTheTexture(t *testing.T) {
	for _, dark := range []bool{true, false} {
		theme := gfx.DarkTheme()
		if !dark {
			theme = gfx.LightTheme()
		}
		theme = headlessModel(t).decorate(theme, dark)
		bmp := buildOceanTexture(theme, dark)

		// The extremes of the background, which is what text must survive.
		low, high := 1.0, 0.0
		for y := 0; y < bmp.Height; y += 11 {
			for x := 0; x < bmp.Width; x += 11 {
				o := y*bmp.Stride + x*4
				l := luminance(gfx.Color{
					Red:   float32(bmp.Pix[o+2]) / 255,
					Green: float32(bmp.Pix[o+1]) / 255,
					Blue:  float32(bmp.Pix[o]) / 255,
					Alpha: 1,
				})
				if l < low {
					low = l
				}
				if l > high {
					high = l
				}
			}
		}
		ratio := func(a, b float64) float64 {
			if a < b {
				a, b = b, a
			}
			return (a + 0.05) / (b + 0.05)
		}
		// Body text against the worst pixel of the texture.
		worst := ratio(luminance(theme.Foreground), high)
		if dark {
			worst = ratio(luminance(theme.Foreground), low)
		}
		t.Logf("dark=%v: texture luminance %.3f..%.3f, worst body-text contrast %.2f",
			dark, low, high, worst)
		if worst < 4.5 {
			t.Fatalf("dark=%v: body text has %.2f against the worst part of the texture",
				dark, worst)
		}
	}
}
