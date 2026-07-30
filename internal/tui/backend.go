package tui

import (
	"context"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/store"
)

// Backend is everything the review UI needs from the rest of peel.
//
// It exists so Model can be driven by a fake in tests, and so a future frontend
// over a different source (a review server, say) only has to satisfy this.
type Backend interface {
	// Reload re-reads the session being reviewed.
	Reload(ctx context.Context) (*app.Session, error)
	// Comments returns the comments scoped to this session.
	Comments() ([]store.Comment, error)
	AddComment(c store.Comment) (store.Comment, error)
	RemoveComment(id string) error
	SetResolved(id string, resolved bool) error

	StageFile(ctx context.Context, path string) error
	UnstageFile(ctx context.Context, path string) error
	StageAll(ctx context.Context) error
	UnstageAll(ctx context.Context) error

	// OpenFile hands a file to the desktop, for reading it outside the diff.
	OpenFile(ctx context.Context, path string) error

	// Folded returns the files folded away when this review was last read.
	Folded() ([]string, error)
	// SetFolded records the files folded away now.
	SetFolded(paths []string) error

	// Walkthrough returns the AI narrative of the session.
	Walkthrough(ctx context.Context, regenerate bool) (string, error)
}

// appBackend adapts an App and a session to the Backend interface.
type appBackend struct {
	app      *app.App
	session  *app.Session
	provider string
}

// NewBackend returns the Backend the real UI runs against.
func NewBackend(a *app.App, s *app.Session, provider ...string) Backend {
	name := ""
	if len(provider) > 0 {
		name = provider[0]
	}
	return &appBackend{app: a, session: s, provider: name}
}

// Reload re-reads what is being reviewed. A pull request is not in this working
// tree, so reloading one would only re-fetch a diff that cannot have changed.
// A revision session is re-read like the working tree is: its far side is the
// working tree, so it goes on changing as the repository does.
func (b *appBackend) Reload(ctx context.Context) (*app.Session, error) {
	if b.session.PR != nil {
		return b.session, nil
	}
	s, err := b.app.LoadRevision(ctx, b.session.Base)
	if err != nil {
		return nil, err
	}
	// Target and Title identify the session; only its contents are re-read.
	s.Target = b.session.Target
	s.Title = b.session.Title
	b.session = s
	return s, nil
}

func (b *appBackend) Comments() ([]store.Comment, error) {
	return b.app.Comments.List(b.session.CommentFilter())
}

func (b *appBackend) AddComment(c store.Comment) (store.Comment, error) {
	c.Target = b.session.Target
	return b.app.Comments.Add(c)
}

func (b *appBackend) RemoveComment(id string) error {
	return b.app.Comments.Remove(id)
}

func (b *appBackend) SetResolved(id string, resolved bool) error {
	_, err := b.app.Comments.Update(id, func(c *store.Comment) { c.Resolved = resolved })
	return err
}

func (b *appBackend) StageFile(ctx context.Context, path string) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.StageFile(ctx, path)
}

func (b *appBackend) UnstageFile(ctx context.Context, path string) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.UnstageFile(ctx, path)
}

func (b *appBackend) StageAll(ctx context.Context) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.StageAll(ctx)
}

func (b *appBackend) UnstageAll(ctx context.Context) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.UnstageAll(ctx)
}

func (b *appBackend) OpenFile(ctx context.Context, path string) error {
	return b.app.OpenFile(ctx, path)
}

func (b *appBackend) Folded() ([]string, error) {
	return b.app.Folds.Load(b.session.Target)
}

func (b *appBackend) SetFolded(paths []string) error {
	return b.app.Folds.Save(b.session.Target, paths)
}

func (b *appBackend) Walkthrough(ctx context.Context, regenerate bool) (string, error) {
	got, err := b.app.Walkthrough(ctx, b.session, app.WalkthroughRequest{
		Provider:   b.provider,
		Regenerate: regenerate,
	})
	if err != nil {
		return "", err
	}
	return got.Body, nil
}

func (b *appBackend) stageable() error { return b.session.NotStageable() }
