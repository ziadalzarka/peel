package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/store"
)

// A note is corrected where it stands: `e` opens the editor holding what the
// note says, and saving rewrites that note rather than leaving a second one
// beside it saying nearly the same thing.
func TestEditingANoteRewritesItInPlace(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("u1"))
	press(t, m, "e")
	if m.mode != modeComment {
		t.Fatalf("mode = %v, want the editor open", m.mode)
	}
	if got := m.input.Value(); got != "mine, keep it" {
		t.Fatalf("the editor opened holding %q, want what the note says", got)
	}

	typeText(t, m, " — and the one below")
	press(t, m, "enter")

	want := "mine, keep it — and the one below"
	if got := backend.edited; len(got) != 1 || got[0].id != "u1" || got[0].body != want {
		t.Fatalf("EditComment calls = %+v, want one rewriting u1 to %q", got, want)
	}
	if len(backend.added) != 0 {
		t.Errorf("editing left a second comment behind: %+v", backend.added)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, want) {
		t.Errorf("the note on screen is not what was written:\n%s", got)
	}
}

// The editor stands in the note's place rather than under it. Both at once is
// the same note twice, one of them out of date before it is read.
func TestTheEditorStandsWhereTheNoteDid(t *testing.T) {
	m := newModel(t, reviewedByBoth(t))

	row := m.doc.RowOfComment("u1")
	m.moveTo(row)
	press(t, m, "e")

	if m.doc.RowOfComment("u1") >= 0 {
		t.Error("the note is still drawn under the editor rewriting it")
	}
	if m.doc.DraftRow != row {
		t.Errorf("editor starts at row %d, want the note's own row (%d)", m.doc.DraftRow, row)
	}
	if got := ansi.Strip(m.View()); strings.Count(got, "mine, keep it") != 1 {
		t.Errorf("the note reads twice — once saved, once in the editor:\n%s", got)
	}
}

// A note whose code has gone is drawn under its file, and its line number now
// names something else entirely. The editor follows the note rather than the
// number, or rewriting one would open the editor down in the diff, against code
// the note was never about.
func TestTheEditorFollowsANoteWhoseCodeIsGone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "u1", File: "alpha.go", Line: 3, Side: store.SideNew,
			Body: "this leaks the tx", Author: store.AuthorUser, Outdated: true},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("u1")
	if row < 0 {
		t.Fatal("the outdated note was not placed")
	}
	m.moveTo(row)
	press(t, m, "e")

	if m.doc.DraftRow != row {
		t.Errorf("editor starts at row %d, want the note's own row (%d)", m.doc.DraftRow, row)
	}
	if got := draftRows(m.doc); got != m.input.Height() {
		t.Errorf("the editor was laid out %d times over (%d rows for a %d row editor)",
			got/max(m.input.Height(), 1), got, m.input.Height())
	}
}

// Cancelling leaves the note exactly as it was, and the cursor back on it: `e`
// was pressed on that note, and an editor put away is not a move through the
// diff.
func TestCancellingAnEditLeavesTheNoteAsItWas(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	row := m.doc.RowOfComment("u1")
	m.moveTo(row)
	press(t, m, "e")
	typeText(t, m, " rewritten")
	press(t, m, "esc")

	if len(backend.edited) != 0 {
		t.Fatalf("cancelling wrote the edit anyway: %+v", backend.edited)
	}
	if got, _ := commentByID(m.doc.Comments, "u1"); got.Body != "mine, keep it" {
		t.Errorf("body = %q, want what the note said before", got.Body)
	}
	if got := m.doc.RowOfComment("u1"); got != m.cursor {
		t.Errorf("cursor on row %d, want the note it was opened from (%d)", m.cursor, got)
	}
}

// Emptying the editor is not how a note is deleted. `D` is that key, and it
// names what it is about to remove; a note that vanished because the editor was
// cleared would be a review lost to a keystroke meant to start again.
func TestAnEmptiedEditKeepsTheNote(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("u1"))
	press(t, m, "e")
	m.input.SetValue("   ")
	press(t, m, "enter")

	if len(backend.edited) != 0 {
		t.Fatalf("an empty body was written: %+v", backend.edited)
	}
	if len(backend.removed) != 0 {
		t.Fatalf("emptying the editor deleted the note: %v", backend.removed)
	}
	if m.doc.RowOfComment("u1") < 0 {
		t.Error("the note is not on screen any more")
	}
	if !strings.Contains(m.status, "D") {
		t.Errorf("status = %q, want it to say which key deletes", m.status)
	}
}

// A note saved unchanged is not a write. The store would take it, but the
// screen has nothing to redraw and the reload behind it would be paid for
// nothing.
func TestSavingANoteUnchangedWritesNothing(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("u1"))
	press(t, m, "e", "enter")

	if len(backend.edited) != 0 {
		t.Fatalf("EditComment calls = %+v, want none", backend.edited)
	}
	if !strings.Contains(m.status, "unchanged") {
		t.Errorf("status = %q", m.status)
	}
}

// The agent's notes are signed by the agent. Rewriting one would put the
// reviewer's words behind that name — where `C` leaves them out of the review it
// hands over and `X` clears them with the rest of the agent's pass.
func TestTheAgentsNotesAreNotEditable(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("a1"))
	press(t, m, "e")

	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want the editor to have stayed shut", m.mode)
	}
	if !strings.Contains(m.status, "agent") {
		t.Errorf("status = %q, want it to say whose note it is", m.status)
	}
}

// `e` anywhere else says what it wanted, the way every other key that acts on a
// comment does.
func TestEditingAwayFromANoteSaysSo(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "j", "e")

	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want browse", m.mode)
	}
	if !strings.Contains(m.status, "move to a comment") {
		t.Errorf("status = %q", m.status)
	}
}

// A note about a run of lines keeps the run it was written on: `e` changes what
// the note says and nothing about where it points.
func TestEditingANoteKeepsWhatItIsAbout(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "u1", File: "alpha.go", Line: 3, EndLine: 4, Side: store.SideNew,
			Body: "these two belong together", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("u1"))
	press(t, m, "e")
	typeText(t, m, "!")
	press(t, m, "enter")

	got, ok := commentByID(backend.comments, "u1")
	if !ok {
		t.Fatal("the note is not in the store any more")
	}
	if got.Line != 3 || got.EndLine != 4 || got.Side != store.SideNew {
		t.Errorf("the note now points at %s on %s, want alpha.go:3-4 on new", got.Location(), got.Side)
	}
	if got.Body != "these two belong together!" {
		t.Errorf("body = %q, want the edit", got.Body)
	}
}
