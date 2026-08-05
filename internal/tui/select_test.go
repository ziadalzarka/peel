package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// The lines of alpha.go's only hunk, by their position in it: two context lines,
// the removal of old line 3, the two additions that replace it, and a context
// line after them.
const (
	removedOne = 2
	addedOne   = 3
	addedTwo   = 4
	lastLine   = 5
)

// markedRun is what the run on screen covers, as the first and last row of it.
func markedRun(t *testing.T, m *Model) (lo, hi int) {
	t.Helper()
	lo, hi, ok := m.selectedRows()
	if !ok {
		t.Fatal("no run of lines is marked")
	}
	return lo, hi
}

func TestShiftDownMarksTheLinesToWriteOneNoteAbout(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down")

	lo, hi := markedRun(t, m)
	if lo != lineRowOf(t, m, 0, addedOne) || hi != lineRowOf(t, m, 0, addedTwo) {
		t.Errorf("the run covers rows %d-%d, want the two added lines", lo, hi)
	}
	if m.cursor != hi {
		t.Errorf("cursor on row %d, want it moved to the far end of the run at %d", m.cursor, hi)
	}
	if !strings.Contains(m.status, "2 lines marked") {
		t.Errorf("status = %q, want it to say how much is marked", m.status)
	}

	press(t, m, "c")
	typeText(t, m, "these two belong together")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	got := backend.added[0]
	if got.Line != 3 || got.EndLine != 4 || got.Side != store.SideNew {
		t.Errorf("comment = %s:%d-%d on %s, want alpha.go:3-4 on the new side",
			got.File, got.Line, got.EndLine, got.Side)
	}
}

// Marking upwards is the same run, so the note is stored the same way round: a
// range whose first number is the second line read would be a range nothing else
// in peel could place.
func TestShiftUpMarksTheSameRunFromTheOtherEnd(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedTwo))
	press(t, m, "shift+up", "c")
	typeText(t, m, "read these together")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 3 || got.EndLine != 4 {
		t.Errorf("comment = lines %d-%d, want 3-4", got.Line, got.EndLine)
	}
}

// Shrinking is what makes the run a selection rather than a growing count: the
// end the arrow moves is the cursor's, and walking it back to where it started
// leaves an ordinary note on one line.
func TestReversingTheArrowGivesTheLinesBack(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down", "shift+up")

	if lo, hi := markedRun(t, m); lo != hi {
		t.Errorf("the run covers rows %d-%d, want it back to the one line it started on", lo, hi)
	}

	press(t, m, "c")
	typeText(t, m, "just this line")
	press(t, m, "enter")

	if got := backend.added[0]; got.Line != 3 || got.EndLine != 0 {
		t.Errorf("comment = line %d, end %d, want line 3 with no range", got.Line, got.EndLine)
	}
}

// A run is one continuous stretch of code, and between one hunk and the next the
// numbers in between are lines nobody is looking at.
func TestTheRunStopsAtTheEndOfTheHunk(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	last := lineRowOf(t, m, 0, lastLine)
	m.moveTo(last)
	press(t, m, "shift+down")

	if m.cursor != last {
		t.Errorf("cursor moved to row %d, want it left on the last line of the hunk at %d", m.cursor, last)
	}
	if lo, hi := markedRun(t, m); lo != last || hi != last {
		t.Errorf("the run covers rows %d-%d, want only the last line of the hunk", lo, hi)
	}
	if !strings.Contains(m.status, "end of the hunk") {
		t.Errorf("status = %q, want it to say why the run did not grow", m.status)
	}
}

func TestMarkingSaysWhereItCanStart(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "shift+down")

	if m.sel != nil {
		t.Error("a run was marked from a file header, which has no lines to mark")
	}
	if !strings.Contains(m.status, "move to a line of the diff") {
		t.Errorf("status = %q, want it to say where marking starts", m.status)
	}
}

// The run is held, not entered: every key that is not extending it or writing
// the note it is for is the reviewer moving on, and a run still marked behind
// them is a note about to land on lines they had stopped looking at.
func TestAnyOtherActionLetsTheMarkedRunGo(t *testing.T) {
	for _, key := range []string{"down", "up", "j", "k", "]", "[", "alt+down", "alt+up", "g", "G", " ", `\`, "b", "x"} {
		backend := newFakeBackend(newSession(t, twoFileDiff))
		m := newModel(t, backend)

		m.moveTo(lineRowOf(t, m, 0, addedOne))
		press(t, m, "shift+down", key)

		if m.sel != nil {
			t.Errorf("%q left the run marked", key)
		}
	}
}

// The wheel drags the cursor with it, so it moves off the run the same way an
// arrow does.
func TestTheWheelLetsTheMarkedRunGo(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down")
	send(t, m, wheelMsg(tea.MouseButtonWheelDown, m.filePaneWidth()+5))

	if m.sel != nil {
		t.Error("the wheel left the run marked")
	}
}

// `c` is what the run was marked for, and `C` hands the review over without
// touching the diff — neither is the reviewer moving on.
func TestTheCommentKeysKeepTheMarkedRun(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down", "C")

	if m.sel == nil {
		t.Fatal("copying the review let the run go")
	}

	press(t, m, "c")
	if m.sel == nil {
		t.Fatal("the run was let go of as the editor opened, with the note still unwritten")
	}
	// Opening the editor lays the document out again, with rows for it inside the
	// run itself — which is exactly where a run held as row numbers would come
	// apart.
	if lo, hi := markedRun(t, m); lo != lineRowOf(t, m, 0, addedOne) || hi != lineRowOf(t, m, 0, addedTwo) {
		t.Errorf("with the editor open the run covers rows %d-%d, want the two added lines", lo, hi)
	}
	if !strings.Contains(m.status, "alpha.go:3-4") {
		t.Errorf("status = %q, want it to name the run being commented on", m.status)
	}

	press(t, m, "esc")
	if m.sel != nil {
		t.Error("cancelling the note left the run marked")
	}
}

// A note is about one side of the diff, so a run marked from a removal is
// numbered against the file before the change and passes over the additions
// inside it — they have no number there.
func TestARunMarkedFromARemovalIsNumberedOnTheOldSide(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, removedOne))
	press(t, m, "shift+down", "shift+down", "c")
	typeText(t, m, "why did this go?")
	press(t, m, "enter")

	got := backend.added[0]
	if got.Side != store.SideOld || got.Line != 3 || got.EndLine != 0 {
		t.Errorf("comment = %s:%d-%d on %s, want old line 3 with nothing after it on that side",
			got.File, got.Line, got.EndLine, got.Side)
	}
}

func TestTheMarkedRunIsDrawnDownTheEdgeOfTheDiff(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down")

	rows := m.diffLines(m.bodyHeight())
	marked := rows[lineRowOf(t, m, 0, addedOne)-m.top]
	plain := rows[lineRowOf(t, m, 0, lastLine)-m.top]

	if !strings.HasPrefix(marked, "▌") {
		t.Errorf("a marked line reads %q, want the bar that says it is marked", marked)
	}
	if strings.HasPrefix(plain, "▌") {
		t.Errorf("an unmarked line reads %q, want no bar", plain)
	}
}

// A note on a run sits under the first line of it, so the run is the one thing
// about it nothing else on screen says.
func TestASavedRunNoteSaysWhichLinesItCovers(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Body: "these two", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 0 {
		t.Fatal("the note is not in the document")
	}
	if got := m.renderer.Row(m.doc, row, RowState{}); !strings.Contains(got, "lines 3-4") {
		t.Errorf("the note reads %q, want the run it covers", got)
	}
}

// A split row can hold a removal beside the addition that replaced it, so the
// run is over rows and the numbers come off the side the note is written on.
func TestARunIsMarkedInTheSplitLayoutToo(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend, WithLayout(LayoutSplit))

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down", "c")
	typeText(t, m, "both of these")
	press(t, m, "enter")

	if got := backend.added[0]; got.Line != 3 || got.EndLine != 4 || got.Side != store.SideNew {
		t.Errorf("comment = %s:%d-%d on %s, want alpha.go:3-4 on the new side",
			got.File, got.Line, got.EndLine, got.Side)
	}
}

// Both halves of a part-staged file have a line 3, and they are not the same
// line — so a run records which diff its two numbers are counted in, exactly as
// a single line does.
func TestARunRecordsWhichDiffItIsNumberedAgainst(t *testing.T) {
	backend := newFakeBackend(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}))
	m := newModel(t, backend)

	// The index's half opens folded, so the hunk on screen is the working tree's.
	hunk := -1
	for i, ref := range m.doc.Hunks {
		if !ref.Staged {
			hunk = i
			break
		}
	}
	if hunk < 0 {
		t.Fatal("the working tree's half is not on screen")
	}

	// Line 2 of that hunk is "+inserted", the fifth line's worth of new work.
	m.moveTo(lineRowOf(t, m, hunk, 2))
	press(t, m, "shift+down", "c")
	typeText(t, m, "these two")
	press(t, m, "enter")

	got := backend.added[0]
	if got.Origin != store.OriginWorktree {
		t.Errorf("origin = %q, want the working tree's diff", got.Origin)
	}
	if got.Line != 3 || got.EndLine != 4 {
		t.Errorf("comment covers %d-%d, want 3-4 of the file on disk", got.Line, got.EndLine)
	}
}

// What an agent does with a line number is go and edit that line, so the run has
// to reach the paste as well as the store.
func TestTheHandoffNamesTheRunANoteCovers(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Body: "these two", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	press(t, m, "C")
	if len(backend.copied) != 1 {
		t.Fatalf("the review was copied %d times, want once", len(backend.copied))
	}
	if !strings.Contains(backend.copied[0], "alpha.go:3-4") {
		t.Errorf("handoff = %q, want the run named", backend.copied[0])
	}
}

// A run whose code has gone keeps both the numbers it was written on: they are
// the only true thing left to say about it.
func TestAnOutdatedRunSaysWhichLinesItWasWrittenOn(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Body: "these two", Author: store.AuthorUser, Outdated: true},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 0 {
		t.Fatal("the note is not in the document")
	}
	got := m.renderer.Row(m.doc, row, RowState{})
	if !strings.Contains(got, "outdated · was :3-4") {
		t.Errorf("the note reads %q, want the run it was written on", got)
	}
}
