package app

import (
	"testing"
)

// A login QR code has to be a QR code: a grid of modules a phone's camera can
// read. The regression: the encoder's bitmap was sampled as if it were the
// module grid, which drew a 256-module block instead of a 21-to-177 grid.

func TestQRCodesAreRealQRCodes(t *testing.T) {
	modules, err := qrModules("https://music.163.com/login?codekey=abc123def456")
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) == 0 {
		t.Fatal("the encoder produced no dark modules")
	}
	grid := modules[0].Size
	if grid < 21 || grid > 177 {
		t.Fatalf("the code is %d modules wide; a QR code is 21 to 177", grid)
	}
	// Every module is inside the grid, and roughly half are dark: a code that is
	// entirely dark or entirely light encodes nothing.
	for _, module := range modules {
		if module.X < 0 || module.X >= grid || module.Y < 0 || module.Y >= grid {
			t.Fatalf("a module sits at (%d,%d), outside a %d-wide grid", module.X, module.Y, grid)
		}
	}
	density := float64(len(modules)) / float64(grid*grid)
	if density < 0.3 || density > 0.7 {
		t.Fatalf("%.0f%% of the modules are dark, which is not a QR code", density*100)
	}
	// The three finder patterns are at three corners: each is a 7-module ring
	// with a 3-module core, so its corner module and the one inside are dark
	// while the ring between them is light.
	corner := func(x, y int) bool {
		for _, module := range modules {
			if module.X == x && module.Y == y {
				return true
			}
		}
		return false
	}
	for _, at := range [][2]int{{0, 0}, {grid - 7, 0}, {0, grid - 7}} {
		if !corner(at[0], at[1]) {
			t.Fatalf("no finder pattern at (%d,%d)", at[0], at[1])
		}
	}
}

// The code grows with the URL: a longer login key needs a bigger grid, which is
// what tells a real encoder from a fixed grid.
func TestALongerLoginKeyNeedsABiggerCode(t *testing.T) {
	short, err := qrModules("https://music.163.com/login?codekey=a")
	if err != nil {
		t.Fatal(err)
	}
	long, err := qrModules("https://music.163.com/login?codekey=" + "0123456789abcdef" + "0123456789abcdef" + "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if long[0].Size <= short[0].Size {
		t.Fatalf("a much longer URL encodes to a %d-wide grid, no bigger than the short one's %d", long[0].Size, short[0].Size)
	}
}
