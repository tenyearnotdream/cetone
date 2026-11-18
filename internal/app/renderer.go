package app

import (
	"strings"
)

// IsRendererFailure reports whether an error from Run means the window could not
// be created on the GPU: a missing Vulkan/XCB surface (VK_ERROR_EXTENSION_NOT_
// PRESENT is -7), a driver that will not initialize, or a missing library. The
// caller falls back to Viva's CPU renderer for those; a failure of the
// application's own is reported as it is.
func IsRendererFailure(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, fragment := range []string{
		"Vulkan", "vulkan",
		"create instance failed",
		"create XCB surface",
		"Metal", "Direct3D", "D3D11",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
