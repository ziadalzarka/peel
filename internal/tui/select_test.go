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

// Marking a removal and the lines that replaced it is reading a replacement, and
// a note on a replacement is about the code that arrived. So the run is numbered
// on the new side however it started, and passes over the removal — the old
// file's numbers are not the numbers this note is counted in.
func TestARunThatReachesTheNewCodeIsNumberedOnTheNewSide(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, removedOne))
	press(t, m, "shift+down", "shift+down", "c")

	// And the editor is under the run, not up on the removal it started from.
	if _, hi := markedRun(t, m); m.doc.DraftRow != hi+1 {
		t.Errorf("the editor is at row %d, want row %d — under the last line of the run",
			m.doc.DraftRow, hi+1)
	}

	typeText(t, m, "this replacement")
	press(t, m, "enter")

	got := backend.added[0]
	if got.Side != store.SideNew || got.Line != 3 || got.EndLine != 4 {
		t.Errorf("comment = %s:%d-%d on %s, want alpha.go:3-4 on the new side",
			got.File, got.Line, got.EndLine, got.Side)
	}
}

// A run that never reaches the code arriving is a note about the code leaving,
// and is numbered against the file before the change.
func TestARunOfCodeLeavingIsNumberedOnTheOldSide(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	// Upwards from the removal, so the run holds it and the blank line above it
	// and nothing that is arriving.
	m.moveTo(lineRowOf(t, m, 0, removedOne))
	press(t, m, "shift+up", "c")
	typeText(t, m, "why did this go?")
	press(t, m, "enter")

	got := backend.added[0]
	if got.Side != store.SideOld || got.Line != 2 || got.EndLine != 3 {
		t.Errorf("comment = %s:%d-%d on %s, want old lines 2-3, the file as it was",
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

// The editor opens under the whole of what the note is about. On the first line
// of the run it would sit between lines the same note covers, reading as a note
// about the one line above it and leaving the rest of the run below an argument
// nobody had made about it yet.
func TestTheEditorOpensBelowTheMarkedRun(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, addedOne))
	press(t, m, "shift+down", "c")

	_, hi := markedRun(t, m)
	if m.doc.DraftRow != hi+1 {
		t.Errorf("the editor is at row %d, want row %d — under the last line of the run",
			m.doc.DraftRow, hi+1)
	}
}

// Where the editor was is where the note lands, or saving one would shuffle the
// diff under the reviewer at the moment they stopped writing.
func TestASavedRunNoteSitsUnderTheLastLineItCovers(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Side: store.SideNew,
			Body: "these two", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	code, _ := codeUnder(t, m.doc, "c1")
	if !strings.Contains(code, "func Two()") {
		t.Errorf("the note is laid out under %q, want the last line of the run it covers", code)
	}
}

// Writing from a note on a run is answering it, so the second note is about the
// same run and opens in the same place. Falling back to the first line would put
// the editor above the note being answered.
func TestANoteWrittenFromARunNoteCoversTheSameRun(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Side: store.SideNew,
			Body: "these two", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 0 {
		t.Fatal("the note is not in the document")
	}
	m.moveTo(row)
	press(t, m, "c")
	typeText(t, m, "and another thing")
	press(t, m, "enter")

	if got := backend.added[0]; got.Line != 3 || got.EndLine != 4 {
		t.Errorf("comment covers %d-%d, want the 3-4 of the note it answers", got.Line, got.EndLine)
	}
}

// A note on a run sits under the last line of it, so the run is the one thing
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

// The tag says which lines a note covers, but only once the reviewer has read
// down to it. The same bar the run wore while it was being marked stays on the
// code afterwards, so how far a note reaches is visible from the code itself.
func TestASavedNoteBarsEveryLineItCovers(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, EndLine: 4, Side: store.SideNew,
			Body: "these two", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	rows := m.diffLines(m.bodyHeight())
	for _, line := range []int{addedOne, addedTwo} {
		got := rows[lineRowOf(t, m, 0, line)-m.top]
		if !strings.HasPrefix(got, "▌") {
			t.Errorf("line %d of the run reads %q, want the bar that says a note covers it", line, got)
		}
	}
	if got := rows[lineRowOf(t, m, 0, lastLine)-m.top]; strings.HasPrefix(got, "▌") {
		t.Errorf("a line outside the run reads %q, want no bar", got)
	}
}

// A note on one line is a run of one, and reads like every other run: the line
// it is about is barred. Without it the shortest note — much the commonest one —
// is the only one whose code says nothing about it.
func TestASavedNoteOnOneLineBarsThatLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew,
			Body: "hello", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	rows := m.diffLines(m.bodyHeight())
	if got := rows[lineRowOf(t, m, 0, addedTwo)-m.top]; !strings.HasPrefix(got, "▌") {
		t.Errorf("the line the note is on reads %q, want the bar", got)
	}
	if got := rows[lineRowOf(t, m, 0, addedOne)-m.top]; strings.HasPrefix(got, "▌") {
		t.Errorf("the line above the note reads %q, want no bar", got)
	}
}

// A note on a removal is written against the old file, so it bars the line that
// went rather than the one that replaced it — which is the whole reason the
// note names a side.
func TestASavedNoteOnARemovalBarsTheRemovedLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideOld,
			Body: "why did this go", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	rows := m.diffLines(m.bodyHeight())
	if got := rows[lineRowOf(t, m, 0, removedOne)-m.top]; !strings.HasPrefix(got, "▌") {
		t.Errorf("the removed line reads %q, want the bar", got)
	}
	if got := rows[lineRowOf(t, m, 0, addedOne)-m.top]; strings.HasPrefix(got, "▌") {
		t.Errorf("the line that replaced it reads %q, want no bar", got)
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
