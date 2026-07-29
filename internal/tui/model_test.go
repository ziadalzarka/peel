package tui

import (
	"context"
	"errors"
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
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
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

func TestStageOnAHunkSendsThatHunkAndReloads(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "s")

	if len(backend.staged) != 1 {
		t.Fatalf("Stage called %d times, want 1", len(backend.staged))
	}
	sels := backend.staged[0]
	if len(sels) != 1 || sels[0].Hunk.String() != "alpha.go:@-1,4+1,5" {
		t.Fatalf("staged %+v, want alpha.go's only hunk", sels)
	}
	if len(sels[0].Lines) != 0 {
		t.Errorf("whole-hunk staging sent line indexes %v", sels[0].Lines)
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
	if len(backend.staged) != 0 {
		t.Errorf("a file header should not stage hunks: %+v", backend.staged)
	}
}

func TestStageAnAlreadyStagedHunkDoesNothing(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries[:1]))
	m := newModel(t, backend)

	press(t, m, "j", "s")

	if len(backend.staged) != 0 || len(backend.stagedFiles) != 0 {
		t.Fatalf("staging an already-staged hunk called the backend: %+v %v", backend.staged, backend.stagedFiles)
	}
	if !strings.Contains(m.status, "already staged") {
		t.Errorf("status = %q, want it to say the hunk is already staged", m.status)
	}
}

func TestUnstageAnUnstagedHunkDoesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "u")

	if len(backend.unstaged) != 0 {
		t.Fatalf("unstaging an unstaged hunk called the backend: %+v", backend.unstaged)
	}
	if !strings.Contains(m.status, "not staged") {
		t.Errorf("status = %q, want it to say the hunk is not staged", m.status)
	}
}

func TestUnstageOnAStagedHunkSendsIt(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries[:1]))
	m := newModel(t, backend)

	press(t, m, "j", "u")

	if len(backend.unstaged) != 1 {
		t.Fatalf("Unstage called %d times, want 1", len(backend.unstaged))
	}
	if got := backend.unstaged[0][0].Hunk.String(); got != "alpha.go:@-1,4+1,5" {
		t.Errorf("unstaged %q", got)
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

func TestStageAllAndUnstageAll(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "a")
	if backend.stageAll != 1 {
		t.Errorf("StageAll called %d times, want 1", backend.stageAll)
	}
	press(t, m, "U")
	if backend.unstageAll != 1 {
		t.Errorf("UnstageAll called %d times, want 1", backend.unstageAll)
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

func TestReloadClearsTheCachedWalkthrough(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "w")
	if !m.walkLoaded {
		t.Fatal("walkthrough did not load")
	}
	press(t, m, "esc")
	press(t, m, "a")

	if m.walkLoaded {
		t.Error("the walkthrough survived a reload, so it now describes the wrong diff")
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
	press(t, m, "ctrl+s")

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
	press(t, m, "ctrl+s")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 0 || got.File != "alpha.go" {
		t.Errorf("comment = %s:%d, want a file-level comment on alpha.go", got.File, got.Line)
	}
}

func TestEscapeCancelsAComment(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	typeText(t, m, "never mind")
	press(t, m, "esc")

	if len(backend.added) != 0 {
		t.Fatalf("a cancelled comment was saved: %+v", backend.added)
	}
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q", m.status)
	}
}

func TestEmptyCommentIsDiscarded(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	typeText(t, m, "   ")
	press(t, m, "ctrl+s")

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

	commentRow := rowOfComment(m.doc, "c1")
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

	m.moveTo(rowOfComment(m.doc, "c1"))
	press(t, m, "x")
	if backend.resolved["c1"] {
		t.Error("a second x did not reopen the comment")
	}

	m.moveTo(rowOfComment(m.doc, "c1"))
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

func TestLineSelectStagesOnlyTheChosenLines(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "v")
	if m.mode != modeLineSelect {
		t.Fatalf("mode = %v, want line select", m.mode)
	}

	// The hunk's changed lines are the removal at index 2 and the additions at 3
	// and 4. Skip the removal and take the first addition.
	press(t, m, "j", " ", "s")

	if len(backend.staged) != 1 {
		t.Fatalf("Stage called %d times, want 1", len(backend.staged))
	}
	sel := backend.staged[0][0]
	if sel.Hunk.String() != "alpha.go:@-1,4+1,5" {
		t.Errorf("staged hunk = %q", sel.Hunk)
	}
	if len(sel.Lines) != 1 || sel.Lines[0] != 3 {
		t.Errorf("staged lines = %v, want [3]", sel.Lines)
	}
}

func TestLineSelectSelectAllAndNone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "v", "a", "s")
	if len(backend.staged) != 1 {
		t.Fatalf("Stage called %d times, want 1", len(backend.staged))
	}
	if got := backend.staged[0][0].Lines; len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Errorf("staged lines = %v, want all three changed lines", got)
	}

	m = newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
	press(t, m, "j", "v", "a", "n", "s")
	if !strings.Contains(m.status, "select lines") {
		t.Errorf("status = %q, want a nudge to select something", m.status)
	}
}

func TestLineSelectWithNothingSelectedStagesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "v", "s")

	if len(backend.staged) != 0 {
		t.Fatalf("Stage was called with no lines selected: %+v", backend.staged)
	}
	if !strings.Contains(m.status, "select lines with space") {
		t.Errorf("status = %q", m.status)
	}
}

func TestLineSelectForcesUnifiedLayoutAndRestoresIt(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, `\`)
	if m.layout != LayoutSplit {
		t.Fatal("layout did not switch to split")
	}

	press(t, m, "j", "v")
	if m.layout != LayoutUnified {
		t.Errorf("layout = %v while selecting lines, want unified", m.layout)
	}
	if m.lineHunk < 0 {
		t.Fatal("no hunk is being line-selected")
	}

	press(t, m, "esc")
	if m.layout != LayoutSplit {
		t.Errorf("layout = %v after leaving line select, want split back", m.layout)
	}
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if got := m.doc.Rows[m.cursor].Kind; got != RowHunk {
		t.Errorf("cursor is on a %v, want the hunk it came from", got)
	}
}

func TestLineSelectRefusesAFileHeader(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "v")
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if !strings.Contains(m.status, "move to a hunk") {
		t.Errorf("status = %q", m.status)
	}
}

func TestLineSelectCommentUsesTheFocusedLine(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	// The first changed line is the removal on old line 3.
	press(t, m, "j", "v", "c")
	typeText(t, m, "why remove this?")
	press(t, m, "ctrl+s")

	if len(backend.added) != 1 {
		t.Fatalf("AddComment called %d times, want 1", len(backend.added))
	}
	if got := backend.added[0]; got.Line != 3 || got.Side != store.SideOld {
		t.Errorf("comment = line %d on %s, want line 3 on the old side", got.Line, got.Side)
	}
}

func TestUnstageLinesRequiresAStagedHunk(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "v", "a", "u")

	if len(backend.unstaged) != 0 {
		t.Fatalf("Unstage was called on a worktree hunk: %+v", backend.unstaged)
	}
	if !strings.Contains(m.status, "not staged") {
		t.Errorf("status = %q", m.status)
	}
}

func TestUnstageLinesOnAStagedHunkSendsThem(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries[:1]))
	m := newModel(t, backend)

	press(t, m, "j", "v", " ", "u")

	if len(backend.unstaged) != 1 {
		t.Fatalf("Unstage called %d times, want 1", len(backend.unstaged))
	}
	if got := backend.unstaged[0][0].Lines; len(got) != 1 || got[0] != 2 {
		t.Errorf("unstaged lines = %v, want [2]", got)
	}
}

func TestWalkthroughLoadsOnceAndRegeneratesOnDemand(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "w")
	if m.mode != modeWalkthrough {
		t.Fatalf("mode = %v, want walkthrough", m.mode)
	}
	if backend.walkCalls != 1 || backend.regenerate {
		t.Fatalf("walkthrough calls = %d, regenerate = %v", backend.walkCalls, backend.regenerate)
	}
	if !strings.Contains(m.View(), "What changed") {
		t.Error("the walkthrough body is not visible")
	}

	press(t, m, "esc")
	press(t, m, "w")
	if backend.walkCalls != 1 {
		t.Errorf("walkthrough calls = %d, want the cached one reused", backend.walkCalls)
	}

	press(t, m, "r")
	if backend.walkCalls != 2 || !backend.regenerate {
		t.Errorf("r did not regenerate: calls = %d, regenerate = %v", backend.walkCalls, backend.regenerate)
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
	for _, want := range []string{"stage the hunk", `\`, "walkthrough"} {
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

	sizes := []struct{ w, h int }{{40, 10}, {80, 24}, {200, 60}, {20, 8}}
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

func TestViewOnACleanTreeSaysSo(t *testing.T) {
	backend := newFakeBackend(&app.Session{Title: "working tree", Stageable: true})
	m := newModel(t, backend)

	if !strings.Contains(m.View(), "nothing to review") {
		t.Errorf("view = %q, want it to say there is nothing to review", m.View())
	}
	// Every key must be safe on an empty document.
	press(t, m, "j", "k", "J", "K", "g", "G", "s", "u", "v", "c", "x", "D", "tab", `\`)
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
	if m.filePaneWidth() != 0 {
		t.Errorf("file pane width = %d at 60 columns, want it dropped", m.filePaneWidth())
	}
	if m.diffWidth() != 60 {
		t.Errorf("diff width = %d, want the full 60", m.diffWidth())
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
