package app

import (
	"context"
	"fmt"

	"github.com/ziadalzarka/peel/internal/store"
)

const deletedFile = "deleted"

func (a *App) AddHiddenRun(ctx context.Context, s *Session, run store.HiddenRun) (store.HiddenRun, error) {
	run, err := a.prepareHiddenRun(ctx, s, run)
	if err != nil {
		return store.HiddenRun{}, err
	}

	existing, err := a.HiddenRuns(ctx, s, store.HiddenFilter{File: run.File, Author: run.Author})
	if err != nil {
		return store.HiddenRun{}, err
	}
	hidden := a.StateFor(s).Hidden
	created, err := hidden.Add(run)
	if err != nil {
		return store.HiddenRun{}, err
	}
	for _, old := range existing {
		if sameRun(old, run) {
			if err := hidden.Remove(old.ID); err != nil {
				return store.HiddenRun{}, err
			}
		}
	}
	if s.PR != nil {
		return created, nil
	}
	return created, a.KeepAnchors(ctx)
}

func (a *App) prepareHiddenRun(ctx context.Context, s *Session, run store.HiddenRun) (store.HiddenRun, error) {
	entry, ok := s.Entry(run.File)
	if !ok {
		return store.HiddenRun{}, fmt.Errorf("%s has no changes in %s, so there is nothing to hide", run.File, s.Title)
	}
	if entry.Conflicted {
		return store.HiddenRun{}, fmt.Errorf("%s is a merge conflict and has to be read, so it cannot be hidden", run.File)
	}

	run.Origin = store.OriginWorktree
	blob, err := a.freezeRun(ctx, s, run, entry.IsBinary())
	if err != nil {
		return store.HiddenRun{}, err
	}
	run.Blob = blob
	return run, nil
}

func sameRun(old, run store.HiddenRun) bool {
	if old.WholeFile() || run.WholeFile() {
		return old.WholeFile() && run.WholeFile()
	}
	return !old.Outdated && old.Line == run.Line && old.LastLine() == run.LastLine()
}

func (a *App) freezeRun(ctx context.Context, s *Session, run store.HiddenRun, binary bool) (string, error) {
	if s.PR != nil {
		if binary && !run.WholeFile() {
			return "", fmt.Errorf("%s is a binary file and has no lines to hide; leave --line off to hide the whole file", run.File)
		}
		return s.PR.HeadSHA, nil
	}
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
	if s != nil && s.PR != nil {
		runs, err := a.StateFor(s).Hidden.List(f)
		if err != nil {
			return nil, err
		}
		return outdatedByPush(s, runs), nil
	}
	if !a.HasRepo() || s == nil {
		return nil, nil
	}
	runs, err := a.Local.Hidden.List(f)
	if err != nil {
		return nil, err
	}
	return a.placeRuns(ctx, s, runs), nil
}

func outdatedByPush(s *Session, runs []store.HiddenRun) []store.HiddenRun {
	out := make([]store.HiddenRun, len(runs))
	for i, r := range runs {
		r.Outdated = r.Blob != s.PR.HeadSHA
		out[i] = r
	}
	return out
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
