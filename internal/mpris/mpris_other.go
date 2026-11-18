//go:build !linux

package mpris

import "fmt"

// Server is absent off Linux: MPRIS is the Linux desktop's media-controls
// protocol, and each other platform has its own integration.
type Server struct{}

// New reports that this platform has no MPRIS.
func New(identity Identity, handlers Handlers) (*Server, error) {
	return nil, fmt.Errorf("mpris: not available on this platform")
}

// Update does nothing without a server.
func (s *Server) Update(track Track, status Status, position Duration, volume float64) {}

// PublishPosition does nothing without a server.
func (s *Server) SetPosition(position Duration) {}

// Close does nothing without a server.
func (s *Server) Close() {}
