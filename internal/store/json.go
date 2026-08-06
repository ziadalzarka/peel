package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"
)

// ErrNotFound reports a comment ID that is not in the store.
var ErrNotFound = errors.New("comment not found")

// fileFormat is the on-disk shape. Version lets the format change later
// without silently misreading old files.
type fileFormat struct {
	Version  int       `json:"version"`
	Comments []Comment `json:"comments"`
}

const currentVersion = 1

// params are the knobs a store on disk takes: where its timestamps and IDs come
// from, and how long a writer waits for the one before it. They are shared by
// every store here, so a per-review file behaves like the repository's own.
type params struct {
	now         func() time.Time
	newID       func() string
	lockTimeout time.Duration
	lockStale   time.Duration
}

// Option configures a store on disk.
type Option func(*params)

// WithClock overrides the timestamp source, for tests.
func WithClock(fn func() time.Time) Option {
	return func(p *params) { p.now = fn }
}

// WithIDGenerator overrides ID generation, for tests.
func WithIDGenerator(fn func() string) Option {
	return func(p *params) { p.newID = fn }
}

// WithLockTimeout sets how long a mutation waits for a competing writer.
func WithLockTimeout(d time.Duration) Option {
	return func(p *params) { p.lockTimeout = d }
}

// newParams applies opts over the defaults.
func newParams(opts []Option) params {
	p := params{
		now:         time.Now,
		newID:       randomID,
		lockTimeout: 5 * time.Second,
		lockStale:   30 * time.Second,
	}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// JSONStore persists comments to a single JSON file.
//
// Every mutation re-reads the file under a lock before writing, because the
// TUI and an agent invoked from another terminal can both write concurrently.
// Writes go through a temporary file and a rename, so an interrupted write
// cannot leave a half-written store behind.
type JSONStore struct {
	path string
	params
}

// NewJSONStore returns a store backed by the file at path. The file and its
// parent directory are created on first write.
func NewJSONStore(path string, opts ...Option) *JSONStore {
	return &JSONStore{path: path, params: newParams(opts)}
}

// Path returns the file the store reads and writes.
func (s *JSONStore) Path() string { return s.path }

// List returns matching comments, oldest first.
func (s *JSONStore) List(f Filter) ([]Comment, error) {
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	return filterComments(all, f), nil
}

// Get returns one comment by ID.
func (s *JSONStore) Get(id string) (Comment, error) {
	all, err := s.read()
	if err != nil {
		return Comment{}, err
	}
	return findComment(all, id)
}

// Add stores a comment, filling in its ID, author and timestamp when unset.
func (s *JSONStore) Add(c Comment) (Comment, error) {
	c, err := prepareComment(c, s.params)
	if err != nil {
		return Comment{}, err
	}

	err = s.mutate(func(all []Comment) ([]Comment, error) {
		next, added, err := addComment(all, c, s.params)
		if err != nil {
			return nil, err
		}
		c = added
		return next, nil
	})
	if err != nil {
		return Comment{}, err
	}
	return c, nil
}

// Update applies mutate to one comment and persists the result.
func (s *JSONStore) Update(id string, apply func(*Comment)) (Comment, error) {
	var updated Comment
	err := s.mutate(func(all []Comment) ([]Comment, error) {
		next, got, err := updateComment(all, id, apply)
		if err != nil {
			return nil, err
		}
		updated = got
		return next, nil
	})
	if err != nil {
		return Comment{}, err
	}
	return updated, nil
}

// Remove deletes one comment by ID.
func (s *JSONStore) Remove(id string) error {
	return s.mutate(func(all []Comment) ([]Comment, error) {
		return removeComment(all, id)
	})
}

// Clear deletes every comment matching the filter.
func (s *JSONStore) Clear(f Filter) (int, error) {
	removed := 0
	err := s.mutate(func(all []Comment) ([]Comment, error) {
		kept, n := clearComments(all, f)
		removed = n
		return kept, nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// read loads the store, treating a missing file as empty.
func (s *JSONStore) read() ([]Comment, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if len(b) == 0 {
		return nil, nil
	}

	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if f.Version > currentVersion {
		return nil, fmt.Errorf("%s: written by a newer peel (format version %d)", s.path, f.Version)
	}
	return f.Comments, nil
}

// mutate runs apply against the freshly-read store and writes the result back,
// holding a lock for the whole read-modify-write.
func (s *JSONStore) mutate(apply func([]Comment) ([]Comment, error)) error {
	release, err := lockFile(s.path, s.lockTimeout, s.lockStale)
	if err != nil {
		return err
	}
	defer release()

	all, err := s.read()
	if err != nil {
		return err
	}
	next, err := apply(all)
	if err != nil {
		return err
	}
	return s.write(next)
}

// write persists comments atomically: a temporary file in the same directory
// followed by a rename, so readers never observe a partial file.
func (s *JSONStore) write(comments []Comment) error {
	if comments == nil {
		comments = []Comment{}
	}
	sortComments(comments)

	return writeJSONFile(s.path, "comments", fileFormat{Version: currentVersion, Comments: comments})
}

// sortComments orders by creation time, then ID, so output is stable.
func sortComments(cs []Comment) {
	sort.SliceStable(cs, func(i, j int) bool {
		if !cs[i].CreatedAt.Equal(cs[j].CreatedAt) {
			return cs[i].CreatedAt.Before(cs[j].CreatedAt)
		}
		return cs[i].ID < cs[j].ID
	})
}

// randomID returns a short, collision-resistant identifier.
func randomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; fall back to a time-derived
		// value rather than panicking in a review tool.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))[:8]
	}
	return hex.EncodeToString(b[:])
}
