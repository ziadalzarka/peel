package tui

import (
	"context"
	"maps"
	"runtime"
	"strings"
	"sync"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
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
	//
	// It is called after every change, so it is expected to hand back the copies
	// it read before rather than read a whole changeset again for a file that
	// moved, and to say whether it turned up anything new.
	Context(ctx context.Context, s *app.Session) (Copies, error)

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

	// ReviewPayload is what posting the review would send: the summary written
	// for it and the comments that can be posted inline. It is what the question
	// asked before anything leaves the machine is asked against.
	ReviewPayload(body string, event forge.ReviewEvent) (forge.Review, error)
	// SubmitReview posts that review to the code host and marks the comments it
	// carried resolved. It is the only thing peel does that anyone else can see,
	// so nothing calls it without the reviewer having said yes.
	SubmitReview(ctx context.Context, body string, event forge.ReviewEvent) (forge.Review, error)
}

// appBackend adapts an App and a session to the Backend interface.
type appBackend struct {
	app     *app.App
	session *app.Session
	// state is where this review is kept: the repository's own files for the
	// working tree, the file named after the pull request for one of those.
	state    app.State
	provider string

	// mu guards copies, which is read and written on whichever goroutine the UI
	// puts a read on.
	mu sync.Mutex
	// copies are the file copies already read, by what produced them, so a
	// reload only pays for the ones that have actually moved on.
	copies map[copyID][]string
	// handed is what the last read gave back, for saying whether this one is
	// giving back anything different.
	handed map[FileSide]copyID
}

// NewBackend returns the Backend the real UI runs against.
func NewBackend(a *app.App, s *app.Session, provider ...string) Backend {
	name := ""
	if len(provider) > 0 {
		name = provider[0]
	}
	return &appBackend{app: a, session: s, state: a.StateFor(s), provider: name}
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
	all, err := b.state.Comments.List(b.session.CommentFilter())
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
	created, err := b.state.Comments.Add(c)
	if err != nil {
		return store.Comment{}, err
	}
	return created, b.keepAnchors(ctx)
}

// EditComment replaces what a note says. The blob it is anchored to is left
// exactly as it was: the note is about the same code as before, and re-freezing
// the file here would move it on to whatever that line holds now.
func (b *appBackend) EditComment(id, body string) error {
	_, err := b.state.Comments.Update(id, func(c *store.Comment) { c.Body = body })
	return err
}

func (b *appBackend) RemoveComment(ctx context.Context, id string) error {
	if err := b.state.Comments.Remove(id); err != nil {
		return err
	}
	return b.keepAnchors(ctx)
}

func (b *appBackend) SetResolved(id string, resolved bool) error {
	_, err := b.state.Comments.Update(id, func(c *store.Comment) { c.Resolved = resolved })
	return err
}

// keepAnchors holds open the snapshots this repository's notes are measured
// from. A pull request's notes are anchored to nothing here, so a review of one
// has no objects to keep.
func (b *appBackend) keepAnchors(ctx context.Context) error {
	if b.session.PR != nil {
		return nil
	}
	return b.app.KeepAnchors(ctx)
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
// Only the copies that have actually moved on are read. Staging one file among
// two hundred leaves the other hundred and ninety-nine byte for byte as they
// were, and reading them again would cost a pass over the whole changeset to
// hand back what the screen is already drawn from.
//
// A file that cannot be read — deleted, or gone since the diff — is left out
// rather than failing the rest, exactly as it is left out of the pass: what it
// costs is the expanders on that one file.
func (b *appBackend) Context(ctx context.Context, s *app.Session) (Copies, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A pull request is not in this working tree. The paths in it name local
	// files that have nothing to do with the review, and reading context out of
	// those would put code on screen the changeset never touched.
	if s == nil || s.PR != nil {
		fresh := len(b.handed) > 0
		b.copies, b.handed = nil, nil
		return Copies{Fresh: fresh}, nil
	}

	want := copiesWanted(s)
	held := make(map[copyID][]string, len(want))
	files := make(map[FileSide][]string, len(want))
	sides := make(map[FileSide]copyID, len(want))
	var missing []copyRead
	for _, r := range want {
		lines, ok := b.copies[r.id]
		if !ok {
			missing = append(missing, r)
			continue
		}
		held[r.id] = lines
		files[r.side] = lines
		sides[r.side] = r.id
	}
	for r, lines := range b.readCopies(ctx, missing) {
		held[r.id] = lines
		files[r.side] = lines
		sides[r.side] = r.id
	}

	// What was handed back is named by what produced it, so the same sides
	// carrying the same IDs is the same screen: nothing was read, nothing was
	// dropped, and nothing moved from one side of a file to the other.
	fresh := !maps.Equal(b.handed, sides)
	b.copies, b.handed = held, sides
	return Copies{Files: files, Fresh: fresh}, nil
}

// readCopies reads the copies not already held, several at a time.
//
// A changeset of any size is a file read per side and a git call per part-staged
// one, and doing them in a row is where a reload that has to read them spends
// all of its time. They are independent — different files, different processes —
// so they go out together, bounded so a large changeset does not fork a hundred
// gits at once.
func (b *appBackend) readCopies(ctx context.Context, missing []copyRead) map[copyRead][]string {
	if len(missing) == 0 {
		return nil
	}
	lines := make([][]string, len(missing))
	read := make([]bool, len(missing))
	slots := make(chan struct{}, min(len(missing), max(runtime.NumCPU(), 4)))

	var wg sync.WaitGroup
	for i, r := range missing {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			got, err := b.readCopy(ctx, r)
			if err != nil {
				return
			}
			lines[i], read[i] = got, true
		}()
	}
	wg.Wait()

	out := make(map[copyRead][]string, len(missing))
	for i, r := range missing {
		if read[i] {
			out[r] = lines[i]
		}
	}
	return out
}

func (b *appBackend) readCopy(ctx context.Context, r copyRead) ([]string, error) {
	if r.index {
		return b.app.Repo.IndexLines(ctx, r.side.Path)
	}
	return b.app.Repo.WorkingLines(r.side.Path)
}

// Copies are the files a session's hunks are numbered against, read whole so the
// unchanged code the diff leaves out can be drawn in place.
type Copies struct {
	// Files holds each side's own copy, line by line. A side with no entry is
	// one the review cannot expand.
	Files map[FileSide][]string
	// Fresh reports that this read turned up something the one before it did
	// not: a file read again, or one that has left the review. A change to one
	// file among many leaves it false, and a screen drawn from copies that are
	// all still the same copies does not need drawing again.
	Fresh bool
}

// copyID names one copy of one file by what produced it: the path, and the
// changes standing between HEAD and the content being read. Two copies with the
// same ID are the same bytes, so the second one is not worth reading.
//
// It is also what lets a file staged whole keep the copy already read for it:
// `git add` puts exactly the disk's content into the index, so the index's copy
// afterwards is the working copy from before under another name.
type copyID struct {
	Path string
	// Changes fingerprints the diff that produces the content — the staged one
	// for the index's copy, both of them for the disk's. A file's content is
	// what HEAD holds plus that diff, which is the same assumption peel makes
	// when it drops a reload whose fingerprint has not moved.
	Changes string
}

// copyRead is one copy to read: the side of the file it is drawn beside, what
// produced it, and whether it comes out of the index rather than the disk.
type copyRead struct {
	side  FileSide
	id    copyID
	index bool
}

// copiesWanted lists the copies a session needs.
//
// Only a part-staged file is worth the git call: a file with everything staged
// has no unstaged diff, and having no unstaged diff is exactly what it means for
// the file on disk to be the file in the index.
func copiesWanted(s *app.Session) []copyRead {
	out := make([]copyRead, 0, len(s.Files))
	for _, f := range s.Files {
		if f.IsBinary() {
			continue
		}
		if f.Unstaged != nil {
			out = append(out, copyRead{
				side: FileSide{Path: f.Path},
				id:   copyID{Path: f.Path, Changes: changesOf(f.Staged, f.Unstaged)},
			})
		}
		if f.Staged != nil {
			out = append(out, copyRead{
				side:  FileSide{Path: f.Path, Staged: true},
				id:    copyID{Path: f.Path, Changes: changesOf(f.Staged)},
				index: f.State() == git.StatePartial,
			})
		}
	}
	return out
}

// changesOf fingerprints the diffs that stand between HEAD and one copy of a
// file. The sides are written out unlabelled, so the change that was unstaged a
// moment ago hashes the same once it is staged — which is exactly when the copy
// read off the disk is the copy the index now holds.
func changesOf(diffs ...*git.FileDiff) string {
	var b strings.Builder
	for _, d := range diffs {
		writeDiffFingerprint(&b, d)
	}
	return store.Fingerprint(b.String())
}

func (b *appBackend) OpenFile(ctx context.Context, path string) error {
	return b.app.OpenFile(ctx, path)
}

func (b *appBackend) Copy(ctx context.Context, text string) error {
	return b.app.Copy(ctx, text)
}

func (b *appBackend) Folded() ([]string, error) {
	return b.state.Folds.Load(b.session.Target)
}

func (b *appBackend) SetFolded(paths []string) error {
	return b.state.Folds.Save(b.session.Target, paths)
}

func (b *appBackend) AgentCommentsHidden() (bool, error) {
	view, err := b.state.Views.Load(b.session.Target)
	return view.AgentCommentsHidden, err
}

// SetAgentCommentsHidden reads the view back before writing it, so a filter
// added later is not dropped by the one being changed here.
func (b *appBackend) SetAgentCommentsHidden(hidden bool) error {
	view, err := b.state.Views.Load(b.session.Target)
	if err != nil {
		return err
	}
	view.AgentCommentsHidden = hidden
	return b.state.Views.Save(b.session.Target, view)
}

// ReviewPayload is what P would post: the summary being written and the notes
// that can go with it, built exactly as `peel pr submit` builds one.
func (b *appBackend) ReviewPayload(body string, event forge.ReviewEvent) (forge.Review, error) {
	return b.app.PreviewSubmission(b.session, b.submitOptions(body, event))
}

func (b *appBackend) SubmitReview(ctx context.Context, body string, event forge.ReviewEvent) (forge.Review, error) {
	return b.app.SubmitReview(ctx, b.session, b.submitOptions(body, event))
}

// submitOptions is how a review posted from the UI is put together: the
// comments it carries are resolved once they are posted, since a note the other
// side can now read is one that has left the reviewer's own list.
func (b *appBackend) submitOptions(body string, event forge.ReviewEvent) app.SubmitOptions {
	return app.SubmitOptions{Body: body, Event: event, ResolveAfter: true}
}

// Walkthrough returns the narrative of the session, cached with the rest of
// this review's state.
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
