// Package mpris exposes the player to the Linux desktop's media controls:
// the media keys, the sound-menu widget, and any MPRIS client.
//
// The interface is org.mpris.MediaPlayer2 and org.mpris.MediaPlayer2.Player on
// the session bus. Calls that change playback arrive on the bus thread and go
// straight to the engine through the callbacks given to New; state changes are
// published with Update, which is safe from any goroutine.
package mpris

// Track is what a client shows and what its controls act on.
type Track struct {
	// ID is a stable identifier for the track, the song id as a string.
	ID     string
	Title  string
	Artist string
	Album  string
	// Length is the track's duration; a zero length is reported as unknown.
	Length Duration
	// ArtURL is the album art, fetchable by the client.
	ArtURL string
}

// Duration is a length in microseconds, MPRIS's own unit.
type Duration int64

// Status is the transport state a client reads.
type Status int

const (
	Stopped Status = iota
	Playing
	Paused
)

// Handlers are what the desktop's buttons call. They run on the bus goroutine,
// so they must only publish work for the window thread.
type Handlers struct {
	Play        func()
	Pause       func()
	PlayPause   func()
	Stop        func()
	Next        func()
	Previous    func()
	Seek        func(offset Duration)
	SetPosition func(position Duration)
	SetVolume   func(volume float64)
	Raise       func()
	Quit        func()
}

// Identity is what the desktop's widget titles the application.
type Identity struct {
	// Name is the application name ("ncmviva").
	Name string
	// Description is the long name the sound menu shows.
	Description string
	// DesktopEntry is the .desktop file name without its suffix.
	DesktopEntry string
}
