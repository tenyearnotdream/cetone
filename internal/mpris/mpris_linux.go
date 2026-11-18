//go:build linux

package mpris

import (
	"fmt"
	"math"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// objectPath is the one player this application exposes.
const objectPath = dbus.ObjectPath("/org/mpris/MediaPlayer2")

// Server is the player's presence on the session bus.
type Server struct {
	conn     *dbus.Conn
	props    *prop.Properties
	name     string
	handlers Handlers

	mu       sync.Mutex
	track    Track
	status   Status
	position Duration
	volume   float64
	canSeek  bool
}

// New connects to the session bus and exports the player. A desktop with no
// session bus (a bare terminal, a container) reports the reason and the caller
// runs without media controls.
func New(identity Identity, handlers Handlers) (*Server, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("mpris: session bus: %w", err)
	}
	server := &Server{conn: conn, name: identity.Name, handlers: handlers, volume: 1, canSeek: true}

	// The two interfaces are exported as one object: root holds the
	// application's own properties, player the transport's.
	props, err := prop.Export(conn, objectPath, server.properties(identity))
	if err != nil {
		return nil, fmt.Errorf("mpris: export properties: %w", err)
	}
	server.props = props

	if err := conn.ExportWithMap(server, map[string]string{"SeekBy": "Seek"}, objectPath, "org.mpris.MediaPlayer2.Player"); err != nil {
		return nil, fmt.Errorf("mpris: export player: %w", err)
	}
	if err := conn.Export(server, objectPath, "org.mpris.MediaPlayer2"); err != nil {
		return nil, fmt.Errorf("mpris: export application: %w", err)
	}

	name := "org.mpris.MediaPlayer2." + identity.Name
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("mpris: request name %s: %w", name, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("mpris: %s is already taken by another player", name)
	}
	return server, nil
}

// properties builds the property map the two interfaces publish.
func (s *Server) properties(identity Identity) map[string]map[string]*prop.Prop {
	identityName := identity.Name
	if identityName == "" {
		identityName = "ncmviva"
	}
	description := identity.Description
	if description == "" {
		description = identityName
	}
	desktopEntry := identity.DesktopEntry
	if desktopEntry == "" {
		desktopEntry = identityName
	}
	return map[string]map[string]*prop.Prop{
		"org.mpris.MediaPlayer2": {
			"Identity":            {Value: identityName, Writable: false, Emit: prop.EmitFalse},
			"DesktopEntry":        {Value: desktopEntry, Writable: false, Emit: prop.EmitFalse},
			"CanQuit":             {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanRaise":            {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanSetFullscreen":    {Value: false, Writable: false, Emit: prop.EmitFalse},
			"HasTrackList":        {Value: false, Writable: false, Emit: prop.EmitFalse},
			"SupportedUriSchemes": {Value: []string{}, Writable: false, Emit: prop.EmitFalse},
			"SupportedMimeTypes":  {Value: []string{"audio/mpeg", "audio/flac"}, Writable: false, Emit: prop.EmitFalse},
		},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: "Stopped", Writable: false, Emit: prop.EmitTrue},
			"LoopStatus":     {Value: "Playlist", Writable: true, Emit: prop.EmitTrue, Callback: s.setLoop},
			"Rate":           {Value: 1.0, Writable: true, Emit: prop.EmitTrue},
			"Shuffle":        {Value: false, Writable: true, Emit: prop.EmitTrue},
			"Metadata":       {Value: map[string]dbus.Variant{}, Writable: false, Emit: prop.EmitTrue},
			"Volume":         {Value: 1.0, Writable: true, Emit: prop.EmitTrue, Callback: s.setVolume},
			"Position":       {Value: int64(0), Writable: false, Emit: prop.EmitFalse},
			"MinimumRate":    {Value: 1.0, Writable: false, Emit: prop.EmitFalse},
			"MaximumRate":    {Value: 1.0, Writable: false, Emit: prop.EmitFalse},
			"CanGoNext":      {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanGoPrevious":  {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanPlay":        {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanPause":       {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanSeek":        {Value: true, Writable: false, Emit: prop.EmitFalse},
			"CanControl":     {Value: true, Writable: false, Emit: prop.EmitFalse},
		},
	}
}

// Update publishes the current track and transport state. It is safe from any
// goroutine; a failure to publish is silent because a desktop without media
// controls is not an error the user can act on.
func (s *Server) Update(track Track, status Status, position Duration, volume float64) {
	s.mu.Lock()
	s.track = track
	s.status = status
	s.position = position
	s.volume = volume
	s.mu.Unlock()
	if s.props == nil {
		return
	}
	statusName := "Stopped"
	switch status {
	case Playing:
		statusName = "Playing"
	case Paused:
		statusName = "Paused"
	}
	s.props.SetMust("org.mpris.MediaPlayer2.Player", "PlaybackStatus", statusName)
	s.props.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", s.metadata())
	s.props.SetMust("org.mpris.MediaPlayer2.Player", "Volume", math.Max(0, math.Min(1, volume)))
}

// PublishPosition reports a seek's landing point without a full update.
func (s *Server) PublishPosition(position Duration) {
	s.mu.Lock()
	s.position = position
	s.mu.Unlock()
	if s.props == nil {
		return
	}
	s.props.SetMust("org.mpris.MediaPlayer2.Player", "Position", int64(position))
}

// metadata is the MPRIS metadata map for the current track.
func (s *Server) metadata() map[string]dbus.Variant {
	s.mu.Lock()
	track := s.track
	s.mu.Unlock()
	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/" + sanitize(track.ID))),
		"xesam:title":   dbus.MakeVariant(track.Title),
	}
	if track.Artist != "" {
		metadata["xesam:artist"] = dbus.MakeVariant([]string{track.Artist})
	}
	if track.Album != "" {
		metadata["xesam:album"] = dbus.MakeVariant(track.Album)
	}
	if track.Length > 0 {
		metadata["mpris:length"] = dbus.MakeVariant(int64(track.Length))
	}
	if track.ArtURL != "" {
		metadata["mpris:artUrl"] = dbus.MakeVariant(track.ArtURL)
	}
	return metadata
}

// sanitize keeps a track id to the characters a D-Bus object path allows.
func sanitize(id string) string {
	out := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		out = append(out, '0')
	}
	return string(out)
}

// ----- the Player interface's methods -----

// Next is org.mpris.MediaPlayer2.Player.Next.
func (s *Server) Next() *dbus.Error {
	if s.handlers.Next != nil {
		s.handlers.Next()
	}
	return nil
}

// Previous is org.mpris.MediaPlayer2.Player.Previous.
func (s *Server) Previous() *dbus.Error {
	if s.handlers.Previous != nil {
		s.handlers.Previous()
	}
	return nil
}

// Pause is org.mpris.MediaPlayer2.Player.Pause.
func (s *Server) Pause() *dbus.Error {
	if s.handlers.Pause != nil {
		s.handlers.Pause()
	}
	return nil
}

// PlayPause is org.mpris.MediaPlayer2.Player.PlayPause.
func (s *Server) PlayPause() *dbus.Error {
	if s.handlers.PlayPause != nil {
		s.handlers.PlayPause()
	}
	return nil
}

// Stop is org.mpris.MediaPlayer2.Player.Stop.
func (s *Server) Stop() *dbus.Error {
	if s.handlers.Stop != nil {
		s.handlers.Stop()
	}
	return nil
}

// Play is org.mpris.MediaPlayer2.Player.Play.
func (s *Server) Play() *dbus.Error {
	if s.handlers.Play != nil {
		s.handlers.Play()
	}
	return nil
}

// SeekBy is org.mpris.MediaPlayer2.Player.Seek, in microseconds, relative. It is
// exported under the name "Seek" (see New) because the Go method of that name
// would have to satisfy io.Seeker's signature, which a D-Bus method cannot.
func (s *Server) SeekBy(offset int64) *dbus.Error {
	if s.handlers.Seek != nil {
		s.handlers.Seek(Duration(offset))
	}
	return nil
}

// SetPosition is org.mpris.MediaPlayer2.Player.SetPosition.
func (s *Server) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	if s.handlers.SetPosition != nil {
		s.handlers.SetPosition(Duration(position))
	}
	return nil
}

// OpenUri is org.mpris.MediaPlayer2.Player.OpenUri; the application plays from
// its own library, so this reports that it is not supported.
func (s *Server) OpenUri(uri string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("mpris: opening a uri is not supported"))
}

// ----- the application interface's methods -----

// Raise is org.mpris.MediaPlayer2.Raise.
func (s *Server) Raise() *dbus.Error {
	if s.handlers.Raise != nil {
		s.handlers.Raise()
	}
	return nil
}

// Quit is org.mpris.MediaPlayer2.Quit.
func (s *Server) Quit() *dbus.Error {
	if s.handlers.Quit != nil {
		s.handlers.Quit()
	}
	return nil
}

// setVolume is the property callback.
func (s *Server) setVolume(change *prop.Change) *dbus.Error {
	value, ok := change.Value.(float64)
	if !ok {
		return dbus.MakeFailedError(fmt.Errorf("mpris: volume is not a number"))
	}
	if s.handlers.SetVolume != nil {
		s.handlers.SetVolume(value)
	}
	return nil
}

// setLoop is the property callback; the application's repeat mode is set from
// its own window, so the value is accepted and reported back.
func (s *Server) setLoop(change *prop.Change) *dbus.Error { return nil }

// Close releases the name and the connection.
func (s *Server) Close() {
	if s.conn == nil {
		return
	}
	s.conn.ReleaseName("org.mpris.MediaPlayer2." + s.name)
	s.conn.Close()
	s.conn = nil
}
