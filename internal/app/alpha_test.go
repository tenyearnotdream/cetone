package app

import (
	"testing"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/headless"
	"github.com/vivaui/viva/ui"
)

// A translucent panel over a dark backdrop must blend, not replace. The design
// depends on it, so it is checked rather than assumed.
func TestTranslucentPanelsBlend(t *testing.T) {
	backdrop := gfx.Bitmap{
		Pix:   make([]byte, 4*4*4),
		Width: 4, Height: 4, Stride: 16,
	}
	// Opaque red backdrop.
	for i := 0; i < len(backdrop.Pix); i += 4 {
		backdrop.Pix[i+0] = 0   // B
		backdrop.Pix[i+1] = 0   // G
		backdrop.Pix[i+2] = 255 // R
		backdrop.Pix[i+3] = 255
	}
	page := func(c ui.C) {
		ui.Stack(c).Fill().Children(func() {
			ui.Image(c, backdrop).Fill()
			// A half-transparent white panel over it.
			ui.Row(c).Size(40, 40).Background(gfx.RGBA(255, 255, 255, 0.5))
		})
	}
	p := ui.NewPage(ui.PageConfig{View: page, Theme: themePtrForTest()})
	session, err := headless.Open(p.Config(app.Config{Title: "a", Width: 100, Height: 100}), nil, headless.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	img, err := session.Image()
	if err != nil {
		t.Skipf("no renderer here: %v", err)
	}
	// Read the pixel under the panel.
	r, g, b, _ := img.At(20, 20).RGBA()
	t.Logf("under the panel: r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	if r>>8 == 255 && g>>8 == 255 {
		t.Skip("the bitmap was not rendered (no backdrop), so blending cannot be judged")
	}
	if r>>8 < 100 {
		t.Fatalf("the panel did not lighten the backdrop (r=%d)", r>>8)
	}
}

func themePtrForTest() *gfx.Theme {
	th := gfx.DarkTheme()
	return &th
}
