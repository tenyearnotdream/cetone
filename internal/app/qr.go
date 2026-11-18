package app

import (
	"image/color"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vivaui/viva/ui"
)

// The login QR code is rendered from the QR encoder's own image, one module at
// a time, because a Viva canvas has no image scaling and a module grid stays
// sharp at any window size.

// qrModules encodes a URL and returns its dark modules.
func qrModules(content string) ([]qrModule, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	code.DisableBorder = true
	image := code.Image(256)
	bounds := image.Bounds()
	// The encoder's image is square; sample its centre of each cell so a
	// rounding difference at the edge cannot flip a module.
	size := bounds.Dx()
	modules := make([]qrModule, 0, size*size/2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if isDark(image.At(x, y)) {
				modules = append(modules, qrModule{X: x, Y: y, Size: size})
			}
		}
	}
	return modules, nil
}

func isDark(c color.Color) bool {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return false
	}
	// The encoder's palette is black on white.
	return r < 0x8000 && g < 0x8000 && b < 0x8000
}

// qrModule is one dark square: its cell and the grid's width.
type qrModule struct {
	X, Y, Size int
}

// drawQR paints the modules inside bounds, with a quiet zone of four modules
// around the grid (the QR specification's own margin).
func drawQR(d *ui.DrawContext, modules []qrModule, bounds ui.Rect) {
	if len(modules) == 0 {
		return
	}
	size := modules[0].Size
	side := min(bounds.Width, bounds.Height)
	if side <= 0 {
		return
	}
	quiet := side * 4 / float32(size+8)
	grid := side - quiet*2
	if grid <= 0 {
		return
	}
	cell := grid / float32(size)
	originX := bounds.X + (bounds.Width-side)/2 + quiet
	originY := bounds.Y + (bounds.Height-side)/2 + quiet
	black := ui.Color{Alpha: 1}
	for _, module := range modules {
		x := originX + float32(module.X)*cell
		y := originY + float32(module.Y)*cell
		// A tiny overlap hides the seam between adjacent cells at fractional
		// scales, which is what a scanner needs at a small size.
		d.Canvas.Fill(ui.Rect{X: x, Y: y, Width: cell + 0.35, Height: cell + 0.35}, black)
	}
}
