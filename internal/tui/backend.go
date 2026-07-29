package tui

import (
	"context"
	"fmt"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
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

	Stage(ctx context.Context, sels []git.Selection) error
	Unstage(ctx context.Context, sels []git.Selection) error
	StageFile(ctx context.Context, path string) error
	UnstageFile(ctx context.Context, path string) error
	StageAll(ctx context.Context) error
	UnstageAll(ctx context.Context) error

	// Walkthrough returns the AI narrative of the session.
	Walkthrough(ctx context.Context, regenerate bool) (string, error)
}

// appBackend adapts an App and a session to the Backend interface.
type appBackend struct {
	app     *app.App
	session *app.Session
}

// NewBackend returns the Backend the real UI runs against.
func NewBackend(a *app.App, s *app.Session) Backend {
	return &appBackend{app: a, session: s}
}

// Reload re-reads the working tree. A pull request is not in this working tree,
// so reloading one would only re-fetch a diff that cannot have changed.
func (b *appBackend) Reload(ctx context.Context) (*app.Session, error) {
	if !b.session.Stageable {
		return b.session, nil
	}
	s, err := b.app.LoadWorkingTree(ctx)
	if err != nil {
		return nil, err
	}
	s.Target = b.session.Target
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

func (b *appBackend) Stage(ctx context.Context, sels []git.Selection) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.Stage(ctx, sels)
}

func (b *appBackend) Unstage(ctx context.Context, sels []git.Selection) error {
	if err := b.stageable(); err != nil {
		return err
	}
	return b.app.Stager.Unstage(ctx, sels)
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

func (b *appBackend) Walkthrough(ctx context.Context, regenerate bool) (string, error) {
	got, err := b.app.Walkthrough(ctx, b.session, app.WalkthroughRequest{Regenerate: regenerate})
	if err != nil {
		return "", err
	}
	return got.Body, nil
}

func (b *appBackend) stageable() error {
	if b.session.Stageable {
		return nil
	}
	return fmt.Errorf("%s is not in this working tree — nothing to stage", b.session.Title)
}
