package git

import (
	"context"
	"fmt"
)

// Stager moves changes between the working tree and the index.
//
// A file is what most of it moves, through `git add` and `git restore
// --staged`: those handle a deletion, a binary file, an untracked file and CRLF
// because that is their job, and nothing peel writes can get them wrong.
//
// StageHunk is the one operation that generates a patch, and the one that can:
// a hunk, forwards, into the index, from a diff whose old side is the index
// already. Splitting a hunk finer than that is what `git add -p` is for.
type Stager struct {
	repo *Repo
}

// NewStager returns a Stager backed by repo.
func NewStager(repo *Repo) *Stager { return &Stager{repo: repo} }

// StageFile stages every change to path, including deletions and untracked
// files.
func (s *Stager) StageFile(ctx context.Context, path string) error {
	return s.repo.StageFile(ctx, path)
}

// StageHunk stages one hunk of what a file has out of the index, leaving the
// rest of that file where it is.
//
// The hunk is named by ID and looked up here rather than handed over as a diff
// the caller is holding: an ID is line offsets, so anything that has touched the
// file since it was read leaves it naming a hunk that is no longer there. Not
// finding it is worth saying in words — the alternative is `git apply` failing
// against a file it cannot match, which reads like peel wrote a bad patch.
//
// An untracked file is refused rather than escalated: `git apply --cached`
// cannot write a path the index has never heard of, and the `git add -N` that
// would make it addressable is a change to the index in its own right, made
// behind a keypress that said nothing about it.
//
// What it reads is the one file, not the tree: a keypress is answered by
// `git diff -- <path>` and the apply, and the whole status the screen is redrawn
// from is read once, behind it, by the reload every change ends in.
func (s *Stager) StageHunk(ctx context.Context, id HunkID) error {
	file, ok, err := s.repo.UnstagedFile(ctx, id.Path)
	if err != nil {
		return err
	}
	if !ok {
		if s.repo.IsUntracked(ctx, id.Path) {
			return fmt.Errorf("%s is untracked — the index has never heard of it, so it goes in whole", id.Path)
		}
		return fmt.Errorf("%s has nothing out of the index — reload and try again", id.Path)
	}

	for _, h := range file.Hunks {
		if !id.Matches(file, h) {
			continue
		}
		patch, err := Patch(file, h)
		if err != nil {
			return err
		}
		return s.repo.ApplyToIndex(ctx, patch)
	}
	return fmt.Errorf("hunk %s has moved since it was read — reload and try again", id)
}

// UnstageFile removes every staged change to path, leaving the working tree
// untouched.
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
