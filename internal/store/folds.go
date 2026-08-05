package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// FoldStore remembers which files a reviewer has folded away.
//
// Folding is how a pass through a diff records what has been read, and a pass
// rarely finishes in one sitting — so it outlives the process, the way comments
// do. It is keyed by target so the working tree and a pull request each keep
// their own.
type FoldStore interface {
	Load(target string) ([]string, error)
	Save(target string, files []string) error
}

// foldFile is the on-disk shape, a list of folded paths per review target.
type foldFile struct {
	Version int                 `json:"version"`
	Folded  map[string][]string `json:"folded"`
}

// JSONFoldStore persists folds to a single JSON file.
type JSONFoldStore struct{ path string }

// NewJSONFoldStore returns a store backed by the file at path.
func NewJSONFoldStore(path string) *JSONFoldStore { return &JSONFoldStore{path: path} }

// Path returns the file the store reads and writes.
func (s *JSONFoldStore) Path() string { return s.path }

// Load returns the paths folded away in the given target, in path order.
func (s *JSONFoldStore) Load(target string) ([]string, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	return f.Folded[target], nil
}

// Save replaces the folds of one target, leaving every other target alone.
func (s *JSONFoldStore) Save(target string, files []string) error {
	f, err := s.read()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		delete(f.Folded, target)
	} else {
		sorted := append([]string(nil), files...)
		sort.Strings(sorted)
		f.Folded[target] = sorted
	}
	f.Version = currentVersion
	return writeJSONFile(s.path, "folds", f)
}

// read loads the file, treating a missing or unreadable one as no folds: a
// review is not worth failing over what is only a record of what has been read.
func (s *JSONFoldStore) read() (foldFile, error) {
	empty := foldFile{Version: currentVersion, Folded: map[string][]string{}}

	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("read %s: %w", s.path, err)
	}

	var f foldFile
	if err := json.Unmarshal(b, &f); err != nil {
		return empty, nil
	}
	if f.Folded == nil {
		f.Folded = map[string][]string{}
	}
	return f, nil
}
