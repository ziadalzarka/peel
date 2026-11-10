package app

import (
	"context"

	"github.com/ziadalzarka/peel/internal/store"
)

type Notes struct {
	Comments []store.Comment
	Folds    []store.HiddenRun
}

type Replaced struct {
	Comments Change
	Folds    Change
	Skipped  []Skipped
}

type Change struct {
	Kept    int
	Added   int
	Removed int
}

type Skipped struct {
	Location string
	Err      error
}

func (a *App) ReplaceNotes(ctx context.Context, s *Session, author store.Author, notes Notes) (Replaced, error) {
	var out Replaced
	comments := make([]store.Comment, 0, len(notes.Comments))
	for _, c := range notes.Comments {
		c.Author, c.Target = author, s.Target
		if err := c.Validate(); err != nil {
			out.Skipped = append(out.Skipped, Skipped{Location: c.Location(), Err: err})
			continue
		}
		if blob, err := a.Snapshot(ctx, s, c); err == nil {
			c.Blob = blob
		}
		comments = append(comments, c)
	}

	folds := make([]store.HiddenRun, 0, len(notes.Folds))
	for _, r := range notes.Folds {
		r.Author = author
		prepared, err := r, r.Validate()
		if err == nil {
			prepared, err = a.prepareHiddenRun(ctx, s, r)
		}
		if err != nil {
			out.Skipped = append(out.Skipped, Skipped{Location: r.Location(), Err: err})
			continue
		}
		folds = append(folds, prepared)
	}

	state := a.StateFor(s)
	filter := s.CommentFilter()
	filter.Author = author
	added, removed, err := state.Comments.Replace(filter, comments)
	if err != nil {
		return Replaced{}, err
	}
	out.Comments = Change{Kept: len(comments) - added, Added: added, Removed: removed}

	added, removed, err = state.Hidden.Replace(store.HiddenFilter{Author: author}, folds)
	if err != nil {
		return Replaced{}, err
	}
	out.Folds = Change{Kept: len(folds) - added, Added: added, Removed: removed}

	if s.PR != nil {
		return out, nil
	}
	return out, a.KeepAnchors(ctx)
}
