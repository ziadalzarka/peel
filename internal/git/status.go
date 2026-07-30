package git

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
)

// emptyTree is git's fixed hash for a tree with no entries. Diffing the index
// against it stands in for `git diff --cached` when HEAD does not exist yet.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// StageState summarises where a file's changes currently live.
type StageState int

const (
	// StateUnstaged has changes only in the working tree.
	StateUnstaged StageState = iota
	// StatePartial has changes in both the index and the working tree. A file
	// can be in both at once, which is why the file list needs three states
	// rather than a checkbox.
	StatePartial
	// StateStaged has changes only in the index.
	StateStaged
)

// Symbol returns the single-character indicator for the state.
func (s StageState) Symbol() string {
	switch s {
	case StateStaged:
		return "✓"
	case StatePartial:
		return "●"
	default:
		return " "
	}
}

func (s StageState) String() string {
	switch s {
	case StateStaged:
		return "staged"
	case StatePartial:
		return "partial"
	default:
		return "unstaged"
	}
}

// FileEntry is one file's complete change state across the index and the
// working tree.
type FileEntry struct {
	Path string
	// Unstaged holds working-tree changes not yet in the index, or nil.
	Unstaged *FileDiff
	// Staged holds index changes not yet committed, or nil.
	Staged *FileDiff
	// Untracked marks a file git does not track. Its Unstaged diff is
	// synthesized for display and does not come from the index.
	Untracked bool
}

// State classifies the entry for display.
func (e FileEntry) State() StageState {
	switch {
	case e.Staged != nil && e.Unstaged != nil:
		return StatePartial
	case e.Staged != nil:
		return StateStaged
	default:
		return StateUnstaged
	}
}

// IsBinary reports whether either side is binary, meaning there is no diff to
// show — only the fact that the file changed.
func (e FileEntry) IsBinary() bool {
	return (e.Unstaged != nil && e.Unstaged.IsBinary) || (e.Staged != nil && e.Staged.IsBinary)
}

// Stats sums additions and removals across both sides.
func (e FileEntry) Stats() (added, removed int) {
	for _, d := range []*FileDiff{e.Staged, e.Unstaged} {
		if d != nil {
			a, r := d.Stats()
			added += a
			removed += r
		}
	}
	return added, removed
}

// Primary returns the diff to show by default — the working-tree side when
// there is one, since that is what the reviewer is deciding about.
func (e FileEntry) Primary() *FileDiff {
	if e.Unstaged != nil {
		return e.Unstaged
	}
	return e.Staged
}

// Status is the full working-tree state peel reviews.
type Status struct {
	Files []FileEntry
}

// Entry returns the entry for path.
func (s Status) Entry(path string) (FileEntry, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return FileEntry{}, false
}

// IsEmpty reports whether there is nothing to review.
func (s Status) IsEmpty() bool { return len(s.Files) == 0 }

// entrySet collects per-file state as the sides of a status are read.
type entrySet map[string]*FileEntry

// at returns the entry for path, creating it on first mention.
func (s entrySet) at(path string) *FileEntry {
	if e, ok := s[path]; ok {
		return e
	}
	e := &FileEntry{Path: path}
	s[path] = e
	return e
}

// status returns the collected entries in path order.
func (s entrySet) status() Status {
	paths := make([]string, 0, len(s))
	for path := range s {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	out := Status{Files: make([]FileEntry, 0, len(paths))}
	for _, path := range paths {
		out.Files = append(out.Files, *s[path])
	}
	return out
}

// LoadStatus reads the working tree and index and merges them into one
// per-file view.
func (r *Repo) LoadStatus(ctx context.Context) (Status, error) {
	unstaged, err := r.Unstaged(ctx)
	if err != nil {
		return Status{}, err
	}
	staged, err := r.stagedAgainstBase(ctx)
	if err != nil {
		return Status{}, err
	}

	entries := entrySet{}
	for i := range unstaged.Files {
		f := unstaged.Files[i]
		entries.at(f.Path()).Unstaged = &f
	}
	for i := range staged.Files {
		f := staged.Files[i]
		entries.at(f.Path()).Staged = &f
	}
	if err := r.addUntracked(ctx, entries); err != nil {
		return Status{}, err
	}
	return entries.status(), nil
}

// LoadStatusSince reads everything that changed between a commit and the
// working tree.
//
// The index split LoadStatus reports does not apply here: a change committed
// since the base is neither staged nor unstaged, it is simply part of the diff.
// Every file therefore carries one side, and Session.Stageable is what stops
// the UI offering to stage what it cannot.
func (r *Repo) LoadStatusSince(ctx context.Context, commit string) (Status, error) {
	diff, err := r.ChangesSince(ctx, commit)
	if err != nil {
		return Status{}, err
	}

	entries := entrySet{}
	for i := range diff.Files {
		f := diff.Files[i]
		entries.at(f.Path()).Unstaged = &f
	}
	if err := r.addUntracked(ctx, entries); err != nil {
		return Status{}, err
	}
	return entries.status(), nil
}

// addUntracked marks the untracked paths and synthesizes a diff for any that
// has none yet, so a new file is reviewable before the index is touched.
func (r *Repo) addUntracked(ctx context.Context, entries entrySet) error {
	untracked, err := r.Untracked(ctx)
	if err != nil {
		return err
	}

	// An intent-to-add file appears in both `ls-files --others` and the index,
	// so only synthesize a diff for paths with no real diff yet.
	var needsSynthetic []string
	for _, path := range untracked {
		e := entries.at(path)
		e.Untracked = true
		if e.Unstaged == nil {
			needsSynthetic = append(needsSynthetic, path)
		}
	}
	for path, diff := range r.untrackedDiffs(ctx, needsSynthetic) {
		entries.at(path).Unstaged = diff
	}
	return nil
}

// stagedAgainstBase reads the index diff, falling back to the empty tree when
// the repository has no commits yet.
func (r *Repo) stagedAgainstBase(ctx context.Context) (Diff, error) {
	if r.HasHead(ctx) {
		return r.Staged(ctx)
	}
	args := append([]string{"diff", "--cached"}, diffFlags...)
	args = append(args, emptyTree)
	out, err := r.git(ctx, args...)
	if err != nil {
		return Diff{}, fmt.Errorf("git diff --cached (unborn HEAD): %w", err)
	}
	return ParseDiff(out)
}

// untrackedDiffs synthesizes diffs for untracked paths concurrently, since each
// one costs a subprocess. Files that fail to diff are skipped rather than
// failing the whole status load — an unreadable file should not block review of
// everything else.
func (r *Repo) untrackedDiffs(ctx context.Context, paths []string) map[string]*FileDiff {
	out := make(map[string]*FileDiff, len(paths))
	if len(paths) == 0 {
		return out
	}

	workers := min(runtime.NumCPU(), len(paths))
	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for path := range jobs {
				diff, err := r.UntrackedDiff(ctx, path)
				if err != nil {
					continue
				}
				mu.Lock()
				out[path] = &diff
				mu.Unlock()
			}
		}()
	}
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	return out
}
