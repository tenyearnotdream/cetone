package store

import (
	"path/filepath"
	"time"

	"ncmviva/internal/ncm"
)

// Session is the saved login and the account it belongs to.
type Session struct {
	Cookies map[string]string `json:"cookies"`
	User    ncm.UserInfo      `json:"user"`
	SavedAt time.Time         `json:"savedAt"`
}

// Sessions reads and writes the session file.
type Sessions struct {
	path string
}

// NewSessions points at the session file under the paths.
func NewSessions(paths Paths) *Sessions {
	return &Sessions{path: filepath.Join(paths.Config, "session.json")}
}

// Load reads the saved session; a missing file is not an error.
func (s *Sessions) Load() (Session, bool, error) {
	var session Session
	existed, err := readJSON(s.path, &session)
	if err != nil {
		return Session{}, false, err
	}
	if !existed || len(session.Cookies) == 0 {
		return Session{}, false, nil
	}
	return session, true, nil
}

// Save writes the session.
func (s *Sessions) Save(session Session) error {
	session.SavedAt = time.Now()
	return writeJSON(s.path, session)
}

// Clear removes the saved session, for a logout.
func (s *Sessions) Clear() error {
	err := removeIfExists(s.path)
	return err
}

// Path is where the session lives, for a settings page that shows it.
func (s *Sessions) Path() string { return s.path }
