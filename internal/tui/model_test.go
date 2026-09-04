package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// newModel builds a Model with colour and syntax highlighting off, so View
// output is plain text.
func newModel(t *testing.T, backend *fakeBackend, opts ...Option) *Model {
	t.Helper()
	comments, err := backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	all := append([]Option{WithTheme(Theme{}), WithoutSyntax(), WithSize(100, 30)}, opts...)
	return New(context.Background(), backend, backend.session, comments, all...)
}

// keyMsg turns a key name into the message bubbletea would deliver.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	case "alt+up":
		return tea.KeyMsg{Type: tea.KeyUp, Alt: true}
	case "alt+down":
		return tea.KeyMsg{Type: tea.KeyDown, Alt: true}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

// press delivers key presses and runs whatever commands they produce, so a test
// sees the state the user would see after the async work lands.
func press(t *testing.T, m *Model, names ...string) {
	t.Helper()
	for _, name := range names {
		send(t, m, keyMsg(name))
	}
}

func send(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	_, cmd := m.Update(msg)
	settle(t, m, cmd, 0)
}

// settle runs a command and feeds its message back, up to a few rounds — enough
// for the mutate-then-reload chain without risking a runaway loop in a test.
func settle(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 6 {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return
	case tea.QuitMsg:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			settle(t, m, c, depth+1)
		}
		return
	}
	_, next := m.Update(msg)
	settle(t, m, next, depth+1)
}

// typeText feeds characters into the comment editor.
//
// Commands are dropped rather than run: the editor's only command is a cursor
// blink timer, and settling it would make every keystroke wait on a real clock.
func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestNewStartsOnTheFirstFileHeader(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowFile {
		t.Errorf("cursor is on a %v, want a file header", got)
	}
	if m.doc.FileAt(m.cursor) != 0 {
		t.Errorf("cursor is on file %d, want the first", m.doc.FileAt(m.cursor))
	}
}

func TestNavigationKeysMoveTheCursor(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "j")
	if got := m.doc.Rows[m.cursor].Kind; got != RowHunk {
		t.Fatalf("after j the cursor is on a %v, want a hunk", got)
	}
	press(t, m, "k")
	if got := m.doc.Rows[m.cursor].Kind; got != RowFile {
		t.Fatalf("after k the cursor is on a %v, want a file", got)
	}

	press(t, m, "alt+down")
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Fatalf("after opt+↓ the cursor is on %q, want beta.txt", got)
	}
	press(t, m, "alt+up")
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "alpha.go" {
		t.Fatalf("after opt+↑ the cursor is on %q, want alpha.go", got)
	}

	press(t, m, "G")
	if m.cursor != m.doc.LastStop() {
		t.Errorf("G left the cursor at %d, want %d", m.cursor, m.doc.LastStop())
	}
	press(t, m, "g")
	if m.cursor != m.doc.FirstStop() {
		t.Errorf("g left the cursor at %d, want %d", m.cursor, m.doc.FirstStop())
	}
}

func TestCursorStaysVisibleWhileScrolling(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)), WithSize(100, 12))

	for range 30 {
		press(t, m, "j")
	}
	if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
		t.Errorf("cursor %d is outside the window [%d,%d)", m.cursor, m.top, m.top+m.bodyHeight())
	}
	press(t, m, "ctrl+u", "ctrl+u", "ctrl+u")
	if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
		t.Errorf("after scrolling up the cursor %d is outside [%d,%d)", m.cursor, m.top, m.top+m.bodyHeight())
	}
}

// manyFileSession builds a session with more files than any test window can
// show, so the file pane has something to scroll through.
func manyFileSession(t *testing.T, n int) *app.Session {
	t.Helper()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "diff --git a/f%02d.txt b/f%02d.txt\n"+
			"index 1111111..2222222 100644\n--- a/f%02d.txt\n+++ b/f%02d.txt\n"+
			"@@ -1,2 +1,2 @@\n keep\n-old%d\n+new%d\n", i, i, i, i, i, i)
	}
	return newSession(t, b.String())
}

// wheelMsg is one notch of the wheel at a column.
func wheelMsg(button tea.MouseButton, x int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: 1, Action: tea.MouseActionPress, Button: button}
}

// The arrows move the cursor a line at a time, over the diff body as well as the
// headers, so there is nothing to enter before commenting on or staging one line.
func TestArrowKeysMoveTheCursorOneLineAtATime(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)), WithSize(100, 12))

	start := m.cursor
	for want := 1; want <= 3; want++ {
		press(t, m, "down")
		if m.cursor != start+want {
			t.Fatalf("after %d presses of down the cursor is at row %d, want %d", want, m.cursor, start+want)
		}
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowLine {
		t.Errorf("the cursor is on a %v, want a diff line", got)
	}
	press(t, m, "up")
	if m.cursor != start+2 {
		t.Errorf("up left the cursor at row %d, want %d", m.cursor, start+2)
	}
}

// The brackets move up to ten lines and stop at the header of the file below
// rather than counting on past it, so a file is never entered partway down with
// its name having gone by on the way.
func TestBracketsStopAtTheFileTheyReach(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))

	start := m.cursor
	press(t, m, "]")
	leapt := m.cursor

	// Ten presses of the arrow from the same row, counted out here rather than
	// taken from the constant, so what the jump gave up is what is being checked.
	stepped := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))
	for range 10 {
		press(t, stepped, "down")
	}
	if leapt >= stepped.cursor {
		t.Fatalf("] left the cursor at row %d, at or past the %d ten presses of down reach — "+
			"want it stopped short at the file in between", leapt, stepped.cursor)
	}
	if leapt != m.doc.RowOfFile(1) {
		t.Fatalf("] left the cursor at row %d, want the second file's header at %d",
			leapt, m.doc.RowOfFile(1))
	}
	if !m.doc.IsStop(leapt) {
		t.Errorf("the cursor landed on row %d, which it cannot rest on", leapt)
	}
	if leapt < m.top || leapt >= m.top+m.bodyHeight() {
		t.Errorf("the cursor %d is outside the window [%d,%d) — the window did not follow the jump",
			leapt, m.top, m.top+m.bodyHeight())
	}

	// And the next press carries on past it, so the jump is split where something
	// happened rather than blocked there.
	press(t, m, "]")
	if m.cursor <= leapt {
		t.Errorf("a second ] left the cursor at row %d, want it carried on past the header at %d",
			m.cursor, leapt)
	}

	press(t, m, "[")
	press(t, m, "[")
	if m.cursor != start {
		t.Errorf("[ left the cursor at row %d, want the row it started on, %d", m.cursor, start)
	}
	if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
		t.Errorf("after jumping back the cursor %d is outside the window [%d,%d)",
			m.cursor, m.top, m.top+m.bodyHeight())
	}
}

// A file header stops one press, not the key: holding the bracket walks through
// every file of the review and stops at the bottom of it.
func TestBracketsStopAtTheEndsOfTheDiff(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))

	for range 50 {
		press(t, m, "]")
	}
	if m.cursor != m.doc.LastStop() {
		t.Errorf("] held down left the cursor at %d, want the last row %d", m.cursor, m.doc.LastStop())
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "f19.txt" {
		t.Errorf("the cursor ended in %q, want the last file", got)
	}

	for range 50 {
		press(t, m, "[")
	}
	if m.cursor != m.doc.FirstStop() {
		t.Errorf("[ held down left the cursor at %d, want the first row %d", m.cursor, m.doc.FirstStop())
	}
}

// Option and an arrow are the file jump the brackets used to be: a whole file at
// a time, landing on its header with the window opened on it, and stopping at the
// first and last file rather than running past them.
func TestOptionArrowsMoveAWholeFileAtATime(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))

	// From inside a file, back up to its own header first — the file being left is
	// the one the cursor is in, not the one below it.
	m.moveTo(m.doc.Leap(m.doc.RowOfFile(3), 2))
	press(t, m, "alt+up")
	if m.cursor != m.doc.RowOfFile(3) {
		t.Fatalf("opt+↑ from inside file 3 left the cursor at %d, want its header at %d",
			m.cursor, m.doc.RowOfFile(3))
	}

	press(t, m, "alt+down")
	if m.cursor != m.doc.RowOfFile(4) || m.top != m.cursor {
		t.Errorf("opt+↓ left the cursor at %d and the window at %d, want both on file 4's header (%d)",
			m.cursor, m.top, m.doc.RowOfFile(4))
	}

	for range 50 {
		press(t, m, "alt+down")
	}
	if got := m.doc.FileAt(m.cursor); got != 19 {
		t.Errorf("opt+↓ held down ended in file %d, want the last one", got)
	}
	for range 50 {
		press(t, m, "alt+up")
	}
	if got := m.doc.FileAt(m.cursor); got != 0 {
		t.Errorf("opt+↑ held down ended in file %d, want the first one", got)
	}
}

// cmd+↑ and cmd+↓ are the ends of the diff, the same rows g and G reach. The
// terminal has to say which modifier was held for them to arrive at all, and
// bubbletea has no name for the sequence it sends when it does, so the raw bytes
// are what peel reads.
func TestCmdArrowsReachTheEndsOfTheDiff(t *testing.T) {
	// Plain cmd, cmd with another modifier along with it, the event type kitty
	// appends when it has been asked to report one, and the meta bit a terminal
	// uses when it is configured to send cmd as meta instead.
	for _, seq := range []string{"\x1b[1;9B", "\x1b[1;10B", "\x1b[1;13B", "\x1b[1;9:1B", "\x1b[1;9:3B", "\x1b[1;33B"} {
		m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))

		send(t, m, csiSequenceMsg(seq))
		if m.cursor != m.doc.LastStop() {
			t.Errorf("%q left the cursor at %d, want the last row %d", seq, m.cursor, m.doc.LastStop())
		}

		send(t, m, csiSequenceMsg(strings.Replace(seq, "B", "A", 1)))
		if m.cursor != m.doc.FirstStop() {
			t.Errorf("the ↑ of %q left the cursor at %d, want the first row %d", seq, m.cursor, m.doc.FirstStop())
		}
	}

	// They are read as the home and end peel already binds, so a terminal that
	// sends those for cmd and an arrow — several are configured to — lands on the
	// same rows without any of the above.
	m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))
	press(t, m, "end")
	if m.cursor != m.doc.LastStop() {
		t.Errorf("end left the cursor at %d, want the last row %d", m.cursor, m.doc.LastStop())
	}
	press(t, m, "home")
	if m.cursor != m.doc.FirstStop() {
		t.Errorf("home left the cursor at %d, want the first row %d", m.cursor, m.doc.FirstStop())
	}
}

// An arrow a terminal reports another modifier for is not cmd, and is left
// alone: ctrl+↓ is not a key peel binds, and reading it as one would move the
// cursor on a press meant for something else. Nor is cmd sideways: `0` and `$`
// are what reach the ends of a line, and the diff does not scroll to them.
func TestModifiedArrowsWithoutCmdUpOrDownAreLeftAlone(t *testing.T) {
	for _, seq := range []string{
		"\x1b[1;5B", "\x1b[1;2B", "\x1b[1;3B", "\x1b[1;7B", "\x1b[1;8B",
		"\x1b[1;9C", "\x1b[1;9D",
		"\x1b[3~", "\x1b[1;B", "\x1b[1;99999999999999999999B",
	} {
		m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))

		start := m.cursor
		send(t, m, csiSequenceMsg(seq))
		if m.cursor != start {
			t.Errorf("%q moved the cursor to %d, want it left at %d", seq, m.cursor, start)
		}
	}
}

// The wheel still scrolls the diff, and drags the line cursor along rather than
// leaving it addressing a row that has left the window.
func TestWheelDragsTheCursorWithTheWindow(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))

	for range 6 {
		send(t, m, wheelMsg(tea.MouseButtonWheelDown, m.filePaneWidth()+5))
	}
	if m.top == 0 {
		t.Fatal("the wheel did not scroll the diff")
	}
	if m.cursor < m.top || m.cursor >= m.top+m.bodyHeight() {
		t.Errorf("the cursor %d is outside the window [%d,%d)", m.cursor, m.top, m.top+m.bodyHeight())
	}
}

func TestFilePaneScrollsWithoutMovingTheDiff(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))
	if m.filePaneWidth() == 0 {
		t.Fatal("the file pane is not showing")
	}

	top := m.top
	press(t, m, "}", "}")
	if m.fileTop != 2 {
		t.Errorf("} left the file pane at %d, want 2", m.fileTop)
	}
	if m.top != top {
		t.Errorf("} moved the diff window to %d, want it left at %d", m.top, top)
	}

	press(t, m, "{")
	if m.fileTop != 1 {
		t.Errorf("{ left the file pane at %d, want 1", m.fileTop)
	}
}

// b hands the file list's width to the diff and takes it back, without moving
// the reviewer off the row they were reading.
func TestBHidesAndShowsTheFilePane(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))
	if m.filePaneWidth() == 0 {
		t.Fatal("the file pane is not showing")
	}

	press(t, m, "j", "j")
	at := m.doc.Rows[m.cursor]

	press(t, m, "b")
	if m.filePaneWidth() != 0 {
		t.Errorf("b left the file pane %d wide, want it hidden", m.filePaneWidth())
	}
	if m.diffWidth() != m.width {
		t.Errorf("with the pane hidden the diff is %d wide, want the full %d", m.diffWidth(), m.width)
	}
	if got := m.doc.Rows[m.cursor]; got != at {
		t.Errorf("b left the cursor on %+v, want it on %+v", got, at)
	}
	body := strings.Split(m.View(), "\n")[headerHeight : headerHeight+m.bodyHeight()]
	for i, line := range body {
		if w := ansi.StringWidth(line); w != m.width {
			t.Fatalf("with the pane hidden body line %d is %d cells, want %d: %q", i, w, m.width, line)
		}
	}

	press(t, m, "b")
	if m.filePaneWidth() == 0 {
		t.Error("b again did not bring the file pane back")
	}
	if got := m.doc.Rows[m.cursor]; got != at {
		t.Errorf("showing the pane again left the cursor on %+v, want it on %+v", got, at)
	}
}

func TestWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))
	pane := m.filePaneWidth()
	if pane == 0 {
		t.Fatal("the file pane is not showing")
	}

	send(t, m, wheelMsg(tea.MouseButtonWheelDown, 1))
	if m.fileTop != wheelLines {
		t.Errorf("a wheel notch over the pane left fileTop at %d, want %d", m.fileTop, wheelLines)
	}
	if m.top != 0 {
		t.Errorf("a wheel notch over the pane moved the diff to row %d, want it left at 0", m.top)
	}

	send(t, m, wheelMsg(tea.MouseButtonWheelDown, pane+5))
	if m.top != wheelLines {
		t.Errorf("a wheel notch over the diff left the window at row %d, want %d", m.top, wheelLines)
	}
	send(t, m, wheelMsg(tea.MouseButtonWheelUp, pane+5))
	if m.top != 0 {
		t.Errorf("wheeling back up left the window at row %d, want 0", m.top)
	}
}

// TestThePaneMarksTheFileTheWindowOpensOn covers the case scrolling creates: the
// cursor is dragged ahead to a stop in a later file while the window is still
// showing the file before it. The pane names the file being read, not the one
// the cursor happens to sit in.
func TestThePaneMarksTheFileTheWindowOpensOn(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))

	second := m.doc.RowOfFile(1)
	for m.top < second-2 {
		press(t, m, "down")
	}
	if m.doc.FileAt(m.cursor) == 0 {
		t.Fatalf("the cursor is still in file 0 at row %d, so the test proves nothing", m.cursor)
	}
	if got := m.markedFile(); got != 0 {
		t.Errorf("the window opens on file 0 but the pane marks file %d", got)
	}

	press(t, m, "down")
	if m.doc.Rows[m.top].Kind != RowBlank {
		t.Fatalf("row %d is a %v, want the blank row between the files", m.top, m.doc.Rows[m.top].Kind)
	}
	if got := m.markedFile(); got != 1 {
		t.Errorf("a window opening on the blank row between files marks file %d, want the file below it", got)
	}
}

func TestFileJumpsOpenTheWindowOnTheFile(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 20)), WithSize(100, 12))

	press(t, m, "alt+down", "alt+down")
	if m.cursor != m.doc.RowOfFile(2) || m.top != m.cursor {
		t.Errorf("after two opt+↓ the window starts at row %d and the cursor is at %d, want both at file 2's header (%d)",
			m.top, m.cursor, m.doc.RowOfFile(2))
	}
	if got := m.markedFile(); got != 2 {
		t.Errorf("after two opt+↓ the pane marks file %d, want 2", got)
	}

	press(t, m, "alt+up")
	if got := m.markedFile(); got != 1 {
		t.Errorf("after opt+↑ the pane marks file %d, want 1", got)
	}
}

// The file mode acts on the whole file from anywhere in it, so it stages from a
// hunk header too — `S` is what puts the press on the hunk instead.
func TestStageOnAHunkStagesItsFileAndReloads(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("StageFile calls = %v, want [alpha.go]", got)
	}
	if backend.reloads != 1 {
		t.Errorf("reloads = %d, want 1 after staging", backend.reloads)
	}
	if !strings.Contains(m.status, "staged alpha.go") {
		t.Errorf("status = %q, want it to name what was staged", m.status)
	}
}

func TestStageOnAFileHeaderStagesTheWholeFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("StageFile calls = %v, want [alpha.go]", got)
	}
}

func TestStageOnADiffLineStagesTheWholeFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	// A context line: the file mode acts on the file from anywhere inside it, so
	// even an unchanged row stages the whole of it.
	m.moveTo(lineRowOf(t, m, 0, 0))
	press(t, m, "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("StageFile calls = %v, want [alpha.go]", got)
	}
}

// A staged file folds away, since it has been dealt with — and `space` opens it
// again, because a staged file is still worth reading.
func TestStagingAFileFoldsItAndSpaceOpensItAgain(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)
	m := newModel(t, backend)

	press(t, m, "s")

	if !m.doc.Files[0].Collapsed {
		t.Fatal("the staged file did not fold away")
	}
	if m.doc.Files[1].Collapsed {
		t.Error("staging alpha.go folded beta.txt too")
	}

	press(t, m, "alt+up", "space")
	if m.doc.Files[0].Collapsed {
		t.Error("space did not open the staged file again")
	}
}

func TestStageAnAlreadyStagedFileDoesNothing(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries[:1]))
	m := newModel(t, backend)

	press(t, m, "j", "s")

	if len(backend.stagedFiles) != 0 {
		t.Fatalf("staging an already-staged file called the backend: %v", backend.stagedFiles)
	}
	if !strings.Contains(m.status, "already staged") {
		t.Errorf("status = %q, want it to say the file is already staged", m.status)
	}
	// `s` still means "done with this one", so it folds without calling git.
	if !m.doc.Files[0].Collapsed {
		t.Error("s on an already-staged file left it open")
	}
}

// Unstaging puts a file back among the things to review, so it opens again.
func TestUnstagingAFileOpensIt(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries[:1]))
	m := newModel(t, backend)

	press(t, m, "space")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("tab did not fold the file")
	}

	press(t, m, "u")

	if got := backend.unstagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("UnstageFile calls = %v, want [alpha.go]", got)
	}
	if m.doc.Files[0].Collapsed {
		t.Error("the unstaged file stayed folded away")
	}
}

func TestUnstageOnAFileWithNothingStagedDoesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "u")

	if len(backend.unstagedFiles) != 0 {
		t.Fatalf("UnstageFile called with %v", backend.unstagedFiles)
	}
	if !strings.Contains(m.status, "nothing staged") {
		t.Errorf("status = %q", m.status)
	}
}

func TestStageAllFoldsEveryFileAndUnstageAllOpensThem(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "a")
	if backend.stageAll != 1 {
		t.Errorf("StageAll called %d times, want 1", backend.stageAll)
	}
	for i, f := range m.doc.Files {
		if !f.Collapsed {
			t.Errorf("file %d (%s) is still open after staging everything", i, f.Entry.Path)
		}
	}

	press(t, m, "U")
	if backend.unstageAll != 1 {
		t.Errorf("UnstageAll called %d times, want 1", backend.unstageAll)
	}
	for i, f := range m.doc.Files {
		if f.Collapsed {
			t.Errorf("file %d (%s) is still folded after unstaging everything", i, f.Entry.Path)
		}
	}
}

func TestBackendFailureIsShownAndNothingIsReloaded(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.opErr = errors.New("patch does not apply")
	m := newModel(t, backend)

	press(t, m, "j", "s")

	if m.err == nil || !strings.Contains(m.err.Error(), "patch does not apply") {
		t.Fatalf("err = %v, want the backend's failure", m.err)
	}
	if backend.reloads != 0 {
		t.Errorf("reloads = %d, want 0 after a failed stage", backend.reloads)
	}
	if m.busy != "" {
		t.Errorf("busy = %q, want it cleared after the failure", m.busy)
	}
	if !strings.Contains(m.View(), "patch does not apply") {
		t.Error("the failure is not visible in the view")
	}
	if m.doc.Files[0].Collapsed {
		t.Error("a failed stage folded the file away; it still has to be reviewed")
	}

	// The next key press clears it, so an old error cannot linger.
	press(t, m, "k")
	if m.err != nil {
		t.Errorf("err = %v, want it cleared by the next key", m.err)
	}
}

// Staging is how the reviewer moves through the diff: the file folds away and
// the cursor lands on the next one to read, so a pass is `s` after `s`.
func TestStagingAdvancesToTheNextFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))

	// After staging, alpha.go is fully staged and only beta.txt is left.
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "j", "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Fatalf("cursor is on %q, want the next file to review, beta.txt", got)
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowFile {
		t.Errorf("cursor is on a %v, want the next file read from its header", got)
	}
}

// Folding a file away pulls what is below it up, so the file the pass carries on
// with is usually already on screen. The window stays where it is when it is:
// scrolling it to put a file the reviewer can already see at the first row would
// throw away the diff around it for nothing.
func TestStagingLeavesTheWindowOnANextFileAlreadyOnScreen(t *testing.T) {
	backend := newFakeBackend(manyFileSession(t, 20))
	staged := manyFileSession(t, 20)
	staged.Files[0].Staged, staged.Files[0].Unstaged = staged.Files[0].Unstaged, nil
	backend.nextSession = staged

	m := newModel(t, backend, WithSize(100, 12))
	press(t, m, "s")

	want := m.doc.RowOfFile(1)
	if m.cursor != want {
		t.Fatalf("after staging the cursor is at row %d, want the next file's header (%d)", m.cursor, want)
	}
	if want >= m.bodyHeight() {
		t.Fatalf("the next file's header at row %d is off a window %d rows tall, so this is not the case being tested",
			want, m.bodyHeight())
	}
	if m.top != 0 {
		t.Errorf("the window moved to row %d for a file already on screen, want it left at 0", m.top)
	}
}

// When the next file is off screen the window moves only as far as it has to:
// far enough to read the file's opening lines, and no further. The file lands at
// the bottom of the window with what came before it still above, the way it would
// have arrived had the reviewer scrolled there by hand.
func TestFoldingScrollsFarEnoughToOpenTheNextFile(t *testing.T) {
	// Everything between the first file and file 14 has been dealt with already —
	// staged, and folded away by the staging — so the file the pass moves on to
	// is far below the window.
	session := manyFileSession(t, 20)
	for i := 1; i < 14; i++ {
		session.Files[i].Staged, session.Files[i].Unstaged = session.Files[i].Unstaged, nil
	}
	m := newModel(t, newFakeBackend(session), WithSize(100, 30))
	for i := 1; i < 14; i++ {
		m.collapsed[m.doc.Files[i].Entry.Path] = true
	}
	m.rebuild()
	press(t, m, " ")

	want := m.doc.RowOfFile(14)
	if m.cursor != want {
		t.Fatalf("after folding the cursor is at row %d, want file 14's header (%d)", m.cursor, want)
	}
	if m.top >= want {
		t.Fatalf("the window starts at row %d, want it above file 14's header (%d) with the files before it still in view",
			m.top, want)
	}
	if shown := m.top + m.bodyHeight() - want; shown != revealLines {
		t.Errorf("the window shows %d rows of file 14, want its first %d", shown, revealLines)
	}
}

// The next file is the next one with work still out of the index. A file staged
// elsewhere — by an agent, or by a git add outside peel — has nothing left to
// decide about, so the pass goes over it whether or not its diff is on screen.
func TestStagingPassesOverAFileAlreadyInTheIndex(t *testing.T) {
	entries := parseFiles(t, threeFileDiff)
	entries[1].Staged, entries[1].Unstaged = entries[1].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries))

	staged := parseFiles(t, threeFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	staged[1].Staged, staged[1].Unstaged = staged[1].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "gamma.md" {
		t.Errorf("cursor is on %q, want gamma.md — beta.txt is in the index already", got)
	}
}

// A fold is where a file is on screen, not whether it is done: a file folded
// away with work still out of the index is a stop, and `space` on it opens the
// work the fold was hiding.
func TestStagingStopsOnAFoldedFileWithWorkLeftInIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeFileDiff))
	backend.folded = []string{"beta.txt"}

	staged := parseFiles(t, threeFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("cursor is on %q, want beta.txt — it is folded away, but nothing of it is staged", got)
	}
}

// With nothing left below, there is nowhere to carry the pass on to: the cursor
// stays on the file just dealt with rather than landing on a header with
// nothing under it. A file left over above does not pull the cursor back.
func TestStagingStaysPutWhenEverythingBelowIsDone(t *testing.T) {
	entries := parseFiles(t, threeFileDiff)
	entries[2].Staged, entries[2].Unstaged = entries[2].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries))

	staged := parseFiles(t, threeFileDiff)
	staged[1].Staged, staged[1].Unstaged = staged[1].Unstaged, nil
	staged[2].Staged, staged[2].Unstaged = staged[2].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "alt+down", "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("cursor is on %q, want it to stay on beta.txt — gamma.md is staged and alpha.go is behind the cursor", got)
	}
}

// Staging the last file has nowhere to advance to, and the file just folded is a
// better place to stay than the top of a diff already reviewed.
func TestStagingTheLastFileStaysOnIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))

	staged := parseFiles(t, twoFileDiff)
	staged[1].Staged, staged[1].Unstaged = staged[1].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "alt+down", "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("cursor is on %q, want it to stay on beta.txt", got)
	}
}

// `o` hands the file the cursor is in to the desktop, from anywhere inside it,
// and leaves the review where it was.
func TestOpenSendsTheFileToTheDesktop(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "o")

	if want := []string{"alpha.go"}; len(backend.opened) != 1 || backend.opened[0] != want[0] {
		t.Fatalf("opened %v, want %v", backend.opened, want)
	}
	if m.doc.Rows[m.cursor].Kind != RowHunk {
		t.Errorf("opening the file moved the cursor off the hunk it was on")
	}
	if m.busy != "" || !strings.Contains(m.status, "alpha.go") {
		t.Errorf("busy = %q, status = %q, want the open reported and done", m.busy, m.status)
	}
}

func TestOpenReportsAFailureToOpen(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.opErr = errors.New("no application knows what to do with it")
	m := newModel(t, backend)

	press(t, m, "o")

	if m.err == nil {
		t.Fatal("a failed open reported no error")
	}
	if m.busy != "" {
		t.Errorf("busy = %q, want it cleared", m.busy)
	}
}

func TestStagingKeepsTheWalkthroughItGuided(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "w")
	if !m.walkLoaded {
		t.Fatal("walkthrough did not load")
	}
	press(t, m, "a")

	if len(m.doc.Steps) != 2 {
		t.Errorf("staging left %d notes in the diff, want the 2 the reviewer was reading", len(m.doc.Steps))
	}
	if m.walkStale {
		t.Error("staging marked the walkthrough stale, but the code it describes did not change")
	}
	if backend.walkCalls != 1 {
		t.Errorf("walkthrough calls = %d, want no provider call for a stage", backend.walkCalls)
	}
}

func TestChangedCodeMarksTheWalkthroughStale(t *testing.T) {
	before := newSession(t, twoFileDiff)
	before.DiffText = twoFileDiff
	backend := newFakeBackend(before)
	m := newModel(t, backend)

	press(t, m, "w")
	after := newSession(t, twoFileDiff)
	after.DiffText = twoFileDiff + "\n// an agent kept working\n"
	backend.nextSession = after
	press(t, m, "r")

	if !m.walkStale {
		t.Error("the code moved on under the narrative and nothing said so")
	}
	if len(m.doc.Steps) != 2 {
		t.Errorf("the stale walkthrough was dropped: %d notes left", len(m.doc.Steps))
	}
	if !strings.Contains(m.View(), "stale") {
		t.Error("the header does not mark the walkthrough stale")
	}
}

// Folding a file means the same thing staging one does — done with it — so the
// cursor moves on. Opening one again leaves the cursor on it.
func TestSpaceFoldsAFileAndMovesOn(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "space")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("space did not collapse alpha.go")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("folding alpha.go left the cursor on %q, want it moved on to beta.txt", got)
	}

	press(t, m, "alt+up", "space")
	if m.doc.Files[0].Collapsed {
		t.Error("space did not expand alpha.go again")
	}
	if m.doc.Rows[m.cursor].Kind != RowFile || m.doc.FileAt(m.cursor) != 0 {
		t.Error("opening alpha.go again moved the cursor off it")
	}
}

// A pass rarely finishes in one sitting, so what has been folded away outlives
// the process: the review opens again with the same files out of the way.
func TestFoldsAreRememberedAcrossSessions(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "space")
	if got := backend.folded; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("saved folds %v, want alpha.go alone", got)
	}

	again := newModel(t, backend)
	if !again.doc.Files[0].Collapsed {
		t.Error("alpha.go opened unfolded, want the fold it was left with")
	}
	if again.doc.Files[1].Collapsed {
		t.Error("beta.txt opened folded, and it was never folded")
	}

	press(t, again, "space")
	if len(backend.folded) != 0 {
		t.Errorf("saved folds %v after opening the file again, want none", backend.folded)
	}
}

// Follow mode reloads on a timer, and a reload that folds nothing must not
// rewrite the folds every few seconds.
func TestReloadWithoutAFoldChangeWritesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "space")
	saves := backend.foldSaves

	press(t, m, "r", "r")

	if backend.foldSaves != saves {
		t.Errorf("reloading wrote the folds %d more times, want none", backend.foldSaves-saves)
	}
}

// Staging folds a file, and that fold is a record of the pass like any other.
func TestStagingSavesTheFoldItMakes(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)
	m := newModel(t, backend)

	press(t, m, "s")

	if got := backend.folded; len(got) != 1 || got[0] != "alpha.go" {
		t.Errorf("saved folds %v, want alpha.go", got)
	}
}

// A file whose change has been committed away is done with. Remembering its fold
// would hide the next change to it, which is a new thing to read.
func TestFoldsOfFilesNoLongerInTheDiffAreDropped(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.folded = []string{"alpha.go", "gone.go"}
	m := newModel(t, backend)

	if _, ok := m.collapsed["gone.go"]; ok {
		t.Error("a file that is not in the diff was restored as folded")
	}
	if !m.doc.Files[0].Collapsed {
		t.Fatal("alpha.go opened unfolded, want the fold it was left with")
	}

	press(t, m, "alt+down", "space")

	want := []string{"alpha.go", "beta.txt"}
	if got := backend.folded; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("saved folds %v, want %v — gone.go should have been dropped", got, want)
	}
}

// The last file has nothing to move on to, so folding it stays on it.
func TestFoldingTheLastFileStaysOnIt(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "alt+down", "space")

	if !m.doc.Files[1].Collapsed {
		t.Fatal("space did not collapse beta.txt")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("cursor is on %q, want it to stay on beta.txt", got)
	}
}

// Folding moves on the way staging does, over the files whose work is in the
// index and on to the next one whose is not — folded or not.
func TestFoldingMovesOnByTheIndexAndNotTheFold(t *testing.T) {
	entries := parseFiles(t, threeFileDiff)
	entries[1].Staged, entries[1].Unstaged = entries[1].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries))
	backend.folded = []string{"gamma.md"}
	m := newModel(t, backend)

	press(t, m, "space")

	if !m.doc.Files[0].Collapsed {
		t.Fatal("space did not collapse alpha.go")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "gamma.md" {
		t.Errorf("cursor is on %q, want gamma.md — beta.txt is staged, and gamma.md is folded with its work still out", got)
	}
}

// Where the cursor goes after a file has been dealt with is the reviewer's, not
// peel's: `peel.afterStage` set to next-open puts the pass back on the fold, so
// a file staged outside peel is a stop again because its diff has still to be
// read here.
func TestAfterStageNextOpenMovesByTheFold(t *testing.T) {
	entries := parseFiles(t, threeFileDiff)
	entries[1].Staged, entries[1].Unstaged = entries[1].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries))

	staged := parseFiles(t, threeFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	staged[1].Staged, staged[1].Unstaged = staged[1].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend, WithMoves(app.Moves{AfterStage: app.MoveOpen}))
	press(t, m, "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("cursor is on %q, want beta.txt — under next-open a staged file left open is a stop", got)
	}
}

// Set to stay, the cursor does not move at all: the file folds away under it and
// the reviewer decides where to go next.
func TestAfterStageStayLeavesTheCursorOnTheFileStaged(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend, WithMoves(app.Moves{AfterStage: app.MoveStay}))
	press(t, m, "s")

	if !m.doc.Files[0].Collapsed {
		t.Fatal("s did not fold alpha.go away")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "alpha.go" {
		t.Errorf("cursor is on %q, want it left on alpha.go", got)
	}
}

// The two settings are two settings: staging can be told to stay while folding
// goes on carrying the pass down the diff.
func TestFoldingKeepsItsOwnSettingWhenStagingStays(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend, WithMoves(app.Moves{AfterStage: app.MoveStay, AfterFold: app.MoveUnstaged}))

	press(t, m, "space")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("folding left the cursor on %q, want beta.txt — afterFold was never set to stay", got)
	}
}

// A pull request has no index to read the default against, so there the fold is
// what the pass goes by: a file already folded away has been read, and one still
// open has not.
func TestAPassThatCannotStageMovesByTheFold(t *testing.T) {
	backend := newFakeBackend(readOnlySession(t, threeFileDiff))
	backend.folded = []string{"beta.txt"}
	m := newModel(t, backend)

	press(t, m, "space")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "gamma.md" {
		t.Errorf("cursor is on %q, want gamma.md — beta.txt is folded away and nothing here can be staged", got)
	}
}

func TestBackslashTogglesLayout(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, `\`)
	if m.layout != LayoutSplit || m.doc.Layout != LayoutSplit {
		t.Fatalf("layout = %v, document = %v, want split", m.layout, m.doc.Layout)
	}
	press(t, m, `\`)
	if m.layout != LayoutUnified || m.doc.Layout != LayoutUnified {
		t.Fatalf("layout = %v, document = %v, want unified", m.layout, m.doc.Layout)
	}
}

func TestCommentOnAHunkAnchorsToTheFirstAddedLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	if m.mode != modeComment {
		t.Fatalf("mode = %v, want comment", m.mode)
	}
	typeText(t, m, "needs a test")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	got := backend.added[0]
	if got.File != "alpha.go" || got.Line != 3 || got.Side != store.SideNew {
		t.Errorf("comment anchored at %s:%d (%s), want alpha.go:3 on the new side", got.File, got.Line, got.Side)
	}
	if got.Body != "needs a test" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Author != store.AuthorUser {
		t.Errorf("author = %q, want user", got.Author)
	}
	if got.Hunk != "alpha.go:@-1,4+1,5" {
		t.Errorf("hunk = %q, want the hunk it was written against", got.Hunk)
	}
	if m.mode != modeBrowse {
		t.Errorf("mode = %v after saving, want browse", m.mode)
	}
}

func TestCommentOnAFileHeaderIsFileLevel(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "c")
	typeText(t, m, "rename this file")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 0 || got.File != "alpha.go" {
		t.Errorf("comment = %s:%d, want a file-level comment on alpha.go", got.File, got.Line)
	}
}

func TestTheCommentEditorOpensInTheDiffAtTheAnchor(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	row := lineRowOf(t, m, 0, 3)
	m.moveTo(row)
	press(t, m, "c")

	if m.doc.DraftRow != m.cursor+1 {
		t.Errorf("editor starts at row %d, want the row after the line it comments on (%d)", m.doc.DraftRow, m.cursor+1)
	}
	if m.doc.Rows[m.cursor].Kind != RowLine {
		t.Errorf("the cursor moved to a %v, want to stay on the line being commented on", m.doc.Rows[m.cursor].Kind)
	}
	if got := m.View(); !strings.Contains(ansi.Strip(got), "func Two() int") {
		t.Errorf("the editor took the diff off screen:\n%s", got)
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Write a review comment") {
		t.Errorf("the editor is not on screen:\n%s", got)
	}
}

// The editor stands where the note will, so in the split layout it opens in the
// half the note is going to hang under and is sized to it.
func TestTheCommentEditorOpensInTheHalfTheNoteWillHangUnder(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, `\`, "j", "c")
	typeText(t, m, "about the code arriving")
	if m.doc.DraftRow < 0 {
		t.Fatal("the editor was not laid out")
	}

	got := ansi.Strip(m.renderer.Row(m.doc, m.doc.DraftRow, RowState{Draft: m.draftLines()[0]}))
	if noteColumn(t, got) != "new" {
		t.Errorf("the editor opened on the old side: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("the editor was sized to the pane and cut off by its half: %q", got)
	}
}

func TestTheCommentEditorGrowsWithWhatIsWritten(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "j", "c")
	opened := m.input.Height()
	for range opened + 2 {
		press(t, m, "alt+enter")
	}

	if m.input.Height() <= opened {
		t.Fatalf("editor height stayed at %d after writing past it", m.input.Height())
	}
	if got := draftRows(m.doc); got != m.input.Height() {
		t.Errorf("document reserves %d rows for a %d row editor", got, m.input.Height())
	}
}

func TestEnterSavesAndAltEnterWritesAnotherLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	typeText(t, m, "first")
	press(t, m, "alt+enter")
	typeText(t, m, "second")
	if m.mode != modeComment {
		t.Fatal("alt+enter saved the comment instead of writing another line")
	}

	press(t, m, "enter")
	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0].Body; got != "first\nsecond" {
		t.Errorf("body = %q, want both lines", got)
	}
}

// csiSequenceMsg stands in for the message bubbletea hands on when it reads a
// sequence it has no key for. Its own type is unexported, and what matters is
// that the raw bytes arrive on a message whose type peel cannot name.
type csiSequenceMsg []byte

func TestShiftEnterWritesAnotherLine(t *testing.T) {
	for _, seq := range []string{"\x1b[13;2u", "\x1b[27;2;13~"} {
		backend := newFakeBackend(newSession(t, twoFileDiff))
		m := newModel(t, backend)

		press(t, m, "j", "c")
		typeText(t, m, "first")
		send(t, m, csiSequenceMsg(seq))
		typeText(t, m, "second")
		if m.mode != modeComment {
			t.Fatalf("%q saved the comment instead of writing another line", seq)
		}

		press(t, m, "enter")
		if len(backend.added) != 1 {
			t.Fatalf("AddComment called %d times, want 1", len(backend.added))
		}
		if got := backend.added[0].Body; got != "first\nsecond" {
			t.Errorf("%q: body = %q, want both lines", seq, got)
		}
	}
}

func TestCommentingLeavesTheCursorWhereItWasWritten(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, 3))
	ref, line, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatal("the cursor is not on a diff line")
	}

	press(t, m, "c")
	typeText(t, m, "needs a test")
	press(t, m, "enter")

	if draftRows(m.doc) != 0 {
		t.Error("the editor is still in the document after saving")
	}
	gotRef, gotLine, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatalf("the cursor landed on a %v, want the line the comment was written on", m.doc.Rows[m.cursor].Kind)
	}
	if gotRef.ID != ref.ID || gotLine != line {
		t.Errorf("cursor is on %v line %d, want %v line %d", gotRef.ID, gotLine, ref.ID, line)
	}
}

func TestEscapeCancelsACommentAndLeavesTheCursorAlone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j")
	before := m.cursor
	press(t, m, "c")
	typeText(t, m, "never mind")
	press(t, m, "esc")

	if len(backend.added) != 0 {
		t.Fatalf("a cancelled comment was saved: %+v", backend.added)
	}
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if draftRows(m.doc) != 0 {
		t.Error("the editor is still in the document after cancelling")
	}
	if m.cursor != before {
		t.Errorf("cursor moved from row %d to %d", before, m.cursor)
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q", m.status)
	}
}

// A comment long enough to overrun the pane used to be cut off with an
// ellipsis, which lost most of what an agent had written about a line.
func TestALongCommentIsWrappedOntoTheRowsBelowIt(t *testing.T) {
	long := strings.Repeat("this note runs on and on about the line it was left on. ", 4)
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew, Body: long, Author: store.AuthorAgent},
	}
	m := newModel(t, backend)

	var rows []string
	head := m.doc.RowOfComment("c1")
	for i := head; i < len(m.doc.Rows) && m.doc.Rows[i].Kind == RowComment; i++ {
		row := ansi.Strip(m.renderer.Row(m.doc, i, RowState{}))
		if strings.Contains(row, "…") {
			t.Errorf("row %d was truncated: %q", i-head, row)
		}
		rows = append(rows, strings.ReplaceAll(row, "┃", ""))
	}

	if got := strings.Join(strings.Fields(strings.Join(rows, " ")), " "); got != "agent: "+strings.TrimSpace(long) {
		t.Errorf("the comment reads as %q on screen", got)
	}
}

// The rows a comment takes up depend on how much room the pane has, so a resize
// has to lay the document out again — not only when a walkthrough is on.
func TestResizingRewrapsTheCommentsOnScreen(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew,
			Body: strings.Repeat("a wide note about a narrow line. ", 4), Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	wide := commentRows(m.doc, "c1")
	send(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})
	narrow := commentRows(m.doc, "c1")

	if narrow <= wide {
		t.Errorf("the comment took %d rows at 100 columns and %d at 60, want more when narrower", wide, narrow)
	}
}

// commentRows counts the rows one comment takes up.
func commentRows(d Document, id string) int {
	n := 0
	for i := d.RowOfComment(id); i >= 0 && i < len(d.Rows) && d.Rows[i].Kind == RowComment; i++ {
		n++
	}
	return n
}

func TestResolvingKeepsTheCursorOnTheComment(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew, Body: "look again", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfComment("c1"))

	press(t, m, "x")

	if got, ok := m.doc.CommentAt(m.cursor); !ok || got.ID != "c1" {
		t.Errorf("cursor left the comment it resolved, landing on a %v", m.doc.Rows[m.cursor].Kind)
	}
}

// draftRows counts the rows the comment editor is holding.
func draftRows(d Document) int {
	n := 0
	for _, r := range d.Rows {
		if r.Kind == RowDraft {
			n++
		}
	}
	return n
}

func TestEmptyCommentIsDiscarded(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	typeText(t, m, "   ")
	press(t, m, "enter")

	if len(backend.added) != 0 {
		t.Fatalf("a blank comment was saved: %+v", backend.added)
	}
	if !strings.Contains(m.status, "discarded") {
		t.Errorf("status = %q", m.status)
	}
}

func TestResolveAndDeleteActOnTheCommentAtTheCursor(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Body: "look again", Author: store.AuthorAgent},
	}
	m := newModel(t, backend)

	commentRow := m.doc.RowOfComment("c1")
	if commentRow < 0 {
		t.Fatal("the comment was not placed")
	}
	m.moveTo(commentRow)

	press(t, m, "x")
	if !backend.resolved["c1"] {
		t.Fatal("x did not resolve the comment")
	}
	if !strings.Contains(m.status, "resolved") {
		t.Errorf("status = %q", m.status)
	}

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "x")
	if backend.resolved["c1"] {
		t.Error("a second x did not reopen the comment")
	}

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "D")
	if got := backend.removed; len(got) != 1 || got[0] != "c1" {
		t.Errorf("RemoveComment calls = %v, want [c1]", got)
	}
}

// TestDeletingACommentLeavesTheCursorOnItsLine holds `D` to the line it was
// pressed on. The comment named by the cursor is gone by the time the rebuild
// looks for it, and falling back to the top of the file would throw away the
// reviewer's place in the diff.
func TestDeletingACommentLeavesTheCursorOnItsLine(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	row := m.doc.RowOfComment("a1")
	if row < 0 {
		t.Fatal("the comment was not placed")
	}
	want, wantLine, ok := m.doc.LineAt(m.doc.AnchorOf(row))
	if !ok {
		t.Fatal("the comment is not anchored to a line")
	}
	m.moveTo(row)

	press(t, m, "D")

	got, gotLine, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatalf("cursor is on a %v, want the line the comment was on", m.doc.Rows[m.cursor].Kind)
	}
	if got.ID != want.ID || gotLine != wantLine {
		t.Errorf("cursor on %v line %d, want %v line %d", got.ID, gotLine, want.ID, wantLine)
	}
}

// TestHidingTheAgentsCommentsLeavesTheCursorOnTheLine is the same rule for `A`:
// the comment under the cursor goes away without being deleted, and the reviewer
// stays where they were reading.
func TestHidingTheAgentsCommentsLeavesTheCursorOnTheLine(t *testing.T) {
	m := newModel(t, reviewedByBoth(t))

	row := m.doc.RowOfComment("a1")
	want, wantLine, ok := m.doc.LineAt(m.doc.AnchorOf(row))
	if !ok {
		t.Fatal("the comment is not anchored to a line")
	}
	m.moveTo(row)

	press(t, m, "A")

	got, gotLine, ok := m.doc.LineAt(m.cursor)
	if !ok || got.ID != want.ID || gotLine != wantLine {
		t.Errorf("cursor on %v line %d, want %v line %d", got.ID, gotLine, want.ID, wantLine)
	}
}

// reviewedByBoth is a diff commented on by an agent and by the reviewer, which
// is what `A` and `X` have to tell apart.
func reviewedByBoth(t *testing.T) *fakeBackend {
	t.Helper()
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "a1", File: "alpha.go", Line: 3, Body: "this drops the error", Author: store.AuthorAgent},
		{ID: "u1", File: "alpha.go", Line: 4, Body: "mine, keep it", Author: store.AuthorUser},
		{ID: "a2", File: "beta.txt", Line: 2, Body: "and this one moved", Author: store.AuthorAgent},
	}
	return backend
}

func TestAHidesTheAgentsCommentsAndLeavesTheReviewersOwn(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	press(t, m, "A")
	for _, id := range []string{"a1", "a2"} {
		if row := m.doc.RowOfComment(id); row >= 0 {
			t.Errorf("agent comment %s is still on row %d", id, row)
		}
	}
	if m.doc.RowOfComment("u1") < 0 {
		t.Error("the reviewer's own comment went with them")
	}
	if !strings.Contains(m.headerView(), "agent hidden") {
		t.Errorf("header = %q, want it to say the notes are hidden", m.headerView())
	}
	if len(backend.removed) != 0 {
		t.Errorf("hiding removed %v — it must not delete anything", backend.removed)
	}

	press(t, m, "A")
	for _, id := range []string{"a1", "u1", "a2"} {
		if m.doc.RowOfComment(id) < 0 {
			t.Errorf("comment %s did not come back", id)
		}
	}
}

// A review read without the agent's notes is read that way for a reason, and
// the reason outlasts the process: opening it again keeps them out of the diff
// rather than putting a review back that has been dealt with already.
func TestHiddenAgentCommentsAreRememberedAcrossSessions(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	press(t, m, "A")
	if !backend.agentHidden {
		t.Fatal("hiding the agent's notes was not written down")
	}

	again := newModel(t, backend)
	for _, id := range []string{"a1", "a2"} {
		if row := again.doc.RowOfComment(id); row >= 0 {
			t.Errorf("agent comment %s came back on row %d, want it still hidden", id, row)
		}
	}
	if !strings.Contains(again.headerView(), "agent hidden") {
		t.Errorf("header = %q, want it to say the notes are hidden", again.headerView())
	}

	press(t, again, "A")
	if backend.agentHidden {
		t.Error("showing them again was not written down")
	}
}

// The record is a filter, and a filter with nothing to filter is a header
// claiming notes are hidden that `A` refuses to argue with — so a review with
// no agent notes in it opens plain, whatever was written down.
func TestARemembersNothingWhenThereAreNoAgentComments(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "u1", File: "alpha.go", Line: 4, Body: "mine, keep it", Author: store.AuthorUser},
	}
	backend.agentHidden = true

	m := newModel(t, backend)
	if m.agentCommentsOff {
		t.Error("the review opened filtered with nothing to filter")
	}
	if strings.Contains(m.headerView(), "agent hidden") {
		t.Errorf("header = %q, want no claim that notes are hidden", m.headerView())
	}
}

// Deleting the notes says what hiding them said, and more, so the record of
// hiding them goes with them.
func TestDeletingTheAgentsCommentsForgetsThatTheyWereHidden(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	press(t, m, "A")
	press(t, m, "X", "y")

	if backend.agentHidden {
		t.Error("the notes are deleted and still written down as hidden")
	}
}

func TestXDeletesEveryAgentCommentOnlyAfterYes(t *testing.T) {
	backend := reviewedByBoth(t)
	m := newModel(t, backend)

	press(t, m, "X")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want the question to be up", m.mode)
	}
	if !strings.Contains(m.footerView(), "delete 2 agent comments?") {
		t.Errorf("footer = %q, want it to ask", m.footerView())
	}

	press(t, m, "n")
	if len(backend.removed) != 0 {
		t.Fatalf("a no deleted %v", backend.removed)
	}
	if m.mode != modeBrowse || !strings.Contains(m.status, "cancelled") {
		t.Errorf("mode = %v, status = %q", m.mode, m.status)
	}

	press(t, m, "X", "y")
	if got := append([]string(nil), backend.removed...); len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Errorf("RemoveComment calls = %v, want [a1 a2]", got)
	}
	if m.doc.RowOfComment("u1") < 0 {
		t.Error("the reviewer's own comment was deleted with the agent's")
	}
	if !strings.Contains(m.status, "deleted 2 agent comments") {
		t.Errorf("status = %q", m.status)
	}
}

func TestTheAgentCommentKeysSayWhenThereAreNone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "u1", File: "alpha.go", Line: 3, Body: "mine", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	press(t, m, "A")
	if m.agentCommentsOff || !strings.Contains(m.status, "no agent comments") {
		t.Errorf("hidden = %v, status = %q", m.agentCommentsOff, m.status)
	}

	press(t, m, "X")
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want no question over an empty deletion", m.mode)
	}
	if m.doc.RowOfComment("u1") < 0 {
		t.Error("the reviewer's own comment left the diff")
	}
}

// `C` hands the review to an agent that cannot read peel's store: the notes go
// on the clipboard as text, the resolved ones stay behind, and the review itself
// does not move.
func TestCCopiesTheReviewAsTextToPaste(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "this leaks the tx", Author: store.AuthorUser},
		{ID: "c2", File: "alpha.go", Line: 4, Side: store.SideNew, Body: "dealt with", Author: store.AuthorUser, Resolved: true},
		{ID: "c3", File: "beta.txt", Line: 2, Side: store.SideNew, Body: "wrong fixture", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	before := m.cursor

	press(t, m, "C")

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	text := backend.copied[0]
	for _, want := range []string{"alpha.go:3", "this leaks the tx", "beta.txt:2", "wrong fixture"} {
		if !strings.Contains(text, want) {
			t.Errorf("the copied review is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "dealt with") {
		t.Errorf("a resolved note was handed over:\n%s", text)
	}
	if !strings.Contains(m.status, "copied 2 comments") || !strings.Contains(m.status, "1 resolved one left out") {
		t.Errorf("status = %q, want the copy and the omission both reported", m.status)
	}
	if m.cursor != before {
		t.Errorf("cursor moved from row %d to %d — copying is not progress through the diff", before, m.cursor)
	}
}

// What `C` hands an agent is the review the person wrote: the agent's own notes
// are left out whether or not they are on screen.
func TestCCopiesOnlyTheReviewersOwnNotes(t *testing.T) {
	for _, showing := range []bool{true, false} {
		backend := reviewedByBoth(t)
		m := newModel(t, backend)
		if !showing {
			press(t, m, "A")
		}

		press(t, m, "C")

		if len(backend.copied) != 1 {
			t.Fatalf("agent notes showing = %v: Copy called %d times, want 1", showing, len(backend.copied))
		}
		text := backend.copied[0]
		if !strings.Contains(text, "mine, keep it") {
			t.Errorf("agent notes showing = %v: the reviewer's own note was left out:\n%s", showing, text)
		}
		for _, agent := range []string{"this drops the error", "and this one moved"} {
			if strings.Contains(text, agent) {
				t.Errorf("agent notes showing = %v: an agent note was copied:\n%s", showing, text)
			}
		}
		if !strings.Contains(m.status, "copied 1 comment") {
			t.Errorf("agent notes showing = %v: status = %q, want the reviewer's one note counted", showing, m.status)
		}
	}
}

func TestCSaysWhenThereIsNothingToHandOver(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "C")
	if len(backend.copied) != 0 {
		t.Fatalf("an empty review was copied: %q", backend.copied)
	}
	if !strings.Contains(m.status, "no comments to copy") {
		t.Errorf("status = %q", m.status)
	}

	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Body: "dealt with", Author: store.AuthorUser, Resolved: true},
	}
	m = newModel(t, backend)

	press(t, m, "C")
	if len(backend.copied) != 0 {
		t.Fatalf("a resolved review was copied: %q", backend.copied)
	}
	if !strings.Contains(m.status, "every comment of your own is resolved") {
		t.Errorf("status = %q, want the resolved review distinguished from an empty one", m.status)
	}
}

// A screen full of the agent's notes and none of your own is not an empty
// review, and saying "no comments to copy" over one reads like a bug.
func TestCSaysTheAgentsNotesAreNotTheReviewToHandOver(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "a1", File: "alpha.go", Line: 3, Body: "this drops the error", Author: store.AuthorAgent},
	}
	m := newModel(t, backend)

	press(t, m, "C")
	if len(backend.copied) != 0 {
		t.Fatalf("an agent's own notes were copied: %q", backend.copied)
	}
	if !strings.Contains(m.status, "no comments of your own to copy") {
		t.Errorf("status = %q, want it to say whose notes were left out", m.status)
	}
}

func TestCReportsHavingNothingToCopyWith(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Body: "this leaks the tx", Author: store.AuthorUser},
	}
	backend.opErr = errors.New("nothing on PATH to copy with")
	m := newModel(t, backend)

	press(t, m, "C")

	if m.err == nil {
		t.Fatal("a failed copy reported nothing")
	}
	if strings.Contains(m.status, "copied") {
		t.Errorf("status = %q, want the copy taken back off", m.status)
	}
}

func TestResolveAwayFromACommentSaysSo(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "x")
	if len(backend.resolved) != 0 {
		t.Fatalf("SetResolved was called: %v", backend.resolved)
	}
	if !strings.Contains(m.status, "move to a comment") {
		t.Errorf("status = %q", m.status)
	}
}

// lineRowOf finds the row displaying a hunk line, so a test can put the cursor on
// a specific line of the diff.
func lineRowOf(t *testing.T, m *Model, hunk, line int) int {
	t.Helper()
	row := m.doc.RowOfLine(hunk, line)
	if row < 0 {
		t.Fatalf("hunk %d has no row for line %d", hunk, line)
	}
	return row
}

func TestUnstageOnALineOfAnUnstagedFileDoesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, 3))
	press(t, m, "u")

	if len(backend.unstagedFiles) != 0 {
		t.Fatalf("UnstageFile was called on a worktree-only file: %v", backend.unstagedFiles)
	}
	if !strings.Contains(m.status, "nothing staged") {
		t.Errorf("status = %q", m.status)
	}
}

func TestCommentOnADiffLineUsesThatLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	// Line 2 of the hunk is the removal of old line 3.
	m.moveTo(lineRowOf(t, m, 0, 2))
	press(t, m, "c")
	typeText(t, m, "why remove this?")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 3 || got.Side != store.SideOld {
		t.Errorf("comment = line %d on %s, want line 3 on the old side", got.Line, got.Side)
	}
}

// The point of a cursor that rests anywhere: a note on code the change did not
// touch, which is where half of review feedback belongs.
func TestCommentOnAnUnchangedLineAnchorsToIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, 0))
	press(t, m, "c")
	typeText(t, m, "this package name is wrong")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 1 || got.Side != store.SideNew {
		t.Errorf("comment = line %d on %s, want line 1 on the new side", got.Line, got.Side)
	}
}

func TestWalkthroughLoadsOnceAndRegeneratesOnDemand(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "w")
	if m.mode != modeBrowse {
		t.Fatalf("mode = %v — the walkthrough is the diff, not a screen of its own", m.mode)
	}
	if backend.walkCalls != 1 || backend.regenerate {
		t.Fatalf("walkthrough calls = %d, regenerate = %v", backend.walkCalls, backend.regenerate)
	}
	if !strings.Contains(m.View(), "The function alpha exports") {
		t.Error("the walkthrough body is not visible")
	}

	press(t, m, "w", "w")
	if backend.walkCalls != 1 {
		t.Errorf("walkthrough calls = %d, want the cached one reused", backend.walkCalls)
	}
	if len(m.doc.Steps) == 0 {
		t.Error("showing the walkthrough again left the diff ungrouped")
	}

	press(t, m, "W")
	if backend.walkCalls != 2 || !backend.regenerate {
		t.Errorf("W did not regenerate: calls = %d, regenerate = %v", backend.walkCalls, backend.regenerate)
	}
}

func TestWalkthroughFailureIsReported(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.opErr = errors.New("claude not found")
	m := newModel(t, backend)

	press(t, m, "w")

	if m.err == nil {
		t.Fatal("the walkthrough failure was swallowed")
	}
	if !strings.Contains(m.View(), "claude not found") {
		t.Error("the failure is not visible in the walkthrough pane")
	}
}

func TestHelpOpensAndAnyOtherKeyCloses(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("mode = %v, want help", m.mode)
	}
	if !strings.Contains(m.View(), "stage the file") {
		t.Error("help does not mention staging")
	}

	press(t, m, "x")
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse after any other key", m.mode)
	}
}

// There are more keys than rows on a short terminal, and a list cut off at the
// bottom is a key nobody reading it knows about — so the help screen scrolls,
// and every binding can be read on the smallest window peel opens in.
func TestHelpScrollsToTheEndOfTheKeys(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)), WithSize(100, 8))

	press(t, m, "?")
	var read strings.Builder
	for range len(m.helpBindings()) + 4 {
		read.WriteString(m.View())
		press(t, m, "down")
		if m.mode != modeHelp {
			t.Fatalf("↓ closed the help screen instead of reading on")
		}
	}
	for _, want := range []string{`\`, "walkthrough", "go to a file by name", "never from the command line"} {
		if !strings.Contains(read.String(), want) {
			t.Errorf("scrolling the whole help screen never showed %q", want)
		}
	}

	// It stops at the last line rather than scrolling the list off the top.
	if !strings.Contains(m.View(), "never from the command line") {
		t.Error("the foot of the help screen scrolled past its own end")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
		press(t, m, key)
		if !m.quitting {
			t.Errorf("%q did not quit", key)
		}
		if m.View() != "" {
			t.Errorf("%q left a view behind", key)
		}
	}
}

func TestReloadKeyRefreshes(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "r")
	if backend.reloads != 1 {
		t.Errorf("reloads = %d, want 1", backend.reloads)
	}
	if !strings.Contains(m.status, "reloaded") {
		t.Errorf("status = %q", m.status)
	}
}

func TestReloadKeyLeavesTheCursorOnTheLineItWasReading(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.moveTo(lineRowOf(t, m, 0, 3))
	ref, line, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatal("the cursor is not on a diff line")
	}

	press(t, m, "r")

	gotRef, gotLine, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatalf("the reload put the cursor on a %v, want the line it was reading", m.doc.Rows[m.cursor].Kind)
	}
	if gotRef.ID != ref.ID || gotLine != line {
		t.Errorf("cursor is on %v line %d, want %v line %d", gotRef.ID, gotLine, ref.ID, line)
	}
}

func TestViewFillsTheTerminalAtEverySize(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "a note", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	// 50 and 60 keep the file pane beside a narrow diff, which is where a pane
	// that has not been fitted to the leftover width shows up.
	sizes := []struct{ w, h int }{{40, 10}, {50, 16}, {60, 20}, {80, 24}, {200, 60}, {20, 8}}
	modes := []string{"", `\`, "?", "w"}

	for _, size := range sizes {
		for _, key := range modes {
			m := m
			send(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
			if key != "" {
				press(t, m, key)
			}
			lines := strings.Split(m.View(), "\n")
			if want := headerHeight + m.bodyHeight() + footerHeight; len(lines) != want {
				t.Fatalf("%dx%d after %q: view has %d lines, want %d", size.w, size.h, key, len(lines), want)
			}
			// Every body line must fill the terminal exactly, or a pane's
			// background shows through where a row ran short.
			for i := headerHeight; i < headerHeight+m.bodyHeight(); i++ {
				if w := ansi.StringWidth(lines[i]); w != size.w {
					t.Fatalf("%dx%d after %q: body line %d is %d cells, want %d: %q",
						size.w, size.h, key, i, w, size.w, lines[i])
				}
			}
			press(t, m, "esc")
		}
	}
}

// tabDiff has the tab indentation every Go file in a real diff carries.
const tabDiff = "diff --git a/tabs.go b/tabs.go\n" +
	"index 1111111..2222222 100644\n--- a/tabs.go\n+++ b/tabs.go\n" +
	"@@ -1,4 +1,4 @@ func Do() {\n" +
	" func Do() {\n" +
	" \tfor i := range n {\n" +
	"-\t\t\tdeep(i)\n" +
	"+\t\t\tdeeper(i)\n" +
	" \t}\n"

// withSyntax turns highlighting back on. It matters here because the two
// content paths handle tabs differently: the plain path goes through lipgloss,
// which expands them, while the highlighted path hands text to chroma, which
// does not — and highlighting is what the real UI runs with.
func withSyntax() Option { return func(o *options) { o.syntax = NewHighlighter() } }

// TestNoRowLeavesATabForTheTerminalToExpand guards the frame's height. A tab is
// zero columns to ansi.StringWidth and eight on screen, so one that reaches the
// terminal makes its row overflow and wrap — and a body one line taller than it
// claims scrolls the whole display on every repaint.
func TestNoRowLeavesATabForTheTerminalToExpand(t *testing.T) {
	backend := newFakeBackend(newSession(t, tabDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "tabs.go", Body: "note\twith\ta tab", Author: store.AuthorUser},
	}
	m := newModel(t, backend, WithSize(80, 12), withSyntax())

	for _, layout := range []string{"", `\`} {
		if layout != "" {
			press(t, m, layout)
		}
		// Every scroll position, since only some rows carry a tab — and every
		// column, since a row scrolled sideways is cut after highlighting and
		// carries the escapes opened left of the cut along with it.
		for top := 0; top < m.doc.Len(); top++ {
			m.top = top
			m.clampTop()
			for xoff := 0; xoff <= m.maxCodeOffset()+codeStep; xoff += codeStep {
				m.xoff = xoff
				m.clampCode()
				for i, line := range strings.Split(m.View(), "\n") {
					if strings.Contains(line, "\t") {
						t.Fatalf("%q layout, top %d, col %d: line %d reaches the terminal with a tab: %q",
							m.layout, top, m.xoff, i, line)
					}
					if w := ansi.StringWidth(line); w > m.width {
						t.Fatalf("%q layout, top %d, col %d: line %d is %d cells, want at most %d: %q",
							m.layout, top, m.xoff, i, w, m.width, line)
					}
				}
			}
		}
		m.xoff = 0
		m.clampCode()
	}
}

func TestViewOnACleanTreeSaysSo(t *testing.T) {
	backend := newFakeBackend(&app.Session{Title: "working tree", Stageable: true})
	m := newModel(t, backend)

	if !strings.Contains(m.View(), "nothing to review") {
		t.Errorf("view = %q, want it to say there is nothing to review", m.View())
	}
	// Every key must be safe on an empty document.
	press(t, m, "j", "k", "down", "up", "]", "[", "alt+down", "alt+up", "}", "{", "g", "G", "s", "S", "u", "o", "c", "x", "D", "space", `\`)
	if m.err != nil {
		t.Errorf("err = %v, want none", m.err)
	}
}

func TestViewMarksAReadOnlySession(t *testing.T) {
	session := newSession(t, twoFileDiff)
	session.Stageable = false
	session.Title = "cli/cli#412 fix the thing"
	m := newModel(t, newFakeBackend(session))

	view := m.View()
	if !strings.Contains(view, "read-only") {
		t.Errorf("view does not mark the session read-only:\n%s", view)
	}
	if !strings.Contains(view, "cli/cli#412") {
		t.Error("view does not name the pull request")
	}
}

func TestWindowSizeResizesTheDiffPane(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	send(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	narrow := m.filePaneWidth()
	if narrow < filePaneMin {
		t.Errorf("file pane width = %d at 60 columns, want the pane kept at least %d wide", narrow, filePaneMin)
	}
	if m.diffWidth() != 60-narrow-1 {
		t.Errorf("diff width = %d, want %d", m.diffWidth(), 60-narrow-1)
	}

	send(t, m, tea.WindowSizeMsg{Width: 34, Height: 20})
	if m.filePaneWidth() != 0 {
		t.Errorf("file pane width = %d at 34 columns, want it dropped", m.filePaneWidth())
	}
	if m.diffWidth() != 34 {
		t.Errorf("diff width = %d, want the full 34", m.diffWidth())
	}

	send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	pane := m.filePaneWidth()
	if pane < filePaneMin || pane > filePaneMax {
		t.Errorf("file pane width = %d, want it between %d and %d", pane, filePaneMin, filePaneMax)
	}
	if m.diffWidth() != 120-pane-1 {
		t.Errorf("diff width = %d, want %d", m.diffWidth(), 120-pane-1)
	}
}

func TestFindHunkSurvivesRenumbering(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	id := m.doc.Hunks[1].ID
	m.collapsed["alpha.go"] = true
	m.rebuild()

	if got := m.findHunk(id); got != 0 {
		t.Errorf("findHunk = %d, want 0 after alpha.go collapsed away", got)
	}
	if got := m.findHunk(git.HunkID{Path: "gone.go"}); got != -1 {
		t.Errorf("findHunk for a missing hunk = %d, want -1", got)
	}
}

func TestWalkthroughNotesSitAboveTheFilesTheyCover(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "w")

	if len(m.doc.Steps) != 2 {
		t.Fatalf("the diff carries %d notes, want 2: %+v", len(m.doc.Steps), m.doc.Steps)
	}
	for i, want := range []struct{ title, file string }{
		{"The function alpha exports", "alpha.go"},
		{"The fixture that follows it", "beta.txt"},
	} {
		step := m.doc.Steps[i]
		if step.Number != i+1 || step.Title != want.title {
			t.Errorf("step %d = %d %q, want %d %q", i, step.Number, step.Title, i+1, want.title)
		}
		if len(step.Files) != 1 {
			t.Fatalf("step %d covers %d files, want 1", i, len(step.Files))
		}
		file := m.doc.Files[step.Files[0]]
		if file.Entry.Path != want.file {
			t.Errorf("step %d covers %s, want %s", i, file.Entry.Path, want.file)
		}
		if step.Row >= file.Row {
			t.Errorf("step %d's note is at row %d, want it above %s at row %d", i, step.Row, want.file, file.Row)
		}
	}

	// The notes are added to the diff, not put in place of it.
	if len(m.doc.Hunks) != 2 || len(m.doc.Files) != 2 {
		t.Errorf("the grouped diff has %d files and %d hunks, want the plain diff's 2 and 2",
			len(m.doc.Files), len(m.doc.Hunks))
	}
	view := m.View()
	for _, want := range []string{
		"The function alpha exports",
		"arrives beside it",
		"alpha.go",
		"func One() int { return 2 }",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the grouped diff does not show %q", want)
		}
	}
}

func TestWalkthroughOrdersTheFilesAsItReadsThem(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.walkBody = "## 1. The fixture first\n`beta.txt`\n\nStart here.\n\n" +
		"## 2. Then the code\n`alpha.go`\n\nAnd end here.\n"
	m := newModel(t, backend)

	press(t, m, "w")

	if got := paths(m); got[0] != "beta.txt" || got[1] != "alpha.go" {
		t.Errorf("files are in %v, want the narrative's order [beta.txt alpha.go]", got)
	}

	press(t, m, "w")

	if got := paths(m); got[0] != "alpha.go" || got[1] != "beta.txt" {
		t.Errorf("hiding the walkthrough left the files in %v, want git's order back", got)
	}
	if len(m.doc.Steps) != 0 {
		t.Errorf("hiding the walkthrough left %d notes in the diff", len(m.doc.Steps))
	}
	if !strings.Contains(m.status, "hidden") {
		t.Errorf("status = %q, want it to say the walkthrough is hidden", m.status)
	}
}

// paths lists the files in the order the diff shows them.
func paths(m *Model) []string {
	var out []string
	for _, f := range m.doc.Files {
		out = append(out, f.Entry.Path)
	}
	return out
}

func TestWalkthroughJumpingToAFileLandsOnItsNote(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "w", "alt+down")

	if row := m.doc.Rows[m.cursor]; row.Kind != RowStep || m.doc.StepAt(m.cursor) != 1 {
		t.Fatalf("J landed on %+v, want the note introducing beta.txt", row)
	}
}

func TestWalkthroughJumpingOnFromANoteReachesTheNextFile(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "w")
	m.moveTo(m.doc.Steps[0].Row)
	press(t, m, "alt+down")

	if want := m.doc.Steps[1].Row; m.cursor != want {
		t.Fatalf("opt+↓ on the first note left the cursor at %d, want the note introducing beta.txt at %d", m.cursor, want)
	}

	press(t, m, "alt+up")

	if want := m.doc.Steps[0].Row; m.cursor != want {
		t.Fatalf("opt+↑ on the second note left the cursor at %d, want the note introducing alpha.go at %d", m.cursor, want)
	}
}

func TestWalkthroughJumpingOnFromANoteReachesTheSecondFileItCovers(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeFileDiff))
	backend.walkBody = "## 1. The code and the fixture beside it\n`alpha.go` `beta.txt`\n\nBoth at once.\n\n" +
		"## 2. The notes\n`gamma.md`\n\nThen these.\n"
	m := newModel(t, backend)

	press(t, m, "w")
	m.moveTo(m.doc.Steps[0].Row)
	press(t, m, "alt+down")

	if want := m.doc.Files[1].Row; m.cursor != want {
		t.Fatalf("opt+↓ on a note covering two files landed at %d, want beta.txt at %d", m.cursor, want)
	}
}

func TestWalkthroughCollectsFilesNoStepNamed(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.walkBody = "## 1. Only alpha\n`alpha.go`\n\nNothing was said about the fixture.\n"
	m := newModel(t, backend)

	press(t, m, "w")

	if len(m.doc.Steps) != 2 {
		t.Fatalf("the diff carries %d notes, want the narrative's one plus a leftover group", len(m.doc.Steps))
	}
	leftover := m.doc.Steps[1]
	if leftover.Title != leftoverTitle {
		t.Errorf("leftover group title = %q, want %q", leftover.Title, leftoverTitle)
	}
	if len(leftover.Files) != 1 || m.doc.Files[leftover.Files[0]].Entry.Path != "beta.txt" {
		t.Errorf("leftover covers %v, want beta.txt — a file no step named must still be shown", leftover.Files)
	}
	if !strings.Contains(m.View(), leftoverTitle) {
		t.Error("the leftover group is not visible")
	}
}

func TestWalkthroughIgnoresAPathOutsideTheChangeset(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.walkBody = "## 1. A path that is not here\n`gamma.go`\n\nThe provider invented it.\n"
	m := newModel(t, backend)

	press(t, m, "w")

	if got := m.doc.Steps[0].Files; len(got) != 0 {
		t.Errorf("the invented path resolved to %v, want nothing", got)
	}
	if got := paths(m); len(got) != 2 || got[0] != "alpha.go" || got[1] != "beta.txt" {
		t.Errorf("files are %v, want both of the changeset's, once each", got)
	}
	if !strings.Contains(m.View(), "The provider invented it.") {
		t.Error("a step that placed no file lost its explanation too")
	}
}

func TestTabFoldsAWalkthroughNote(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "w")
	before := m.doc.Len()
	m.moveTo(m.doc.Steps[0].Row)

	press(t, m, "space")

	if !m.walkFolded[0] {
		t.Fatal("tab did not fold the first note")
	}
	if m.doc.Len() >= before {
		t.Errorf("the folded diff is %d rows, was %d — the explanation is still there", m.doc.Len(), before)
	}
	if view := m.View(); strings.Contains(view, "arrives beside it") {
		t.Error("the folded note still shows its explanation")
	} else if !strings.Contains(view, "The function alpha exports") {
		t.Error("folding hid the heading as well")
	}
	if m.cursor != m.doc.Steps[0].Row {
		t.Errorf("cursor at %d, want it left on the folded note at %d", m.cursor, m.doc.Steps[0].Row)
	}
	if got := paths(m); len(got) != 2 {
		t.Errorf("folding a note changed the diff to %v", got)
	}

	press(t, m, "space")
	if m.doc.Len() != before {
		t.Errorf("unfolding left %d rows, want %d back", m.doc.Len(), before)
	}
}

func TestAWalkthroughNoteIsNotStageable(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "w")
	m.moveTo(m.doc.Steps[0].Row)
	press(t, m, "s")

	if len(backend.stagedFiles) != 0 {
		t.Errorf("s on a walkthrough note staged %v", backend.stagedFiles)
	}
	if m.status == "" {
		t.Error("s on a walkthrough note said nothing")
	}
}

// wideModel opens the long-line diff in a pane far too narrow to hold it.
func wideModel(t *testing.T) *Model {
	t.Helper()
	return newModel(t, newFakeBackend(newSession(t, longLineDiff)), WithSize(60, 20))
}

func TestScrollingCodeSidewaysStopsAtTheLongestLine(t *testing.T) {
	m := wideModel(t)

	press(t, m, "l")
	if m.xoff != codeStep {
		t.Errorf("one press of l left the code at column %d, want %d", m.xoff, codeStep)
	}

	press(t, m, "$")
	want := m.maxCodeOffset()
	if want <= 0 {
		t.Fatalf("the long line fits the pane, so there is nothing to scroll")
	}
	if m.xoff != want {
		t.Errorf("$ left the code at column %d, want %d", m.xoff, want)
	}

	// Past the end there is nothing to read, so the pane stays where the longest
	// line ends rather than scrolling out into blank columns.
	press(t, m, "l", "l", "l", "right")
	if m.xoff != want {
		t.Errorf("scrolling past the longest line reached column %d, want it held at %d", m.xoff, want)
	}
}

// TestALongPathScrollsWhereTheCodeDoesNot is the case the offset used to refuse:
// every line of code fits the pane and only the header runs past it, so a scroll
// measured against the code alone left the rest of the path unreachable.
func TestALongPathScrollsWhereTheCodeDoesNot(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, longPathDiff)), WithSize(60, 20))
	if m.doc.CodeWidth > m.renderer.CodeColumns(m.layout) {
		t.Fatalf("the code in this diff overflows the pane, so it is not the header being tested")
	}
	if got := m.renderer.Row(m.doc, 0, RowState{}); strings.Contains(got, "handler.go") {
		t.Fatalf("the whole path fits the pane, so there is nothing to scroll to: %q", got)
	}

	press(t, m, "$")
	if m.xoff <= 0 {
		t.Fatalf("$ left the diff at column %d, want it out at the end of the path", m.xoff)
	}
	if got := m.renderer.Row(m.doc, 0, RowState{}); !strings.Contains(got, "handler.go") {
		t.Errorf("scrolled out to the path's end the header reads %q, want the end of the path in it", got)
	}
}

func TestScrollingCodeBackStopsAtTheFirstColumn(t *testing.T) {
	m := wideModel(t)

	press(t, m, "$", "0")
	if m.xoff != 0 {
		t.Errorf("0 left the code at column %d, want 0", m.xoff)
	}

	press(t, m, "h", "left")
	if m.xoff != 0 {
		t.Errorf("scrolling left of the first column reached %d, want 0", m.xoff)
	}
}

// TestScrollingCodeLeavesTheCursorAlone separates the two windows: sliding the
// code sideways shows the same rows, so unlike the wheel it has no reason to
// drag the cursor anywhere.
func TestScrollingCodeLeavesTheCursorAlone(t *testing.T) {
	m := wideModel(t)
	press(t, m, "j")
	before := m.cursor

	press(t, m, "l", "l", "$")
	if m.cursor != before {
		t.Errorf("cursor moved to row %d while scrolling sideways, want it left on %d", m.cursor, before)
	}
}

func TestHorizontalWheelScrollsTheCode(t *testing.T) {
	m := wideModel(t)

	send(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelRight, X: 50})
	if m.xoff != wheelColumns {
		t.Errorf("a wheel notch right left the code at column %d, want %d", m.xoff, wheelColumns)
	}

	// A horizontal notch over the file pane still reaches the diff: the pane
	// shortens paths from the left already and has nothing to slide.
	top := m.fileTop
	send(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelRight, X: 0})
	if m.xoff != 2*wheelColumns {
		t.Errorf("a notch over the file pane left the code at column %d, want %d", m.xoff, 2*wheelColumns)
	}
	if m.fileTop != top {
		t.Errorf("a horizontal notch scrolled the file list to %d, want it left at %d", m.fileTop, top)
	}

	send(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelLeft, X: 50})
	if m.xoff != wheelColumns {
		t.Errorf("a wheel notch left the code at column %d, want %d", m.xoff, wheelColumns)
	}
}

// TestAWiderTerminalPullsTheCodeBack keeps the offset meaningful across a
// resize: a wider pane shows more of the longest line, so an offset that sat at
// its end has to come back with it.
func TestAWiderTerminalPullsTheCodeBack(t *testing.T) {
	m := wideModel(t)
	press(t, m, "$")
	narrow := m.xoff

	send(t, m, tea.WindowSizeMsg{Width: 200, Height: 20})
	if m.xoff >= narrow {
		t.Errorf("widening the terminal left the code at column %d, want less than %d", m.xoff, narrow)
	}
	if want := m.maxCodeOffset(); m.xoff != want {
		t.Errorf("after widening the code sits at column %d, want %d", m.xoff, want)
	}
}

// TestTheSplitLayoutScrollsFurther is the same line in half the room, so there
// is more of it off screen to reach.
func TestTheSplitLayoutScrollsFurther(t *testing.T) {
	m := wideModel(t)
	unified := m.maxCodeOffset()

	press(t, m, `\`)
	if split := m.maxCodeOffset(); split <= unified {
		t.Errorf("split scrolls to column %d and unified to %d, want split to reach further", split, unified)
	}
}

func TestTheHeaderNamesTheColumnTheCodeStartsAt(t *testing.T) {
	m := wideModel(t)
	if strings.Contains(m.View(), "col ") {
		t.Errorf("the header names a column before anything has been scrolled: %q", header(m))
	}

	press(t, m, "l")
	if want := fmt.Sprintf("col %d", codeStep+1); !strings.Contains(header(m), want) {
		t.Errorf("header = %q, want it to say %q", header(m), want)
	}
}

func header(m *Model) string { return strings.SplitN(m.View(), "\n", 2)[0] }

// The two halves of a part-staged file both hold a line 3, and a note has to
// record which one it was written on — or it comes back on the other.
func TestCommentOnAPartStagedFileRecordsWhichHalf(t *testing.T) {
	backend := newFakeBackend(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}))
	m := newModel(t, backend)

	m.moveTo(rowOfCode(t, m, "inserted"))
	press(t, m, "c")
	typeText(t, m, "why insert this")
	press(t, m, "enter")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	got := backend.added[0]
	if got.Line != 3 || got.Origin != store.OriginWorktree {
		t.Errorf("note anchored at line %d of %q, want line 3 of the working tree", got.Line, got.Origin)
	}
	if text, staged := codeUnder(t, m.doc, got.ID); text != "inserted" || staged {
		t.Errorf("the note was drawn under %q (staged=%v), want it under the line it was written on", text, staged)
	}
}

// The index's half opens folded, and `space` on its heading shows it — the
// reviewer can always read what they staged.
func TestSpaceOnTheStagedHeadingShowsWhatIsAlreadyStaged(t *testing.T) {
	m := newModel(t, newFakeBackend(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")})))

	if !m.doc.Sides[0].Folded {
		t.Fatal("the index's half should open folded")
	}
	m.moveTo(m.doc.Sides[0].Row)
	press(t, m, "space")

	if m.doc.Sides[0].Folded {
		t.Error("space on the heading did not show the staged half")
	}
	if got := m.doc.SideAt(m.cursor); got != 0 {
		t.Errorf("the cursor left the heading it was pressed on, and is on side %d", got)
	}

	press(t, m, "space")
	if !m.doc.Sides[0].Folded {
		t.Error("space did not put the staged half away again")
	}
}

// `space` on the working tree's heading is refused: folding away what is left to
// review would leave a file that says it changed and shows nothing.
func TestSpaceOnTheWorkingTreeHeadingKeepsItOpen(t *testing.T) {
	m := newModel(t, newFakeBackend(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")})))

	m.moveTo(m.doc.Sides[1].Row)
	press(t, m, "space")

	if m.doc.Sides[1].Folded {
		t.Error("the working tree's half folded away")
	}
	if m.status == "" {
		t.Error("nothing was said about why it did not fold")
	}
}

// A file folded away by staging is a file the pass is finished with — until
// something lands in it. What arrives is unreviewed, and a `✓` in the tree is
// the one place a change could hide from the pass, so the file opens again.
func TestAFileChangedAfterStagingOpensAgain(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "s")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("staging alpha.go did not fold it away")
	}

	backend.nextSession = sessionOf([]git.FileEntry{partStagedFile(t, "alpha.go")})
	press(t, m, "r")

	if m.doc.Files[0].Collapsed {
		t.Fatal("alpha.go stayed folded after changing under a staged file")
	}
	if !strings.Contains(m.status, "reopened") {
		t.Errorf("status = %q, want it to say the file was opened again", m.status)
	}
	// What it opens on is the new work: the half already in the index stays put
	// away, so the file shows exactly what arrived.
	if !m.doc.Sides[0].Folded || m.doc.Sides[1].Folded {
		t.Errorf("sides = %+v, want only the working tree's half open", m.doc.Sides)
	}
}

// A file put away by hand was put away on purpose, and stays that way.
func TestAFileFoldedByHandStaysFoldedWhenItChanges(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "space")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("space did not fold alpha.go away")
	}

	backend.nextSession = sessionOf([]git.FileEntry{partStagedFile(t, "alpha.go")})
	press(t, m, "r")

	if !m.doc.Files[0].Collapsed {
		t.Error("a file folded by hand opened again when it changed")
	}
}

// Staging the rest of a part-staged file leaves it with one half, and the
// heading that told the two apart goes with the half it was there to separate —
// even when the reviewer had folded that heading away, which used to leave the
// file showing a "staged" line and none of its diff.
func TestStagingTheRestOfAFileDropsItsSideHeadings(t *testing.T) {
	backend := newFakeBackend(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}))
	m := newModel(t, backend)

	m.moveTo(m.doc.Sides[0].Row)
	press(t, m, "space")
	press(t, m, "space")
	if !m.doc.Sides[0].Folded {
		t.Fatal("the index's half should be folded away by hand before staging the rest")
	}

	whole := partStagedFile(t, "notes.txt")
	whole.Staged, whole.Unstaged = whole.Unstaged, nil
	backend.nextSession = sessionOf([]git.FileEntry{whole})

	press(t, m, "s")
	press(t, m, "space")

	if m.doc.Files[0].Collapsed {
		t.Fatal("space did not open the file staging had folded away")
	}
	if len(m.doc.Sides) != 0 {
		t.Errorf("sides = %+v, want none — the file is all in the index now", m.doc.Sides)
	}
	if len(m.doc.Hunks) == 0 {
		t.Error("the file opened onto nothing, with its diff still hidden behind a folded heading")
	}
}

// rowOfCode finds the row showing a line of code, by its text.
func rowOfCode(t *testing.T, m *Model, text string) int {
	t.Helper()
	for i, r := range m.doc.Rows {
		if r.Kind != RowLine {
			continue
		}
		if m.doc.Hunks[r.Hunk].Hunk.Lines[max(r.Left, r.Right)].Text == text {
			return i
		}
	}
	t.Fatalf("no row shows %q", text)
	return -1
}

// What `C` hands over and what the diff shows are the same review. A note whose
// file has left the change is drawn under a header of its own, so the clipboard
// never carries something the reviewer had no way to see, resolve, or delete.
func TestCCopiesNothingTheDiffDoesNotShow(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "still in the change", Author: store.AuthorUser},
		{ID: "c2", File: "reverted.go", Line: 12, Side: store.SideNew, Body: "the change went, this stayed", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	press(t, m, "C")

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	for _, c := range backend.comments {
		if m.doc.RowOfComment(c.ID) < 0 {
			t.Errorf("%s was copied but drawn nowhere", c.ID)
		}
	}
	got := backend.copied[0]
	if !strings.Contains(got, "reverted.go:12") {
		t.Errorf("the copied review is missing the note on the file that left the diff:\n%s", got)
	}
	if !strings.Contains(got, goneNote) {
		t.Errorf("the agent was sent to a line in a file the change no longer touches, unwarned:\n%s", got)
	}
	if strings.Count(got, goneNote) != 1 {
		t.Errorf("the warning was put on a note whose file is still in the change:\n%s", got)
	}
}

// A file drawn only because a note outlived its changes has nothing to stage.
// `s` on its header says so instead of reporting a staging that stages nothing.
func TestSOnAFileThatLeftTheDiffStagesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "reverted.go", Line: 12, Side: store.SideNew, Body: "left behind", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfFile(m.fileIndex("reverted.go")))

	press(t, m, "s")

	if len(backend.stagedFiles) != 0 {
		t.Errorf("staged %v, want git left alone", backend.stagedFiles)
	}
	if !strings.Contains(m.status, "no changes to stage") {
		t.Errorf("status = %q, want it to say there is nothing there to stage", m.status)
	}
}
