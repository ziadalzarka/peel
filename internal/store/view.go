package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// View is how a review was last being looked at, as opposed to how far through
// it the reviewer got: what the diff was filtered down to rather than what has
// been read. Folds are the other half, and are kept separately.
type View struct {
	// AgentCommentsHidden reports whether the agent's notes were taken out of
	// the diff, which `A` does and no write of the store's undoes.
	AgentCommentsHidden bool `json:"agentCommentsHidden"`
}

// ViewStore remembers how each review was left.
//
// A filter over the diff is a decision about what is worth looking at, and that
// decision outlives the process the way a fold does — reading a diff without an
// agent's notes today should not put them back tomorrow. It is keyed by target
// so the working tree and a pull request each keep their own.
type ViewStore interface {
	Load(target string) (View, error)
	Save(target string, v View) error
}

// viewFile is the on-disk shape, one view per review target.
type viewFile struct {
	Version int             `json:"version"`
	Views   map[string]View `json:"views"`
}

// JSONViewStore persists views to a single JSON file.
type JSONViewStore struct{ path string }

// NewJSONViewStore returns a store backed by the file at path.
func NewJSONViewStore(path string) *JSONViewStore { return &JSONViewStore{path: path} }

// Path returns the file the store reads and writes.
func (s *JSONViewStore) Path() string { return s.path }

// Load returns how the given target was last looked at, or the default view
// when it has never been looked at.
func (s *JSONViewStore) Load(target string) (View, error) {
	f, err := s.read()
	if err != nil {
		return View{}, err
	}
	return f.Views[target], nil
}

// Save replaces the view of one target, leaving every other target alone. A
// target back at the default is dropped rather than written out, so a file of
// filters nobody set does not accumulate.
func (s *JSONViewStore) Save(target string, v View) error {
	f, err := s.read()
	if err != nil {
		return err
	}
	if v == (View{}) {
		delete(f.Views, target)
	} else {
		f.Views[target] = v
	}
	f.Version = currentVersion
	return writeJSONFile(s.path, "view", f)
}

// read loads the file, treating a missing or unreadable one as the default
// view: how a diff was being looked at is not worth failing a review over.
func (s *JSONViewStore) read() (viewFile, error) {
	empty := viewFile{Version: currentVersion, Views: map[string]View{}}

	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("read %s: %w", s.path, err)
	}

	var f viewFile
	if err := json.Unmarshal(b, &f); err != nil {
		return empty, nil
	}
	if f.Views == nil {
		f.Views = map[string]View{}
	}
	return f, nil
}
