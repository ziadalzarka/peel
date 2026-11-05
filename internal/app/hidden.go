package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ziadalzarka/peel/internal/store"
)

const deletedFile = "deleted"

func (a *App) AddHiddenRun(ctx context.Context, s *Session, run store.HiddenRun) (store.HiddenRun, error) {
	if s.PR != nil {
		return store.HiddenRun{}, errors.New("peel fold only works on the working tree, not on a pull request")
	}
	entry, ok := s.Entry(run.File)
	if !ok {
		return store.HiddenRun{}, fmt.Errorf("%s has no changes in %s, so there is nothing to hide", run.File, s.Title)
	}
	if entry.Conflicted {
		return store.HiddenRun{}, fmt.Errorf("%s is a merge conflict and has to be read, so it cannot be hidden", run.File)
	}

	run.Origin = store.OriginWorktree
	blob, err := a.freezeRun(ctx, run, entry.IsBinary())
	if err != nil {
		return store.HiddenRun{}, err
	}
	run.Blob = blob

	existing, err := a.HiddenRuns(ctx, s, store.HiddenFilter{File: run.File, Author: run.Author})
	if err != nil {
		return store.HiddenRun{}, err
	}
	created, err := a.Local.Hidden.Add(run)
	if err != nil {
		return store.HiddenRun{}, err
	}
	for _, old := range existing {
		if sameRun(old, run) {
			if err := a.Local.Hidden.Remove(old.ID); err != nil {
				return store.HiddenRun{}, err
			}
		}
	}
	return created, a.KeepAnchors(ctx)
}

func sameRun(old, run store.HiddenRun) bool {
	if old.WholeFile() || run.WholeFile() {
		return old.WholeFile() && run.WholeFile()
	}
	return !old.Outdated && old.Line == run.Line && old.LastLine() == run.LastLine()
}

func (a *App) freezeRun(ctx context.Context, run store.HiddenRun, binary bool) (string, error) {
	if run.WholeFile() {
		hashes, err := a.Repo.HashFiles(ctx, []string{run.File})
		if err != nil {
			return "", err
		}
		if hash, ok := hashes[run.File]; ok {
			return hash, nil
		}
		return deletedFile, nil
	}
	if binary {
		return "", fmt.Errorf("%s is a binary file and has no lines to hide; leave --line off to hide the whole file", run.File)
	}
	lines, err := a.Repo.WorkingLines(run.File)
	if err != nil {
		return "", fmt.Errorf("%s is not on disk, so it has no lines to hide; leave --line off to hide the whole file", run.File)
	}
	if last := run.LastLine(); last > len(lines) {
		return "", fmt.Errorf("%s has %d lines, so line %d is past its end", run.File, len(lines), last)
	}
	return a.Repo.SnapshotFile(ctx, run.File)
}

func (a *App) HiddenRuns(ctx context.Context, s *Session, f store.HiddenFilter) ([]store.HiddenRun, error) {
	if !a.HasRepo() || s == nil || s.PR != nil {
		return nil, nil
	}
	runs, err := a.Local.Hidden.List(f)
	if err != nil {
		return nil, err
	}
	return a.placeRuns(ctx, s, runs), nil
}

func (a *App) placeRuns(ctx context.Context, s *Session, runs []store.HiddenRun) []store.HiddenRun {
	if len(runs) == 0 {
		return runs
	}
	out := make([]store.HiddenRun, len(runs))
	copy(out, runs)

	var paths []string
	var lineRuns []int
	var notes []store.Comment
	for i, r := range out {
		switch {
		case r.WholeFile():
			paths = append(paths, r.File)
		default:
			lineRuns = append(lineRuns, i)
			notes = append(notes, store.Comment{File: r.File, Line: r.Line, EndLine: r.EndLine,
				Side: store.SideNew, Origin: r.Origin, Blob: r.Blob})
		}
	}

	placed, known := a.relocate(ctx, s, notes)
	for at, i := range lineRuns {
		r := &out[i]
		if _, inReview := s.Entry(r.File); !inReview {
			continue
		}
		if !known[at] || placed[at].Outdated {
			r.Outdated = true
			continue
		}
		c := placed[at]
		r.Origin, r.MovedFrom = c.Origin, c.MovedFrom
		r.Line, r.EndLine = c.Line, c.EndLine
	}

	hashes, err := a.Repo.HashFiles(ctx, paths)
	for i := range out {
		r := &out[i]
		if !r.WholeFile() {
			continue
		}
		if _, inReview := s.Entry(r.File); !inReview {
			continue
		}
		now, onDisk := hashes[r.File]
		if !onDisk {
			now = deletedFile
		}
		r.Outdated = err != nil || now != r.Blob
	}
	return out
}
