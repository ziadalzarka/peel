package store

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

var ErrRunNotFound = errors.New("fold not found")

type HiddenRun struct {
	ID        string    `json:"id"`
	File      string    `json:"file"`
	Line      int       `json:"line,omitempty"`
	EndLine   int       `json:"endLine,omitempty"`
	Origin    Origin    `json:"origin,omitempty"`
	Label     string    `json:"label,omitempty"`
	Blob      string    `json:"blob,omitempty"`
	Author    Author    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	MovedFrom int       `json:"-"`
	Outdated  bool      `json:"-"`
}

func (r HiddenRun) WholeFile() bool { return r.Line == 0 }

func (r HiddenRun) LastLine() int { return max(r.Line, r.EndLine) }

func (r HiddenRun) Location() string {
	switch {
	case r.WholeFile():
		return r.File
	case r.EndLine > r.Line:
		return fmt.Sprintf("%s:%d-%d", r.File, r.Line, r.EndLine)
	default:
		return fmt.Sprintf("%s:%d", r.File, r.Line)
	}
}

func (r HiddenRun) Validate() error {
	if strings.TrimSpace(r.File) == "" {
		return errors.New("fold: file is required")
	}
	if r.Line < 0 {
		return fmt.Errorf("fold: line %d is negative", r.Line)
	}
	if r.EndLine != 0 && r.Line == 0 {
		return fmt.Errorf("fold: end line %d with no line to run from", r.EndLine)
	}
	if r.EndLine != 0 && r.EndLine < r.Line {
		return fmt.Errorf("fold: end line %d is before line %d", r.EndLine, r.Line)
	}
	if !r.Author.Valid() {
		return fmt.Errorf("fold: author %q is not a name", r.Author)
	}
	return nil
}

type HiddenFilter struct {
	File   string
	Author Author
}

func (f HiddenFilter) matches(r HiddenRun) bool {
	return (f.File == "" || r.File == f.File) && (f.Author == "" || r.Author == f.Author)
}

type HiddenRunStore interface {
	List(f HiddenFilter) ([]HiddenRun, error)
	Add(r HiddenRun) (HiddenRun, error)
	Remove(id string) error
	Clear(f HiddenFilter) (int, error)
	Replace(f HiddenFilter, with []HiddenRun) (added, removed int, err error)
}

type hiddenFile struct {
	Version int         `json:"version"`
	Runs    []HiddenRun `json:"runs"`
}

type JSONHiddenRunStore struct {
	path string
	params
}

func NewJSONHiddenRunStore(path string, opts ...Option) *JSONHiddenRunStore {
	return &JSONHiddenRunStore{path: path, params: newParams(opts)}
}

func (s *JSONHiddenRunStore) List(f HiddenFilter) ([]HiddenRun, error) {
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	return filterRuns(all, f), nil
}

func (s *JSONHiddenRunStore) Add(r HiddenRun) (HiddenRun, error) {
	r, err := prepareRun(r, s.params)
	if err != nil {
		return HiddenRun{}, err
	}
	err = s.mutate(func(all []HiddenRun) ([]HiddenRun, error) {
		r.ID = uniqueRunID(all, s.newID)
		return append(all, r), nil
	})
	if err != nil {
		return HiddenRun{}, err
	}
	return r, nil
}

func (s *JSONHiddenRunStore) Remove(id string) error {
	return s.mutate(func(all []HiddenRun) ([]HiddenRun, error) {
		return removeRun(all, id)
	})
}

func (s *JSONHiddenRunStore) Clear(f HiddenFilter) (int, error) {
	removed := 0
	err := s.mutate(func(all []HiddenRun) ([]HiddenRun, error) {
		kept := slices.DeleteFunc(all, f.matches)
		removed = len(all) - len(kept)
		return kept, nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}
func (s *JSONHiddenRunStore) Replace(f HiddenFilter, with []HiddenRun) (int, int, error) {
	added, removed := 0, 0
	err := s.mutate(func(all []HiddenRun) ([]HiddenRun, error) {
		next, a, r, err := replaceRuns(all, f, with, s.params)
		if err != nil {
			return nil, err
		}
		added, removed = a, r
		return next, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return added, removed, nil
}

func (s *JSONHiddenRunStore) read() ([]HiddenRun, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(b) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var f hiddenFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if f.Version > currentVersion {
		return nil, fmt.Errorf("%s: written by a newer peel (format version %d)", s.path, f.Version)
	}
	return f.Runs, nil
}

func (s *JSONHiddenRunStore) mutate(apply func([]HiddenRun) ([]HiddenRun, error)) error {
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
	if next == nil {
		next = []HiddenRun{}
	}
	sortRuns(next)
	return writeJSONFile(s.path, "hidden", hiddenFile{Version: currentVersion, Runs: next})
}

func prepareRun(r HiddenRun, p params) (HiddenRun, error) {
	if r.Author == "" {
		r.Author = AuthorUnknown
	}
	if err := r.Validate(); err != nil {
		return HiddenRun{}, err
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = p.now().UTC()
	}
	return r, nil
}

func filterRuns(all []HiddenRun, f HiddenFilter) []HiddenRun {
	out := make([]HiddenRun, 0, len(all))
	for _, r := range all {
		if f.matches(r) {
			out = append(out, r)
		}
	}
	return out
}
func replaceRuns(all []HiddenRun, f HiddenFilter, with []HiddenRun, p params) ([]HiddenRun, int, int, error) {
	next := make([]HiddenRun, 0, len(all)+len(with))
	var replaced []HiddenRun
	for _, r := range all {
		if f.matches(r) {
			replaced = append(replaced, r)
			continue
		}
		next = append(next, r)
	}
	var added []HiddenRun
	for _, r := range with {
		prepared, err := prepareRun(r, p)
		if err != nil {
			return nil, 0, 0, err
		}
		if at := slices.IndexFunc(replaced, func(old HiddenRun) bool { return sameFold(old, prepared) }); at >= 0 {
			next = append(next, replaced[at])
			replaced = slices.Delete(replaced, at, at+1)
			continue
		}
		prepared.ID = uniqueRunID(slices.Concat(all, added), p.newID)
		added = append(added, prepared)
	}
	return append(next, added...), len(added), len(replaced), nil
}

func sameFold(a, b HiddenRun) bool {
	a.ID, a.CreatedAt = "", time.Time{}
	b.ID, b.CreatedAt = "", time.Time{}
	return a == b
}

func removeRun(all []HiddenRun, id string) ([]HiddenRun, error) {
	at := slices.IndexFunc(all, func(r HiddenRun) bool { return r.ID == id })
	if at < 0 {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	return slices.Delete(all, at, at+1), nil
}

func sortRuns(runs []HiddenRun) {
	slices.SortStableFunc(runs, func(a, b HiddenRun) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), a.CreatedAt.Compare(b.CreatedAt))
	})
}

func uniqueRunID(all []HiddenRun, newID func() string) string {
	for range 100 {
		id := newID()
		if !slices.ContainsFunc(all, func(r HiddenRun) bool { return r.ID == id }) {
			return id
		}
	}
	return fmt.Sprintf("r%d", len(all)+1)
}
