//go:build !linux

package main

import "fmt"

// restartWithSoftwareRendering is only implemented on Linux: the other
// platforms' renderers do not need this fallback.
func restartWithSoftwareRendering() error {
	return fmt.Errorf("software rendering fallback is not implemented on this platform")
}
