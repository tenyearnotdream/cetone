package app

import (
	"context"
	"time"

	"ncmviva/internal/mpris"
	"ncmviva/internal/ncm"
)

// The desktop's media controls: the media keys and the sound-menu widget reach
// the player through MPRIS on Linux, and through nothing on the platforms that
// have no such protocol.

// startMPRIS exports the player on the session bus. A desktop without one is
// not an error: the window simply has no media controls.
func (a *App) startMPRIS() {
	server, err := mpris.New(
		mpris.Identity{Name: "ncmviva", Description: "网易云音乐", DesktopEntry: "ncmviva"},
		mpris.Handlers{
			Play:      func() { a.work.push(func() { a.togglePlay() }); a.invalidate() },
			Pause:     func() { a.work.push(func() { a.engine.Pause() }); a.invalidate() },
			PlayPause: func() { a.work.push(func() { a.togglePlay() }); a.invalidate() },
			Stop:      func() { a.work.push(func() { a.engine.Stop() }); a.invalidate() },
			Next:      func() { a.work.push(func() { a.next() }); a.invalidate() },
			Previous:  func() { a.work.push(func() { a.previous() }); a.invalidate() },
			Seek: func(offset mpris.Duration) {
				a.work.push(func() {
					position := a.engine.Snapshot().Position + time.Duration(offset)
					a.seekTo(position)
				})
				a.invalidate()
			},
			SetPosition: func(position mpris.Duration) {
				a.work.push(func() { a.seekTo(time.Duration(position)) })
				a.invalidate()
			},
			SetVolume: func(volume float64) {
				a.work.push(func() { a.setVolume(volume) })
				a.invalidate()
			},
			Quit: func() { a.work.push(func() { a.quitRequested = true }); a.invalidate() },
		},
	)
	if err != nil {
		return
	}
	a.mprisServer = server
	a.updateMPRIS()
}

// mprisUpdate publishes the current song and state.
func (a *App) updateMPRIS() {
	if a.mprisServer == nil {
		return
	}
	snapshot := a.engine.Snapshot()
	status := mpris.Stopped
	switch snapshot.Status.String() {
	case "playing", "buffering":
		status = mpris.Playing
	case "paused":
		status = mpris.Paused
	}
	track := mpris.Track{}
	if song, ok := a.current(); ok {
		track = mpris.Track{
			ID:     strconvInt(song.ID),
			Title:  song.Name,
			Artist: song.Singer(),
			Album:  song.Album.Name,
			Length: mpris.Duration(song.Duration),
			ArtURL: ncm.CoverURL(song.Album.PicURL, 512),
		}
	}
	a.mprisServer.Update(track, status, mpris.Duration(snapshot.Position), snapshot.Volume)
}

// stopMPRIS releases the bus name.
func (a *App) stopMPRIS() {
	if a.mprisServer != nil {
		a.mprisServer.Close()
		a.mprisServer = nil
	}
}

var _ = context.Background

// StartMPRISForTest exposes the media controls, for a program that opens its
// own window: Run does this itself.
func (a *App) StartMPRISForTest() { a.startMPRIS() }
