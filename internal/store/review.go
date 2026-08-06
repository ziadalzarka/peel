package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// ReviewStore is one review kept in one file: the notes written on it, the
// files folded away, how it was last being looked at, and the narrative
// generated for it.
//
// A pull request does not belong to any one checkout, so its review is not kept
// inside one. The file is named after the pull request and lives under the
// user's own state directory, which is what makes a review portable: #412 read
// from a second worktree, from another clone, or from a directory that is not a
// repository at all is the same pass, with the same notes still on it.
//
// The repository's own review — the working tree — stays in .git/peel, since
// that one is a property of the checkout and means nothing anywhere else.
type ReviewStore struct {
	path string
	// target is the review this file holds, e.g. "github:cli/cli#412". Notes
	// written here are stamped with it, so a comment read out of this file says
	// what it is about the way one out of the repository's store does.
	target string
	params
}

// NewReviewStore returns the store for one review, backed by the file at path.
// The file and the directories above it are created on first write.
func NewReviewStore(path, target string, opts ...Option) *ReviewStore {
	return &ReviewStore{path: path, target: target, params: newParams(opts)}
}

// Path returns the file the store reads and writes.
func (s *ReviewStore) Path() string { return s.path }

// Target returns the review the file holds.
func (s *ReviewStore) Target() string { return s.target }

// Comments, Folds, Views and Walkthroughs are the four faces of the same file,
// one per thing the rest of peel asks a store for.

func (s *ReviewStore) Comments() CommentStore         { return reviewComments{s} }
func (s *ReviewStore) Folds() FoldStore               { return reviewFolds{s} }
func (s *ReviewStore) Views() ViewStore               { return reviewViews{s} }
func (s *ReviewStore) Walkthroughs() WalkthroughCache { return reviewWalkthroughs{s} }

// reviewFile is the on-disk shape: everything one review is, under a version so
// the format can change later without silently misreading an older file.
type reviewFile struct {
	Version     int          `json:"version"`
	Target      string       `json:"target,omitempty"`
	Comments    []Comment    `json:"comments,omitempty"`
	Folded      []string     `json:"folded,omitempty"`
	View        View         `json:"view"`
	Walkthrough *Walkthrough `json:"walkthrough,omitempty"`
}

// read loads the file, treating a missing one as a review nobody has read yet.
//
// A file that will not parse is an error rather than an empty review: it holds
// notes somebody wrote, and quietly opening it as blank would invite writing
// over them.
func (s *ReviewStore) read() (reviewFile, error) {
	empty := reviewFile{Version: currentVersion, Target: s.target}

	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("read %s: %w", s.path, err)
	}
	if len(b) == 0 {
		return empty, nil
	}

	var f reviewFile
	if err := json.Unmarshal(b, &f); err != nil {
		return empty, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if f.Version > currentVersion {
		return empty, fmt.Errorf("%s: written by a newer peel (format version %d)", s.path, f.Version)
	}
	return f, nil
}

// mutate runs apply against the freshly-read file and writes the result back,
// holding a lock for the whole read-modify-write — the TUI and an agent in
// another terminal can both be writing this review at once.
func (s *ReviewStore) mutate(apply func(*reviewFile) error) error {
	release, err := lockFile(s.path, s.lockTimeout, s.lockStale)
	if err != nil {
		return err
	}
	defer release()

	f, err := s.read()
	if err != nil {
		return err
	}
	if err := apply(&f); err != nil {
		return err
	}
	f.Version, f.Target = currentVersion, s.target
	sortComments(f.Comments)
	return writeJSONFile(s.path, "review", f)
}

// reviewComments is the file's comments, as a CommentStore.
type reviewComments struct{ s *ReviewStore }

func (r reviewComments) List(f Filter) ([]Comment, error) {
	file, err := r.s.read()
	if err != nil {
		return nil, err
	}
	return filterComments(file.Comments, f), nil
}

// Add stamps the note with the review the file holds, so a comment read back
// out of it carries the same target as one stored in the repository would.
func (r reviewComments) Add(c Comment) (Comment, error) {
	c.Target = r.s.target
	prepared, err := prepareComment(c, r.s.params)
	if err != nil {
		return Comment{}, err
	}

	added := prepared
	err = r.s.mutate(func(f *reviewFile) error {
		next, got, err := addComment(f.Comments, prepared, r.s.params)
		if err != nil {
			return err
		}
		f.Comments, added = next, got
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return added, nil
}

func (r reviewComments) Update(id string, apply func(*Comment)) (Comment, error) {
	var updated Comment
	err := r.s.mutate(func(f *reviewFile) error {
		next, got, err := updateComment(f.Comments, id, apply)
		if err != nil {
			return err
		}
		f.Comments, updated = next, got
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	return updated, nil
}

func (r reviewComments) Remove(id string) error {
	return r.s.mutate(func(f *reviewFile) error {
		next, err := removeComment(f.Comments, id)
		if err != nil {
			return err
		}
		f.Comments = next
		return nil
	})
}

func (r reviewComments) Clear(f Filter) (int, error) {
	removed := 0
	err := r.s.mutate(func(file *reviewFile) error {
		file.Comments, removed = clearComments(file.Comments, f)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// reviewFolds is the file's folds. The file is one review, so the target names
// which review is being asked about rather than selecting within it — asking
// for another one's folds here would be asking the wrong file.

type reviewFolds struct{ s *ReviewStore }

func (r reviewFolds) Load(string) ([]string, error) {
	f, err := r.s.read()
	if err != nil {
		return nil, err
	}
	return f.Folded, nil
}

func (r reviewFolds) Save(_ string, files []string) error {
	return r.s.mutate(func(f *reviewFile) error {
		f.Folded = sortedCopy(files)
		return nil
	})
}

// reviewViews is the file's view, keyed the same way its folds are.
type reviewViews struct{ s *ReviewStore }

func (r reviewViews) Load(string) (View, error) {
	f, err := r.s.read()
	if err != nil {
		return View{}, err
	}
	return f.View, nil
}

func (r reviewViews) Save(_ string, v View) error {
	return r.s.mutate(func(f *reviewFile) error {
		f.View = v
		return nil
	})
}

// reviewWalkthroughs is the narrative cached for this review. Only one is kept,
// as in the repository's cache, but it is kept per review rather than per
// checkout — so reopening the pull request somewhere else does not pay a
// provider for a narrative that has already been written.
type reviewWalkthroughs struct{ s *ReviewStore }

func (r reviewWalkthroughs) Load() (Walkthrough, bool, error) {
	f, err := r.s.read()
	if err != nil {
		return Walkthrough{}, false, err
	}
	if f.Walkthrough == nil {
		return Walkthrough{}, false, nil
	}
	return *f.Walkthrough, f.Walkthrough.Body != "", nil
}

func (r reviewWalkthroughs) Save(w Walkthrough) error {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = r.s.now().UTC()
	}
	return r.s.mutate(func(f *reviewFile) error {
		f.Walkthrough = &w
		return nil
	})
}

func (r reviewWalkthroughs) Clear() error {
	return r.s.mutate(func(f *reviewFile) error {
		f.Walkthrough = nil
		return nil
	})
}

// sortedCopy is the paths in path order, leaving the caller's slice alone.
func sortedCopy(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	out := append([]string(nil), files...)
	sort.Strings(out)
	return out
}
