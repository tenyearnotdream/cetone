//go:build !linux

package app

// mprisUpdate does nothing off Linux: MPRIS is the Linux desktop's own
// media-controls protocol.
func (a *App) mprisUpdate() {}
