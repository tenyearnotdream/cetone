//go:build linux

package main

import (
	"os"
	"syscall"
)

// restartWithSoftwareRendering replaces this process with the same program run
// with VIVA_SOFTWARE_RENDERING=1, so the window opens on the CPU renderer. The
// process is replaced rather than forked, so there is one window and one
// process, as a user expects.
func restartWithSoftwareRendering() error {
	environment := os.Environ()
	replaced := false
	for i, entry := range environment {
		if len(entry) >= len("VIVA_SOFTWARE_RENDERING=") && entry[:len("VIVA_SOFTWARE_RENDERING=")] == "VIVA_SOFTWARE_RENDERING=" {
			environment[i] = "VIVA_SOFTWARE_RENDERING=1"
			replaced = true
			break
		}
	}
	if !replaced {
		environment = append(environment, "VIVA_SOFTWARE_RENDERING=1")
	}
	return syscall.Exec(os.Args[0], os.Args, environment)
}
