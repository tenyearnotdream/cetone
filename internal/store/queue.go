package store

import (
	"os"
	"path/filepath"
	"time"

	"ncmviva/internal/ncm"
)

// Queue is the play queue as it survives a restart: the songs, which one was
// current, and how far into it the user had listened.
type Queue struct {
	Songs []ncm.Song `json:"songs"`
	Index int        `json:"index"`
	// Position is how far into the current song the last run reached.
	Position time.Duration `json:"position"`
	// WasPlaying reports that the last run ended while music played, so the
	// next one may offer to continue.
	WasPlaying bool      `json:"wasPlaying"`
	SavedAt    time.Time `json:"savedAt"`
}

// QueueStore reads and writes the queue file.
type QueueStore struct {
	path string
}

// NewQueueStore points at the queue file under the paths.
func NewQueueStore(paths Paths) *QueueStore {
	return &QueueStore{path: filepath.Join(paths.Config, "queue.json")}
}

// Load reads the queue; a missing file is empty.
func (q *QueueStore) Load() (Queue, bool, error) {
	var queue Queue
	existed, err := readJSON(q.path, &queue)
	if err != nil || !existed {
		return Queue{}, false, err
	}
	if queue.Index < 0 || queue.Index >= len(queue.Songs) {
		queue.Index = 0
	}
	return queue, true, nil
}

// Save writes the queue.
func (q *QueueStore) Save(queue Queue) error {
	queue.SavedAt = time.Now()
	return writeJSON(q.path, queue)
}

// Clear removes the saved queue.
func (q *QueueStore) Clear() error { return removeIfExists(q.path) }

// Path is the queue file, for a settings page.
func (q *QueueStore) Path() string { return q.path }

// removeIfExists deletes a file, treating absence as success.
func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}
