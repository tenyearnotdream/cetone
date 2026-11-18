package app

import (
	qrcode "github.com/skip2/go-qrcode"

	"github.com/vivaui/viva/ui"
)

// The login QR code is drawn from the encoder's module grid rather than from a
// scaled bitmap, so a module stays a sharp square at any window size.

// qrModule is one dark module of a login QR code: its cell and the grid's
// width in modules.
type qrModule struct {
	X, Y, Size int
}

// qrModules encodes a URL and returns its dark modules. The bitmap includes the
// quiet zone the encoder draws around the code; the panel adds its own margin,
// so the zone is cropped off here.
func qrModules(content string) ([]qrModule, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	grid := code.Bitmap()
	// The quiet zone is four modules on each side.
	const quiet = 4
	size := len(grid) - quiet*2
	if size <= 0 {
		return nil, errEmptyQR
	}
	modules := make([]qrModule, 0, size*size/2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if grid[y+quiet][x+quiet] {
				modules = append(modules, qrModule{X: x, Y: y, Size: size})
			}
		}
	}
	return modules, nil
}

// errEmptyQR reports a code that encoded to nothing, which is a programming
// mistake rather than a server answer.
var errEmptyQR = qrError("the login URL encoded to an empty QR code")

type qrError string

func (e qrError) Error() string { return string(e) }

// drawQR paints the modules inside bounds, with a margin of four modules as the
// QR specification asks.
func drawQR(d *ui.DrawContext, modules []qrModule, bounds ui.Rect) {
	if len(modules) == 0 {
		return
	}
	grid := modules[0].Size
	side := min(bounds.Width, bounds.Height)
	if side <= 0 || grid <= 0 {
		return
	}
	// Four modules of margin on each side, scaled to the box: the code itself
	// takes the rest.
	cell := side / float32(grid+8)
	originX := bounds.X + (bounds.Width-side)/2 + cell*4
	originY := bounds.Y + (bounds.Height-side)/2 + cell*4
	black := ui.Color{Alpha: 1}
	for _, module := range modules {
		x := originX + float32(module.X)*cell
		y := originY + float32(module.Y)*cell
		// A hair of overlap hides the seam between neighbouring cells at a
		// fractional scale, which is what a scanner needs at a small size.
		d.Canvas.Fill(ui.Rect{X: x, Y: y, Width: cell + 0.4, Height: cell + 0.4}, black)
	}
}
