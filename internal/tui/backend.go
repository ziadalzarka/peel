package tui

import (
	"context"

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
	// Comments returns the comments scoped to this session, each moved on to the
	// line its code sits on now.
	Comments(ctx context.Context) ([]store.Comment, error)
	AddComment(ctx context.Context, c store.Comment) (store.Comment, error)
	// EditComment rewrites the body of a comment already stored, leaving
	// everything about where it is anchored alone.
	EditComment(id, body string) error
	RemoveComment(ctx context.Context, id string) error
	SetResolved(id string, resolved bool) error

	StageFile(ctx context.Context, path string) error
	UnstageFile(ctx context.Context, path string) error
	StageAll(ctx context.Context) error
	UnstageAll(ctx context.Context) error

	// Context returns each side's own copy of the files a session shows, line by
	// line, so the unchanged code the diff leaves out can be read in place. A
	// side it has no copy of is one the review cannot expand.
	Context(ctx context.Context, s *app.Session) (map[FileSide][]string, error)

	// OpenFile hands a file to the desktop, for reading it outside the diff.
	OpenFile(ctx context.Context, path string) error
	// Copy puts text on the system clipboard.
	Copy(ctx context.Context, text string) error

	// Folded returns the files folded away when this review was last read.
	Folded() ([]string, error)
	// SetFolded records the files folded away now.
	SetFolded(paths []string) error

	// AgentCommentsHidden reports whether the agent's notes were out of the
	// diff when this review was last read.
	AgentCommentsHidden() (bool, error)
	// SetAgentCommentsHidden records whether they are out of it now.
	SetAgentCommentsHidden(hidden bool) error

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

func (b *appBackend) Comments(ctx context.Context) ([]store.Comment, error) {
	all, err := b.app.Comments.List(b.session.CommentFilter())
	if err != nil {
		return nil, err
	}
	return b.app.Relocate(ctx, b.session, all), nil
}

// AddComment freezes the file the note is about before storing it, so the line
// number going into the store has a version of the file to be a number in.
//
// A snapshot that fails is not worth losing the note over: the comment is stored
// without one and behaves as notes did before anchors existed.
func (b *appBackend) AddComment(ctx context.Context, c store.Comment) (store.Comment, error) {
	c.Target = b.session.Target
	if c.Blob == "" {
		if blob, err := b.app.Snapshot(ctx, b.session, c); err == nil {
			c.Blob = blob
		}
	}
	created, err := b.app.Comments.Add(c)
	if err != nil {
		return store.Comment{}, err
	}
	return created, b.app.KeepAnchors(ctx)
}

// EditComment replaces what a note says. The blob it is anchored to is left
// exactly as it was: the note is about the same code as before, and re-freezing
// the file here would move it on to whatever that line holds now.
func (b *appBackend) EditComment(id, body string) error {
	_, err := b.app.Comments.Update(id, func(c *store.Comment) { c.Body = body })
	return err
}

func (b *appBackend) RemoveComment(ctx context.Context, id string) error {
	if err := b.app.Comments.Remove(id); err != nil {
		return err
	}
	return b.app.KeepAnchors(ctx)
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

// Context reads the copy of each file its side's hunks are numbered against.
//
// The session is passed in rather than read off the backend: this runs on its
// own goroutine, beside the reload that would otherwise be replacing the field
// underneath it, and the files to read are the ones the reviewer is looking at.
//
// A file that cannot be read — deleted, or gone since the diff — is left out
// rather than failing the rest, exactly as it is left out of the pass: what it
// costs is the expanders on that one file.
func (b *appBackend) Context(ctx context.Context, s *app.Session) (map[FileSide][]string, error) {
	// A pull request is not in this working tree. The paths in it name local
	// files that have nothing to do with the review, and reading context out of
	// those would put code on screen the changeset never touched.
	if s == nil || s.PR != nil {
		return nil, nil
	}

	out := map[FileSide][]string{}
	for _, f := range s.Files {
		if f.IsBinary() {
			continue
		}
		if f.Unstaged != nil {
			if lines, err := b.app.Repo.WorkingLines(f.Path); err == nil {
				out[FileSide{Path: f.Path}] = lines
			}
		}
		if f.Staged != nil {
			if lines, err := b.stagedLines(ctx, f); err == nil {
				out[FileSide{Path: f.Path, Staged: true}] = lines
			}
		}
	}
	return out, nil
}

// stagedLines returns the copy the index holds.
//
// Only a part-staged file is worth the git call: a file with everything staged
// has no unstaged diff, and having no unstaged diff is exactly what it means for
// the file on disk to be the file in the index.
func (b *appBackend) stagedLines(ctx context.Context, f git.FileEntry) ([]string, error) {
	if f.State() == git.StatePartial {
		return b.app.Repo.IndexLines(ctx, f.Path)
	}
	return b.app.Repo.WorkingLines(f.Path)
}

func (b *appBackend) OpenFile(ctx context.Context, path string) error {
	return b.app.OpenFile(ctx, path)
}

func (b *appBackend) Copy(ctx context.Context, text string) error {
	return b.app.Copy(ctx, text)
}

func (b *appBackend) Folded() ([]string, error) {
	return b.app.Folds.Load(b.session.Target)
}

func (b *appBackend) SetFolded(paths []string) error {
	return b.app.Folds.Save(b.session.Target, paths)
}

func (b *appBackend) AgentCommentsHidden() (bool, error) {
	view, err := b.app.Views.Load(b.session.Target)
	return view.AgentCommentsHidden, err
}

// SetAgentCommentsHidden reads the view back before writing it, so a filter
// added later is not dropped by the one being changed here.
func (b *appBackend) SetAgentCommentsHidden(hidden bool) error {
	view, err := b.app.Views.Load(b.session.Target)
	if err != nil {
		return err
	}
	view.AgentCommentsHidden = hidden
	return b.app.Views.Save(b.session.Target, view)
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
