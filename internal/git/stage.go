package git

import (
	"context"
	"fmt"
)

// Stager moves changes between the working tree and the index.
//
// A file is the smallest thing peel stages. Reviewing a file and then staging
// it is one decision; splitting a file across the index and the working tree is
// what `git add -p` is for, and doing it here would mean maintaining a patch
// engine whose bugs write the wrong lines into the index.
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
