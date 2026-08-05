package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// JSONStore persists comments to a single JSON file.
//
// Every mutation re-reads the file under a lock before writing, because the
// TUI and an agent invoked from another terminal can both write concurrently.
// Writes go through a temporary file and a rename, so an interrupted write
// cannot leave a half-written store behind.
type JSONStore struct {
	path        string
	now         func() time.Time
	newID       func() string
	lockTimeout time.Duration
	lockStale   time.Duration
}

// Option configures a JSONStore.
type Option func(*JSONStore)

// WithClock overrides the timestamp source, for tests.
func WithClock(fn func() time.Time) Option {
	return func(s *JSONStore) { s.now = fn }
}

// WithIDGenerator overrides ID generation, for tests.
func WithIDGenerator(fn func() string) Option {
	return func(s *JSONStore) { s.newID = fn }
}

// WithLockTimeout sets how long a mutation waits for a competing writer.
func WithLockTimeout(d time.Duration) Option {
	return func(s *JSONStore) { s.lockTimeout = d }
}

// NewJSONStore returns a store backed by the file at path. The file and its
// parent directory are created on first write.
func NewJSONStore(path string, opts ...Option) *JSONStore {
	s := &JSONStore{
		path:        path,
		now:         time.Now,
		newID:       randomID,
		lockTimeout: 5 * time.Second,
		lockStale:   30 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Path returns the file the store reads and writes.
func (s *JSONStore) Path() string { return s.path }

// List returns matching comments, oldest first.
func (s *JSONStore) List(f Filter) ([]Comment, error) {
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(all))
	for _, c := range all {
		if f.Matches(c) {
			out = append(out, c)
		}
	}
	sortComments(out)
	return out, nil
}

// Get returns one comment by ID.
func (s *JSONStore) Get(id string) (Comment, error) {
	all, err := s.read()
	if err != nil {
		return Comment{}, err
	}
	for _, c := range all {
		if c.ID == id {
			return c, nil
		}
	}
	return Comment{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Add stores a comment, filling in its ID, author and timestamp when unset.
func (s *JSONStore) Add(c Comment) (Comment, error) {
	if err := c.Validate(); err != nil {
		return Comment{}, err
	}
	if c.Side == "" {
		c.Side = SideNew
	}
	if c.Author == "" {
		c.Author = AuthorUser
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = s.now().UTC()
	}

	err := s.mutate(func(all []Comment) ([]Comment, error) {
		if c.ID == "" {
			c.ID = s.uniqueID(all)
		}
		for _, existing := range all {
			if existing.ID == c.ID {
				return nil, fmt.Errorf("comment id %s already exists", c.ID)
			}
		}
		return append(all, c), nil
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
		for i := range all {
			if all[i].ID != id {
				continue
			}
			candidate := all[i]
			apply(&candidate)
			// The ID is the store's to control, not the caller's.
			candidate.ID = id
			if err := candidate.Validate(); err != nil {
				return nil, err
			}
			all[i] = candidate
			updated = candidate
			return all, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	})
	if err != nil {
		return Comment{}, err
	}
	return updated, nil
}

// Remove deletes one comment by ID.
func (s *JSONStore) Remove(id string) error {
	return s.mutate(func(all []Comment) ([]Comment, error) {
		for i, c := range all {
			if c.ID == id {
				return append(all[:i:i], all[i+1:]...), nil
			}
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	})
}

// Clear deletes every comment matching the filter.
func (s *JSONStore) Clear(f Filter) (int, error) {
	removed := 0
	err := s.mutate(func(all []Comment) ([]Comment, error) {
		kept := make([]Comment, 0, len(all))
		for _, c := range all {
			if f.Matches(c) {
				removed++
				continue
			}
			kept = append(kept, c)
		}
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
	release, err := s.lock()
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

// lock takes an exclusive advisory lock via an O_EXCL lock file, which works
// across processes without a platform-specific flock dependency.
func (s *JSONStore) lock() (release func(), err error) {
	lockPath := s.path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	deadline := time.Now().Add(s.lockTimeout)
	backoff := time.Millisecond
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("acquire lock %s: %w", lockPath, err)
		}

		// A lock left behind by a killed process would block every future
		// write, so treat an old one as abandoned.
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > s.lockStale {
				os.Remove(lockPath)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for lock %s; remove it if no other peel is running", lockPath)
		}
		time.Sleep(backoff)
		if backoff < 50*time.Millisecond {
			backoff *= 2
		}
	}
}

// uniqueID returns an ID not already present in all.
func (s *JSONStore) uniqueID(all []Comment) string {
	taken := make(map[string]bool, len(all))
	for _, c := range all {
		taken[c.ID] = true
	}
	for range 100 {
		if id := s.newID(); !taken[id] {
			return id
		}
	}
	// Deterministic fallback so a poor generator cannot loop forever.
	return fmt.Sprintf("c%d", len(all)+1)
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
