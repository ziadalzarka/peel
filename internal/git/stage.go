package git

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Selection addresses what to move between the working tree and the index.
//
// Lines holds indexes into the hunk's Lines slice. A nil or empty Lines means
// the whole hunk, which is the common case; a populated Lines stages only part
// of it.
type Selection struct {
	Hunk  HunkID
	Lines []int
}

// WholeHunk returns a Selection covering all of a hunk.
func WholeHunk(id HunkID) Selection { return Selection{Hunk: id} }

// Stager moves changes between the working tree and the index.
//
// Every operation rebuilds its view from git first, because hunk line offsets
// are invalidated by any change to the tree — including a previous operation of
// its own.
type Stager struct {
	repo *Repo
}

// NewStager returns a Stager backed by repo.
func NewStager(repo *Repo) *Stager { return &Stager{repo: repo} }

// Stage moves the selections from the working tree into the index.
func (s *Stager) Stage(ctx context.Context, sels []Selection) error {
	return s.apply(ctx, sels, Forward)
}

// Unstage removes the selections from the index, leaving the working tree
// untouched.
func (s *Stager) Unstage(ctx context.Context, sels []Selection) error {
	return s.apply(ctx, sels, Reverse)
}

// StageFile stages every change to path, including deletions and untracked
// files. Binary files can only be staged this way.
func (s *Stager) StageFile(ctx context.Context, path string) error {
	return s.repo.StageFile(ctx, path)
}

// UnstageFile removes every staged change to path.
func (s *Stager) UnstageFile(ctx context.Context, path string) error {
	return s.repo.UnstageFile(ctx, path)
}

// StageAll stages every change in the working tree, untracked files included.
func (s *Stager) StageAll(ctx context.Context) error {
	if _, err := s.repo.git(ctx, "add", "--all"); err != nil {
		return fmt.Errorf("stage all: %w", err)
	}
	return nil
}

// UnstageAll empties the index of everything not yet committed.
func (s *Stager) UnstageAll(ctx context.Context) error {
	if !s.repo.HasHead(ctx) {
		if _, err := s.repo.git(ctx, "rm", "-r", "--cached", "--quiet", "."); err != nil {
			return fmt.Errorf("unstage all: %w", err)
		}
		return nil
	}
	if _, err := s.repo.git(ctx, "restore", "--staged", "."); err != nil {
		return fmt.Errorf("unstage all: %w", err)
	}
	return nil
}

// apply builds a single patch covering every selection and applies it once.
//
// One patch rather than one per hunk is deliberate: applying a hunk shifts the
// offsets of every hunk after it, so a sequence of applies computed from one
// diff would write the wrong lines. A single apply is also atomic — git rejects
// the whole patch if any part of it fails.
func (s *Stager) apply(ctx context.Context, sels []Selection, dir Direction) error {
	if len(sels) == 0 {
		return nil
	}

	if dir == Forward {
		if err := s.prepareUntracked(ctx, sels); err != nil {
			return err
		}
	}

	status, err := s.repo.LoadStatus(ctx)
	if err != nil {
		return err
	}

	patch, err := buildSelectionPatch(status, sels, dir)
	if err != nil {
		return err
	}
	return s.repo.Apply(ctx, patch, dir)
}

// prepareUntracked registers untracked files with the index so their hunks can
// be addressed by `git apply --cached`, which cannot create a path the index
// has never heard of.
func (s *Stager) prepareUntracked(ctx context.Context, sels []Selection) error {
	untracked, err := s.repo.Untracked(ctx)
	if err != nil {
		return err
	}
	if len(untracked) == 0 {
		return nil
	}

	isUntracked := make(map[string]bool, len(untracked))
	for _, p := range untracked {
		isUntracked[p] = true
	}

	seen := map[string]bool{}
	for _, sel := range sels {
		path := sel.Hunk.Path
		if !isUntracked[path] || seen[path] {
			continue
		}
		seen[path] = true
		if err := s.repo.IntentToAdd(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

// buildSelectionPatch resolves every selection against the current status and
// renders one patch covering all of them.
func buildSelectionPatch(status Status, sels []Selection, dir Direction) (string, error) {
	byPath := map[string][]Selection{}
	var order []string
	for _, sel := range sels {
		if _, seen := byPath[sel.Hunk.Path]; !seen {
			order = append(order, sel.Hunk.Path)
		}
		byPath[sel.Hunk.Path] = append(byPath[sel.Hunk.Path], sel)
	}
	sort.Strings(order)

	var b strings.Builder
	for _, path := range order {
		entry, ok := status.Entry(path)
		if !ok {
			return "", fmt.Errorf("%s: no longer has changes", path)
		}
		file := entry.Diff(dir)
		if file == nil {
			return "", fmt.Errorf("%s: nothing to %s", path, verb(dir))
		}

		hunks, err := resolveHunks(*file, byPath[path], dir)
		if err != nil {
			return "", err
		}
		if len(hunks) == 0 {
			continue
		}

		filePatch, err := BuildPatch(*file, hunks, dir)
		if err != nil {
			return "", err
		}
		b.WriteString(filePatch)
	}

	if b.Len() == 0 {
		return "", fmt.Errorf("selection resolved to an empty patch")
	}
	return b.String(), nil
}

// resolveHunks matches selections to the file's current hunks, preserving file
// order so the running-delta arithmetic in BuildPatch stays correct.
func resolveHunks(file FileDiff, sels []Selection, dir Direction) ([]Hunk, error) {
	lineSel := map[HunkID][]int{}
	wanted := map[HunkID]bool{}
	for _, sel := range sels {
		wanted[sel.Hunk] = true
		if len(sel.Lines) > 0 {
			lineSel[sel.Hunk] = append(lineSel[sel.Hunk], sel.Lines...)
		}
	}

	var out []Hunk
	matched := map[HunkID]bool{}
	for _, h := range file.Hunks {
		id := file.ID(h)
		if !wanted[id] {
			continue
		}
		matched[id] = true

		lines, partial := lineSel[id]
		if !partial {
			out = append(out, h)
			continue
		}
		selected := make(map[int]bool, len(lines))
		for _, i := range lines {
			if i < 0 || i >= len(h.Lines) {
				return nil, fmt.Errorf("hunk %s: line index %d out of range", id, i)
			}
			selected[i] = true
		}
		trimmed, ok := SelectLines(h, selected, dir)
		if !ok {
			continue
		}
		out = append(out, trimmed)
	}

	for id := range wanted {
		if !matched[id] {
			return nil, fmt.Errorf("hunk %s: not found — the tree changed, reload and retry", id)
		}
	}
	return out, nil
}

func verb(dir Direction) string {
	if dir == Reverse {
		return "unstage"
	}
	return "stage"
}
