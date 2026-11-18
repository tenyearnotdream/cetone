// Package assets holds the artwork the program carries inside its binary: the
// window and status-area icon. Keeping it a package rather than a path means a
// packaged build has the same picture as one run from the source tree.
package assets

import _ "embed"

// IconPNG is the application's icon: a 512-pixel rounded square. The window
// icon and the status item both come from it, scaled to the size each needs.
//
//go:embed icon.png
var IconPNG []byte
