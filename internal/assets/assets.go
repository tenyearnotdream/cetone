// Package assets holds the artwork the program carries inside its binary: the
// window and status-area icons. Keeping it a package rather than a path means a
// packaged build has the same picture as one run from the source tree.
package assets

import _ "embed"

// IconPNG is the application's icon as drawn: a blue whale on a near-black tile.
// The window, the status item and the page's own header all come from it,
// scaled to the size each needs.
//
//go:embed icon.png
var IconPNG []byte

// IconLightPNG is the same artwork on a pale tile. The dark tile disappears into
// a dark taskbar and turns into a black block on a pale one, so the appearance
// picks the variant: IconPNG for the dark look, this one for the light.
//
//go:embed icon-light.png
var IconLightPNG []byte
