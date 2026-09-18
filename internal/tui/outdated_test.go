package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

// outdatedNote is a note whose code was rewritten out from under it.
func outdatedNote() store.Comment {
	return store.Comment{
		ID: "c1", File: "svc.go", Line: 42, Side: store.SideNew,
		Body: "this leaks the tx", Author: store.AuthorUser, Outdated: true,
	}
}

// TestAnOutdatedNoteCarriesItsOwnExplanation checks the tag says what happened.
// Parked under a file saying nothing, the note gives a reader no way to tell
// what it was ever about.
func TestAnOutdatedNoteCarriesItsOwnExplanation(t *testing.T) {
	tag := commentTag(outdatedNote())
	if !strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want it to say the note is outdated", tag)
	}
	if !strings.Contains(tag, "42") {
		t.Errorf("tag = %q, want the line it was written on", tag)
	}
}

// TestAResolvedNoteStillReadsAsResolvedWhenOutdated keeps the two marks from
// displacing each other: a note can be both, and the ✓ is the one the reviewer
// scans for.
func TestAResolvedNoteStillReadsAsResolvedWhenOutdated(t *testing.T) {
	c := outdatedNote()
	c.Resolved = true
	tag := commentTag(c)
	if !strings.Contains(tag, "✓") || !strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want both the resolved mark and the outdated note", tag)
	}
}

// TestACurrentNoteSaysNothingExtra guards against tagging every note. The mark
// only means something if it is absent when the note is fine.
func TestACurrentNoteSaysNothingExtra(t *testing.T) {
	c := outdatedNote()
	c.Outdated = false
	if tag := commentTag(c); strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want no mark on a note that is where it says", tag)
	}
}

// TestTheHandoffWarnsAboutAnOutdatedLine covers the paste path: an agent that
// cannot read the store is handed the notes as text, and a line number it must
// not take at face value has to say so there too.
func TestTheHandoffWarnsAboutAnOutdatedLine(t *testing.T) {
	got := handoffOf([]store.Comment{outdatedNote()})
	if !strings.Contains(got, "svc.go:42") {
		t.Errorf("the handoff lost the anchor:\n%s", got)
	}
	if !strings.Contains(got, "since changed") {
		t.Errorf("the handoff sends an agent to a line that has been rewritten:\n%s", got)
	}
}

func TestTheHandoffNamesTheNearestLineOfAnOutdatedNote(t *testing.T) {
	c := outdatedNote()
	c.NearestLine = 45
	got := handoffOf([]store.Comment{c})
	if !strings.Contains(got, "svc.go:42") || !strings.Contains(got, "line 45 is the nearest line now") {
		t.Errorf("the handoff does not say where the note's code was and where to look now:\n%s", got)
	}
}

func TestAnOutdatedNoteHangsOffItsNearestLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 40, Side: store.SideNew, Body: "rename this",
			Author: store.AuthorUser, Outdated: true, NearestLine: 4},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 1 {
		t.Fatal("the note was not drawn")
	}
	if m.doc.Rows[row].Hunk < 0 {
		t.Fatal("the note was drawn under its file, want it on its nearest line")
	}
	above := m.doc.Rows[row-1]
	if above.Kind != RowLine {
		t.Fatalf("the row above the note is a %v, want the line it hangs off", above.Kind)
	}
	if got := m.doc.Hunks[above.Hunk].Hunk.Lines[above.Left].NewLine; got != 4 {
		t.Errorf("the note hangs off line %d, want its nearest line 4", got)
	}
	if got := m.renderer.Row(m.doc, row, RowState{}); !strings.Contains(got, "outdated · was :40") {
		t.Errorf("the note reads %q, want it still marked outdated with the line it was written on", got)
	}
}

func TestANoteWrittenOnAnOutdatedNoteGoesOnItsNearestLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 40, EndLine: 42, Side: store.SideNew, Body: "rename this",
			Author: store.AuthorUser, Outdated: true, NearestLine: 4},
	}
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "c")
	typeText(t, m, "done")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 4 || got.EndLine != 0 {
		t.Errorf("the new note is on %d-%d, want line 4, where the outdated note is drawn", got.Line, got.EndLine)
	}
}

// TestTheHandoffLeavesACurrentLinePlain keeps the warning meaningful.
func TestTheHandoffLeavesACurrentLinePlain(t *testing.T) {
	c := outdatedNote()
	c.Outdated = false
	got := handoffOf([]store.Comment{c})
	if strings.Contains(got, "since changed") {
		t.Errorf("a note that is where it says was warned about:\n%s", got)
	}
}

// stackedNotes are three notes on one file whose code has been rewritten out
// from under all of them, so none hangs off a line and all three are drawn
// under the file, one on the next.
func stackedNotes() []store.Comment {
	notes := make([]store.Comment, 0, 3)
	for i, body := range []string{"first", "second", "third"} {
		notes = append(notes, store.Comment{
			ID: fmt.Sprintf("c%d", i+1), File: "alpha.go", Line: 40 + i,
			Side: store.SideNew, Body: body, Author: store.AuthorUser, Outdated: true,
		})
	}
	return notes
}

// TestDeletingAStackedNoteMovesToTheOneUnderIt is what the stack costs
// otherwise: the row over a stack of notes is the last line of a diff none of
// them was about, so falling back to it sends the reviewer to the end of the
// file to delete the next note in a list they are working down.
func TestDeletingAStackedNoteMovesToTheOneUnderIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = stackedNotes()
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 0 {
		t.Fatal("the note was not drawn")
	}
	m.moveTo(row)
	press(t, m, "D")

	got, ok := m.doc.CommentAt(m.cursor)
	if !ok {
		t.Fatalf("cursor is on a %v, want the note under the one deleted", m.doc.Rows[m.cursor].Kind)
	}
	if got.ID != "c2" {
		t.Errorf("cursor is on %q, want c2 — the next note down the stack", got.ID)
	}
}

// TestDeletingTheLastStackedNoteMovesToTheOneOverIt closes the other end: there
// is nothing under it, and the note it was written beneath is nearer than
// anything else on screen.
func TestDeletingTheLastStackedNoteMovesToTheOneOverIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = stackedNotes()
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfComment("c3"))
	press(t, m, "D")

	got, ok := m.doc.CommentAt(m.cursor)
	if !ok {
		t.Fatalf("cursor is on a %v, want the note over the one deleted", m.doc.Rows[m.cursor].Kind)
	}
	if got.ID != "c2" {
		t.Errorf("cursor is on %q, want c2 — the note the deleted one sat under", got.ID)
	}
}

// TestDeletingTheOnlyStackedNoteStaysWhereItWas keeps the fallback narrow. With
// no note beside it the cursor has nowhere better to be than the row the note
// was drawn under, which is where the reviewer was already looking.
func TestDeletingTheOnlyStackedNoteStaysWhereItWas(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = stackedNotes()[:1]
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	want := m.doc.AnchorOf(row)
	m.moveTo(row)
	press(t, m, "D")

	if m.cursor != want {
		t.Errorf("cursor is at row %d, want row %d — the row the note was drawn under", m.cursor, want)
	}
}
