package tui

// Every change the reviewer makes is drawn before it is written.
//
// Staging a file, unstaging one, writing a note or resolving one is never in
// doubt — only slow. Running git, or writing the store, takes long enough that
// waiting for it before redrawing makes peel look like it is thinking about a
// decision that has already been made. So the screen is brought forward on the
// keypress, against what the change is about to do; the write happens behind it;
// and the reload that follows only confirms what is already on screen. A write
// that fails puts the screen back and says why.
//
// Two rules keep the guess from ever being seen:
//
//   - Writes are queued, so peel's git calls reach the repository in the order
//     the keys were pressed and never race each other for the index lock.
//   - A reload that lands while another write is still out is dropped: it read
//     git before that write, so applying it would undraw a change the reviewer
//     can already see. The last write of a burst is the one that reconciles. A
//     follow check is dropped on the wider rule that it started before a change
//     was pressed at all, since it can outlive the write that overtook it.

import (
	"context"
	"fmt"
	"maps"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// apply draws a change and writes it behind the screen.
//
// show updates the model, on the keypress, to what op is about to make true. op
// runs off the UI goroutine and is followed by a reload, so what the reviewer
// ends up looking at is what git has rather than what peel guessed.
func (m *Model) apply(show func(), op func(context.Context) error) tea.Cmd {
	return m.write(show, plain(op), nil, nil)
}

func (m *Model) reapply(show func(), op func(context.Context) error, missed func()) tea.Cmd {
	return m.write(show, plain(op), nil, missed)
}

func (m *Model) record(e *entry, show func(), op writeOp) tea.Cmd {
	m.hist.add(e)
	return m.write(show, op, e, nil)
}

type writeOp func(ctx context.Context) (recorded, error)

func plain(op func(context.Context) error) writeOp {
	return func(ctx context.Context) (recorded, error) { return recorded{}, op(ctx) }
}

func (m *Model) write(show func(), op writeOp, e *entry, missed func()) tea.Cmd {
	before := m.snapshot()
	show()

	m.writes.Add(1)
	// Every read already out was asked for before this change existed, and says
	// so by coming back with the older count.
	m.drawn.Add(1)
	wait, done := m.enqueue()
	writes, backend, ctx := &m.writes, m.backend, m.ctx
	return func() tea.Msg {
		if wait != nil {
			<-wait
		}
		defer done()

		got, err := op(ctx)
		if err != nil {
			writes.Add(-1)
			if e != nil {
				e.failed()
			}
			return revertMsg{err: err, before: before, missed: missed}
		}
		if e != nil {
			e.wrote(got)
		}
		// A change pressed after this one is on screen already and queued
		// already: its own reload will bring the truth for both, so this one is
		// not worth the git calls.
		if writes.Add(-1) > 0 {
			return nil
		}
		msg := load(ctx, backend, "")
		loaded, ok := msg.(loadedMsg)
		if !ok {
			// The write landed and only reading it back failed, so the screen is
			// right: report that without taking the change off it.
			return msg
		}
		loaded.reconcile = true
		return loaded
	}
}

// enqueue reserves this change's place in line: it waits for the change before
// it and lets the one after it through when it is done — the read-back included,
// so no git call of peel's runs against an index another one is refreshing.
//
// Places are taken on the keypress, so the repository sees the changes in the
// order they were pressed however the goroutines behind them are scheduled.
func (m *Model) enqueue() (wait <-chan struct{}, done func()) {
	ahead := m.writing
	mine := make(chan struct{})
	m.writing = mine
	return ahead, func() { close(mine) }
}

// revertMsg reports a change that failed to write. The change is on screen
// already, so reporting it means taking it back off.
type revertMsg struct {
	err    error
	before snapshot
	missed func()
}

// snapshot is the screen as it was before a change was drawn on it, kept so a
// write that fails can put it back without asking git for anything.
type snapshot struct {
	session  *app.Session
	comments []store.Comment
	folds    folds
	cursor   int
	top      int
	fileTop  int
	status   string
}

type folds struct {
	collapsed    map[string]bool
	staged       map[string]bool
	sides        map[string]bool
	walk         map[int]bool
	comments     map[string]bool
	othersHidden bool
}

func (m *Model) folds() folds {
	return folds{
		collapsed:    copied(m.collapsed),
		staged:       copied(m.stagedFolds),
		sides:        copied(m.sideFolds),
		walk:         copied(m.walkFolded),
		comments:     copied(m.commentFolds),
		othersHidden: m.othersHidden,
	}
}

func copied[K comparable, V any](from map[K]V) map[K]V {
	out := make(map[K]V, len(from))
	maps.Copy(out, from)
	return out
}

func (m *Model) putFolds(before folds) {
	m.putFoldsBack(before.collapsed)
	m.stagedFolds = copied(before.staged)
	m.sideFolds = copied(before.sides)
	m.walkFolded = copied(before.walk)
	m.commentFolds = copied(before.comments)
	m.setOthersHidden(before.othersHidden)
}

type viewport struct {
	cursor  int
	top     int
	fileTop int
}

func (m *Model) viewport() viewport {
	return viewport{cursor: m.cursor, top: m.top, fileTop: m.fileTop}
}

func (m *Model) putViewport(v viewport) {
	m.cursor = min(v.cursor, max(m.doc.Len()-1, 0))
	m.top, m.fileTop = v.top, v.fileTop
	m.clampTop()
	m.clampFileTop()
}

func (m *Model) snapshot() snapshot {
	return snapshot{
		session:  m.session,
		comments: m.comments,
		folds:    m.folds(),
		cursor:   m.cursor,
		top:      m.top,
		fileTop:  m.fileTop,
		status:   m.status,
	}
}

// restore puts back the screen a change that failed was drawn over.
func (m *Model) restore(before snapshot) {
	m.session = before.session
	m.comments = before.comments
	m.status = before.status
	m.putFolds(before.folds)
	m.rebuild()
	m.putViewport(viewport{cursor: before.cursor, top: before.top, fileTop: before.fileTop})
}

// putFoldsBack undoes the folding a change did, through setCollapsed so that
// what is recorded on disk goes back with the screen and a fold that did not
// move is not rewritten.
func (m *Model) putFoldsBack(before map[string]bool) {
	change := make(map[string]bool, len(m.collapsed)+len(before))
	for path := range m.collapsed {
		change[path] = before[path]
	}
	for path, hidden := range before {
		change[path] = hidden
	}
	m.setCollapsed(change)
}

// restaged returns the session as it will read once the wanted files have been
// staged, or unstaged when stage is false.
//
// Staging a whole file moves its changes to the index, and the index is one of
// the two sides the file list and the diff are drawn from, so the result can be
// drawn before git has been asked for it. A file that is part staged and part
// modified is the one case the guess is only close: the two sides are merged by
// git, not by peel, and the reload behind the change settles the difference.
func restaged(s *app.Session, stage bool, wanted func(path string) bool) *app.Session {
	out := *s
	out.Files = make([]git.FileEntry, len(s.Files))
	copy(out.Files, s.Files)
	for i, entry := range out.Files {
		if !wanted(entry.Path) {
			continue
		}
		switch {
		case stage:
			if entry.Unstaged != nil {
				entry.Staged, entry.Unstaged = entry.Unstaged, nil
			}
			// Staging an untracked file is how it becomes tracked.
			entry.Untracked = false
		case entry.Staged != nil:
			if entry.Unstaged == nil {
				entry.Unstaged = entry.Staged
			}
			entry.Staged = nil
		}
		out.Files[i] = entry
	}
	return &out
}

// restagedHunk returns the session as it will read once one hunk has moved into
// the index, and the entry that moved.
//
// A whole file is a swap of sides and needs nothing from git to draw; one hunk
// out of several needs the two diffs renumbered around it, which is what the git
// package works out. It is false where that cannot be worked out — the hunk is
// not where the screen thinks it is, which means the tree has moved and the
// read-back is the only thing that can answer.
func restagedHunk(s *app.Session, id git.HunkID) (*app.Session, git.FileEntry, bool) {
	for i, entry := range s.Files {
		if entry.Path != id.Path {
			continue
		}
		moved, ok := entry.WithHunkStaged(id)
		if !ok {
			return s, git.FileEntry{}, false
		}
		out := *s
		out.Files = make([]git.FileEntry, len(s.Files))
		copy(out.Files, s.Files)
		out.Files[i] = moved
		return &out, moved, true
	}
	return s, git.FileEntry{}, false
}

func withEntry(s *app.Session, entry git.FileEntry) *app.Session {
	out := *s
	out.Files = make([]git.FileEntry, len(s.Files))
	copy(out.Files, s.Files)
	for i := range out.Files {
		if out.Files[i].Path == entry.Path {
			out.Files[i] = entry
			return &out
		}
	}
	return s
}

// only names one file for restaged, and every names all of them.
func only(path string) func(string) bool {
	return func(p string) bool { return p == path }
}

func every(string) bool { return true }

// unsavedPrefix marks a comment on screen whose write has not landed yet. The
// keys that act on a comment act by ID, and the store does not know this one's
// ID until it has assigned it.
const unsavedPrefix = "unsaved:"

// unsaved reports whether c is a comment drawn ahead of its write.
func unsaved(c store.Comment) bool { return strings.HasPrefix(c.ID, unsavedPrefix) }

// unsavedID numbers a comment drawn ahead of its write, so the document can
// place it and the cursor can be put back on it.
func (m *Model) unsavedID() string {
	m.unsavedIDs++
	return fmt.Sprintf("%s%d", unsavedPrefix, m.unsavedIDs)
}

// The comment list arrives from the store and is still the store's, so every
// change to it copies rather than writes into what it was handed.

func withComment(comments []store.Comment, add store.Comment) []store.Comment {
	return append(append([]store.Comment(nil), comments...), add)
}

func withResolved(comments []store.Comment, id string, resolved bool) []store.Comment {
	out := append([]store.Comment(nil), comments...)
	for i := range out {
		if out[i].ID == id {
			out[i].Resolved = resolved
		}
	}
	return out
}

func withBody(comments []store.Comment, id, body string) []store.Comment {
	out := append([]store.Comment(nil), comments...)
	for i := range out {
		if out[i].ID == id {
			out[i].Body = body
		}
	}
	return out
}

// commentByID finds a comment the screen is holding, for a change that needs
// what it says as well as its ID.
func commentByID(comments []store.Comment, id string) (store.Comment, bool) {
	for _, c := range comments {
		if c.ID == id {
			return c, true
		}
	}
	return store.Comment{}, false
}

func withoutComment(comments []store.Comment, id string) []store.Comment {
	out := make([]store.Comment, 0, len(comments))
	for _, c := range comments {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}

func withoutComments(comments []store.Comment, ids []string) []store.Comment {
	gone := make(map[string]bool, len(ids))
	for _, id := range ids {
		gone[id] = true
	}
	out := make([]store.Comment, 0, len(comments))
	for _, c := range comments {
		if !gone[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
