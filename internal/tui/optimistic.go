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
//     can already see. The last write of a burst is the one that reconciles.

import (
	"context"
	"fmt"
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
	before := m.snapshot()
	show()

	m.writes.Add(1)
	wait, done := m.enqueue()
	writes, backend, ctx := &m.writes, m.backend, m.ctx
	return func() tea.Msg {
		if wait != nil {
			<-wait
		}
		defer done()

		if err := op(ctx); err != nil {
			writes.Add(-1)
			return revertMsg{err: err, before: before}
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
}

// snapshot is the screen as it was before a change was drawn on it, kept so a
// write that fails can put it back without asking git for anything.
type snapshot struct {
	session   *app.Session
	comments  []store.Comment
	collapsed map[string]bool
	cursor    int
	top       int
	fileTop   int
	status    string
}

func (m *Model) snapshot() snapshot {
	folded := make(map[string]bool, len(m.collapsed))
	for path, hidden := range m.collapsed {
		folded[path] = hidden
	}
	return snapshot{
		session:   m.session,
		comments:  m.comments,
		collapsed: folded,
		cursor:    m.cursor,
		top:       m.top,
		fileTop:   m.fileTop,
		status:    m.status,
	}
}

// restore puts back the screen a change that failed was drawn over.
func (m *Model) restore(before snapshot) {
	m.session = before.session
	m.comments = before.comments
	m.status = before.status
	m.putFoldsBack(before.collapsed)
	m.rebuild()
	m.cursor = min(before.cursor, max(m.doc.Len()-1, 0))
	m.top, m.fileTop = before.top, before.fileTop
	m.clampTop()
	m.clampFileTop()
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
