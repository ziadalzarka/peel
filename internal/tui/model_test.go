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
	comments, err := backend.Comments()
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
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
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
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
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

	press(t, m, "J")
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Fatalf("after J the cursor is on %q, want beta.txt", got)
	}
	press(t, m, "K")
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "alpha.go" {
		t.Fatalf("after K the cursor is on %q, want alpha.go", got)
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
	press(t, m, "]", "]")
	if m.fileTop != 2 {
		t.Errorf("] left the file pane at %d, want 2", m.fileTop)
	}
	if m.top != top {
		t.Errorf("] moved the diff window to %d, want it left at %d", m.top, top)
	}

	press(t, m, "[")
	if m.fileTop != 1 {
		t.Errorf("[ left the file pane at %d, want 1", m.fileTop)
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

	press(t, m, "J", "J")
	if m.cursor != m.doc.RowOfFile(2) || m.top != m.cursor {
		t.Errorf("after two J the window starts at row %d and the cursor is at %d, want both at file 2's header (%d)",
			m.top, m.cursor, m.doc.RowOfFile(2))
	}
	if got := m.markedFile(); got != 2 {
		t.Errorf("after two J the pane marks file %d, want 2", got)
	}

	press(t, m, "K")
	if got := m.markedFile(); got != 1 {
		t.Errorf("after K the pane marks file %d, want 1", got)
	}
}

// Staging is whole-file, so `s` on a hunk header stages the file the hunk is in.
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

	// A context line: there is nothing smaller than the file to stage, so even an
	// unchanged row stages the file it is part of.
	m.moveTo(lineRowOf(t, m, 0, 0))
	press(t, m, "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("StageFile calls = %v, want [alpha.go]", got)
	}
}

// A staged file folds away, since it has been dealt with — and `tab` opens it
// again, because a staged file is still worth reading.
func TestStagingAFileFoldsItAndTabOpensItAgain(t *testing.T) {
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

	press(t, m, "tab")
	if m.doc.Files[0].Collapsed {
		t.Error("tab did not open the staged file again")
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

	press(t, m, "tab")
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

func TestReloadAdvancesToWhatIsStillUnstaged(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))

	// After staging, alpha.go is fully staged and only beta.txt is left.
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)

	m := newModel(t, backend)
	press(t, m, "j", "s")

	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "alpha.go" {
		t.Fatalf("cursor moved to %q, want to stay on alpha.go", got)
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowFile {
		t.Errorf("cursor is on a %v; alpha.go has nothing unstaged left, so the header is right", got)
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

func TestTabCollapsesAndExpandsAFile(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "tab")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("tab did not collapse alpha.go")
	}
	if m.doc.Rows[m.cursor].Kind != RowFile {
		t.Errorf("cursor left the file header after collapsing")
	}
	press(t, m, "tab")
	if m.doc.Files[0].Collapsed {
		t.Error("tab did not expand alpha.go again")
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

func TestHelpOpensAndAnyKeyCloses(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("mode = %v, want help", m.mode)
	}
	view := m.View()
	for _, want := range []string{"stage the file", `\`, "walkthrough"} {
		if !strings.Contains(view, want) {
			t.Errorf("help does not mention %q", want)
		}
	}

	press(t, m, "j")
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse after any key", m.mode)
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
		// Every scroll position, since only some rows carry a tab.
		for top := 0; top < m.doc.Len(); top++ {
			m.top = top
			m.clampTop()
			for i, line := range strings.Split(m.View(), "\n") {
				if strings.Contains(line, "\t") {
					t.Fatalf("%q layout, top %d: line %d reaches the terminal with a tab: %q",
						m.layout, top, i, line)
				}
				if w := ansi.StringWidth(line); w > m.width {
					t.Fatalf("%q layout, top %d: line %d is %d cells, want at most %d: %q",
						m.layout, top, i, w, m.width, line)
				}
			}
		}
	}
}

func TestViewOnACleanTreeSaysSo(t *testing.T) {
	backend := newFakeBackend(&app.Session{Title: "working tree", Stageable: true})
	m := newModel(t, backend)

	if !strings.Contains(m.View(), "nothing to review") {
		t.Errorf("view = %q, want it to say there is nothing to review", m.View())
	}
	// Every key must be safe on an empty document.
	press(t, m, "j", "k", "down", "up", "J", "K", "g", "G", "s", "u", "c", "x", "D", "tab", `\`)
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

	press(t, m, "w", "J")

	if row := m.doc.Rows[m.cursor]; row.Kind != RowStep || m.doc.StepAt(m.cursor) != 1 {
		t.Fatalf("J landed on %+v, want the note introducing beta.txt", row)
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

	press(t, m, "tab")

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

	press(t, m, "tab")
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
