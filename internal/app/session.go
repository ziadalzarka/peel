package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// Session is one thing being reviewed: the working tree, the work since some
// commit, or a pull request.
type Session struct {
	// Target scopes comments to this review. Empty means the working tree.
	Target string
	// Title describes the session for display.
	Title string
	// Base is the commit the changes are measured from, as a resolved hash.
	// Empty means HEAD, which is the working tree session.
	Base string
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

// NotStageable explains why staging does not apply to this session, or is nil
// when it does. The UI asks before it stages anything, so a read-only session
// says so on the keypress rather than showing a stage and taking it back.
func (s *Session) NotStageable() error {
	if s.Stageable {
		return nil
	}
	return fmt.Errorf("%s is not in this working tree — nothing to stage", s.Title)
}

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

// LoadRevision reviews everything that changed since ref: the commits made
// since then and the uncommitted work on top of them. An empty ref means the
// working tree.
//
// The base is resolved once and held as a hash, so a session opened on HEAD~1
// keeps the commit it started from when another one lands underneath it.
//
// Comments are scoped to the working tree rather than to the base, because the
// side being reviewed is the working tree either way — moving the base changes
// how far back the diff reaches, not which code the notes are about.
func (a *App) LoadRevision(ctx context.Context, ref string) (*Session, error) {
	if ref == "" {
		return a.LoadWorkingTree(ctx)
	}
	base, err := a.Repo.ResolveCommit(ctx, ref)
	if err != nil {
		return nil, err
	}
	// HEAD is the working-tree session's base already, and HEAD is the only
	// base staging can mean anything against, so keep the richer session
	// rather than hand back a read-only copy of it.
	if head, err := a.Repo.ResolveCommit(ctx, "HEAD"); err == nil && head == base {
		return a.LoadWorkingTree(ctx)
	}

	status, err := a.Repo.LoadStatusSince(ctx, base)
	if err != nil {
		return nil, err
	}
	diffText, err := a.Repo.ChangesSinceText(ctx, base)
	if err != nil {
		return nil, err
	}

	return &Session{
		Target:    "",
		Title:     ref + "..working tree",
		Base:      base,
		Files:     status.Files,
		DiffText:  diffText,
		Stageable: false,
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
		providerMatches := req.Provider == "" || cached.Provider == req.Provider
		if ok && cached.Fresh(s.Target, fingerprint) && providerMatches {
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
