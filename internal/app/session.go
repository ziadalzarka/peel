package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// Session is one thing being reviewed: either the working tree or a pull
// request.
type Session struct {
	// Target scopes comments to this review. Empty means the working tree.
	Target string
	// Title describes the session for display.
	Title string
	// Files is what changed, in path order.
	Files []git.FileEntry
	// DiffText is the raw unified diff, used to generate a walkthrough.
	DiffText string
	// Stageable reports whether staging applies. A pull request is read-only:
	// its changes are not in this working tree, so there is nothing to stage.
	Stageable bool
	// PR is set when reviewing a pull request.
	PR *forge.PullRequest
}

// IsEmpty reports whether there is nothing to review.
func (s *Session) IsEmpty() bool { return len(s.Files) == 0 }

// Entry returns the file entry for path.
func (s *Session) Entry(path string) (git.FileEntry, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return git.FileEntry{}, false
}

// Paths lists every changed path, in order.
func (s *Session) Paths() []string {
	out := make([]string, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, f.Path)
	}
	return out
}

// Stats sums additions and removals across the session.
func (s *Session) Stats() (added, removed int) {
	for _, f := range s.Files {
		a, r := f.Stats()
		added += a
		removed += r
	}
	return added, removed
}

// CommentFilter returns the filter selecting this session's comments.
func (s *Session) CommentFilter() store.Filter {
	return store.Filter{Target: s.Target, MatchTarget: true}
}

// LoadWorkingTree reads the current state of the repository.
func (a *App) LoadWorkingTree(ctx context.Context) (*Session, error) {
	status, err := a.Repo.LoadStatus(ctx)
	if err != nil {
		return nil, err
	}
	diffText, err := a.Repo.AllChangesText(ctx)
	if err != nil {
		return nil, err
	}

	return &Session{
		Target:    "",
		Title:     "working tree",
		Files:     status.Files,
		DiffText:  diffText,
		Stageable: true,
	}, nil
}

// LoadPullRequest fetches a pull request for review. providerName may be empty
// to use the first available forge.
func (a *App) LoadPullRequest(ctx context.Context, providerName, ref string) (*Session, error) {
	provider, err := a.Forges.Resolve(ctx, providerName)
	if err != nil {
		return nil, err
	}

	parsed, err := provider.Parse(ctx, a.Root, ref)
	if err != nil {
		return nil, err
	}
	pr, err := provider.Fetch(ctx, parsed)
	if err != nil {
		return nil, err
	}

	files, err := filesFromDiff(pr.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse diff for %s: %w", parsed, err)
	}

	return &Session{
		Target:    parsed.Target(provider.Name()),
		Title:     pr.Describe(),
		Files:     files,
		DiffText:  pr.Diff,
		Stageable: false,
		PR:        pr,
	}, nil
}

// filesFromDiff converts a raw diff into file entries for display.
//
// The changes land on the working-tree side because that is the side the UI
// renders; Session.Stageable is what stops anything trying to stage them.
func filesFromDiff(diff string) ([]git.FileEntry, error) {
	parsed, err := git.ParseDiff(diff)
	if err != nil {
		return nil, err
	}

	entries := make([]git.FileEntry, 0, len(parsed.Files))
	for i := range parsed.Files {
		f := parsed.Files[i]
		entries = append(entries, git.FileEntry{Path: f.Path(), Unstaged: &f})
	}
	return entries, nil
}

// WalkthroughRequest asks for a narrative of a session.
type WalkthroughRequest struct {
	// Provider names the AI provider, or is empty for the first available.
	Provider string
	// Regenerate ignores any cached narrative.
	Regenerate bool
	// Instruction optionally replaces the default prompt.
	Instruction string
}

// Walkthrough returns a narrative of the session, generating one only when the
// cache is missing or describes a different changeset.
func (a *App) Walkthrough(ctx context.Context, s *Session, req WalkthroughRequest) (store.Walkthrough, error) {
	if strings.TrimSpace(s.DiffText) == "" {
		return store.Walkthrough{}, fmt.Errorf("nothing to summarise: %s has no changes", s.Title)
	}
	fingerprint := store.Fingerprint(s.DiffText)

	if !req.Regenerate {
		cached, ok, err := a.Walkthroughs.Load()
		if err != nil {
			return store.Walkthrough{}, err
		}
		if ok && cached.Fresh(s.Target, fingerprint) {
			return cached, nil
		}
	}

	provider, err := a.AI.Resolve(ctx, req.Provider)
	if err != nil {
		return store.Walkthrough{}, err
	}

	body, err := provider.Walkthrough(ctx, aiRequest(s, req.Instruction))
	if err != nil {
		return store.Walkthrough{}, err
	}

	result := store.Walkthrough{
		Target:      s.Target,
		Fingerprint: fingerprint,
		Provider:    provider.Name(),
		Body:        body,
	}
	if err := a.Walkthroughs.Save(result); err != nil {
		return store.Walkthrough{}, err
	}
	return result, nil
}
