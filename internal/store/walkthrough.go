package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Walkthrough is a cached AI narrative of a changeset.
type Walkthrough struct {
	// Target is what was summarised: empty for the working tree, or a pull
	// request reference.
	Target string `json:"target"`
	// Fingerprint identifies the diff this narrative describes. A changed
	// fingerprint means the cache is stale.
	Fingerprint string `json:"fingerprint"`
	// Provider records which AI provider produced it.
	Provider  string    `json:"provider"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// Fresh reports whether the cached narrative still describes the given diff.
func (w Walkthrough) Fresh(target, fingerprint string) bool {
	return w.Body != "" && w.Target == target && w.Fingerprint == fingerprint
}

// WalkthroughCache stores the most recent narrative.
//
// Only one narrative is kept: it is a cache, not a history, and regenerating
// is cheap enough that eviction policy would be more machinery than value.
type WalkthroughCache interface {
	Load() (Walkthrough, bool, error)
	Save(w Walkthrough) error
	Clear() error
}

// JSONWalkthroughCache stores the narrative as JSON on disk.
type JSONWalkthroughCache struct {
	path string
	now  func() time.Time
}

// NewJSONWalkthroughCache returns a cache backed by the file at path.
func NewJSONWalkthroughCache(path string, opts ...WalkthroughOption) *JSONWalkthroughCache {
	c := &JSONWalkthroughCache{path: path, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WalkthroughOption configures a JSONWalkthroughCache.
type WalkthroughOption func(*JSONWalkthroughCache)

// WithWalkthroughClock overrides the timestamp source, for tests.
func WithWalkthroughClock(fn func() time.Time) WalkthroughOption {
	return func(c *JSONWalkthroughCache) { c.now = fn }
}

// Path returns the file the cache reads and writes.
func (c *JSONWalkthroughCache) Path() string { return c.path }

// Load returns the cached narrative. ok is false when nothing is cached.
func (c *JSONWalkthroughCache) Load() (Walkthrough, bool, error) {
	b, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Walkthrough{}, false, nil
	}
	if err != nil {
		return Walkthrough{}, false, fmt.Errorf("read %s: %w", c.path, err)
	}

	var w Walkthrough
	if err := json.Unmarshal(b, &w); err != nil {
		// A corrupt cache is not worth failing over — regenerate instead.
		return Walkthrough{}, false, nil
	}
	return w, w.Body != "", nil
}

// Save replaces the cached narrative.
func (c *JSONWalkthroughCache) Save(w Walkthrough) error {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = c.now().UTC()
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("encode walkthrough: %w", err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".walkthrough-*.json")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("replace %s: %w", c.path, err)
	}
	return nil
}

// Clear discards the cached narrative.
func (c *JSONWalkthroughCache) Clear() error {
	err := os.Remove(c.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", c.path, err)
	}
	return nil
}

// Fingerprint hashes diff text so a cached narrative can be invalidated when
// the changeset moves on.
func Fingerprint(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:8])
}
