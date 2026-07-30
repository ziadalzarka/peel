package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// heldBackend stops a write inside the backend until the test lets it through,
// so a test can look at the screen while a change is still being written.
type heldBackend struct {
	*fakeBackend
	entered chan string
	release chan struct{}
}

func heldModel(t *testing.T, diff string) (*heldBackend, *Model) {
	t.Helper()
	fake := newFakeBackend(newSession(t, diff))
	held := &heldBackend{
		fakeBackend: fake,
		entered:     make(chan string),
		release:     make(chan struct{}),
	}
	m := New(context.Background(), held, fake.session, nil,
		WithTheme(Theme{}), WithoutSyntax(), WithSize(100, 30))
	return held, m
}

func (h *heldBackend) StageFile(ctx context.Context, path string) error {
	h.entered <- path
	<-h.release
	return h.fakeBackend.StageFile(ctx, path)
}

// The point of the whole thing: the file is staged, folded and left behind on the
// keypress, with git only asked afterwards.
func TestStagingIsOnScreenBeforeGitIsAsked(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.Update(keyMsg("s"))

	if len(backend.stagedFiles) != 0 || backend.reloads != 0 {
		t.Fatalf("git was asked on the keypress: staged %v, reloads %d", backend.stagedFiles, backend.reloads)
	}
	if got := m.doc.Files[0].Entry.State(); got != git.StateStaged {
		t.Errorf("alpha.go reads as %v, want it staged already", got)
	}
	if !m.doc.Files[0].Collapsed {
		t.Error("the staged file has not folded away yet")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("the cursor is on %q, want it moved on to beta.txt", got)
	}
	if !strings.Contains(m.status, "staged alpha.go") {
		t.Errorf("status = %q, want the stage reported", m.status)
	}
	if m.busy != "" {
		t.Errorf("busy = %q, want no banner: there is nothing to wait for", m.busy)
	}
}

// A write that fails puts back exactly the screen it was drawn over.
func TestAFailedWriteTakesTheChangeBackOff(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.opErr = errors.New("index.lock exists")
	m := newModel(t, backend)
	before, top := m.cursor, m.top

	press(t, m, "s")

	if got := m.doc.Files[0].Entry.State(); got != git.StateUnstaged {
		t.Errorf("alpha.go reads as %v after a failed stage, want it unstaged again", got)
	}
	if m.doc.Files[0].Collapsed {
		t.Error("a failed stage left the file folded away")
	}
	if m.cursor != before || m.top != top {
		t.Errorf("the cursor is at row %d of a window at %d, want it back at %d and %d", m.cursor, m.top, before, top)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "index.lock") {
		t.Errorf("err = %v, want the failure the write came back with", m.err)
	}
	if got := backend.folded; len(got) != 0 {
		t.Errorf("folded = %v, want the fold record put back with the screen", got)
	}
}

// A burst of presses is written one write at a time and read back once, at the
// end: the reloads in between describe a screen already moved past.
func TestABurstOfChangesReadsBackOnce(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	_, first := m.Update(keyMsg("s"))
	_, second := m.Update(keyMsg("s"))

	if msg := first(); msg != nil {
		t.Errorf("the first of two writes came back with a %T, want it to leave the reading to the last", msg)
	}
	if backend.reloads != 0 {
		t.Errorf("reloads = %d, want the first write to skip the read-back", backend.reloads)
	}

	settle(t, m, second, 0)

	if backend.reloads != 1 {
		t.Errorf("reloads = %d, want one read-back for the burst", backend.reloads)
	}
	if got := backend.stagedFiles; len(got) != 2 || got[0] != "alpha.go" || got[1] != "beta.txt" {
		t.Errorf("StageFile calls = %v, want [alpha.go beta.txt]", got)
	}
}

// A reload read before the change that is on screen would undraw it, so it is
// dropped rather than applied.
func TestAReadBackFromBeforeTheLastChangeIsDropped(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	m.Update(keyMsg("s"))
	stale := newSession(t, twoFileDiff)
	m.Update(loadedMsg{session: stale, reconcile: true})

	if got := m.doc.Files[0].Entry.State(); got != git.StateStaged {
		t.Errorf("alpha.go reads as %v, want the stage on screen kept", got)
	}
}

// Writes are queued, so peel's git calls arrive in the order the keys were
// pressed however the goroutines behind them are scheduled — two `git add` racing
// for the index lock is peel losing one of them.
func TestWritesReachGitInTheOrderTheyWerePressed(t *testing.T) {
	held, m := heldModel(t, threeFileDiff)

	_, first := m.Update(keyMsg("s"))
	_, second := m.Update(keyMsg("s"))

	done := make(chan tea.Msg, 2)
	go func() { done <- first() }()
	if got := <-held.entered; got != "alpha.go" {
		t.Fatalf("the first write staged %q, want alpha.go", got)
	}
	go func() { done <- second() }()

	select {
	case got := <-held.entered:
		t.Fatalf("%q reached git while the write pressed before it was still out", got)
	case <-time.After(20 * time.Millisecond):
	}

	close(held.release)
	if got := <-held.entered; got != "beta.txt" {
		t.Errorf("the second write staged %q, want beta.txt", got)
	}
	<-done
	<-done
}

// `q` straight after `s` is the natural press now that the stage is already on
// screen, so the write it promised is waited for rather than killed.
func TestQuitWaitsForTheWriteItPromised(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	_, stage := m.Update(keyMsg("s"))
	_, leave := m.Update(keyMsg("q"))

	left := make(chan tea.Msg, 1)
	go func() { left <- leave() }()
	select {
	case <-left:
		t.Fatal("peel quit while the stage it had already reported was unwritten")
	case <-time.After(20 * time.Millisecond):
	}

	stage()

	if _, ok := (<-left).(tea.QuitMsg); !ok {
		t.Error("peel did not quit once the write landed")
	}
	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Errorf("StageFile calls = %v, want the promised [alpha.go]", got)
	}
}

// Rapid presses against a real repository: every file the reviewer staged has to
// end up in the index, whatever order the goroutines run in.
func TestRapidStagesAllReachTheIndex(t *testing.T) {
	repo := gittest.New(t)
	paths := []string{"one.txt", "two.txt", "three.txt"}
	for _, path := range paths {
		repo.Write(path, "old\n")
	}
	repo.Commit("base")
	for _, path := range paths {
		repo.Write(path, "new\n")
	}

	m := realModel(t, repo)
	var cmds []tea.Cmd
	for range paths {
		_, cmd := m.Update(keyMsg("s"))
		cmds = append(cmds, cmd)
	}

	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)
		go func(cmd tea.Cmd) {
			defer wg.Done()
			cmd()
		}(cmd)
	}
	wg.Wait()

	lines := repo.StatusLines()
	if len(lines) != len(paths) {
		t.Fatalf("status = %v, want the three files", lines)
	}
	for _, line := range lines {
		if line[1] != ' ' {
			t.Errorf("%q did not make it fully into the index", line)
		}
	}
}

// A note is in the diff as soon as it is written, and until the store has it
// there is no ID to address it by — so the keys that act on a comment say so
// rather than failing behind the scenes.
func TestANoteIsInTheDiffBeforeItIsSaved(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "j", "c")
	typeText(t, m, "look again")
	_, save := m.Update(keyMsg("enter"))

	if len(m.comments) != 1 {
		t.Fatalf("comments on screen = %d, want the note just written", len(m.comments))
	}
	shown := m.comments[0]
	if m.doc.RowOfComment(shown.ID) < 0 {
		t.Error("the note is not in the diff yet")
	}
	if !strings.Contains(m.status, "commented on") {
		t.Errorf("status = %q, want the note reported", m.status)
	}

	m.moveTo(m.doc.RowOfComment(shown.ID))
	press(t, m, "x")
	if len(backend.resolved) != 0 {
		t.Errorf("a note the store has never seen was resolved: %v", backend.resolved)
	}
	if !strings.Contains(m.status, "still being saved") {
		t.Errorf("status = %q, want it to say the note is not saved yet", m.status)
	}

	settle(t, m, save, 0)

	if len(m.comments) != 1 || m.comments[0].ID != "c1" {
		t.Fatalf("comments = %+v, want the one the store assigned an ID to", m.comments)
	}
	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "x")
	if !backend.resolved["c1"] {
		t.Error("the saved note could not be resolved")
	}
}

// A read-back can land while the reviewer has moved on to writing a note. It is
// the tail of a change they have already seen, so it must not take the keyboard.
func TestAReadBackLeavesTheCommentEditorAlone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	_, stage := m.Update(keyMsg("s"))
	press(t, m, "c")
	typeText(t, m, "still typing")

	settle(t, m, stage, 0)

	if m.mode != modeComment {
		t.Fatalf("mode = %v, want the editor still open", m.mode)
	}
	if got := m.input.Value(); got != "still typing" {
		t.Errorf("the editor holds %q, want what was being typed", got)
	}
	if draftRows(m.doc) == 0 {
		t.Error("the editor left the diff when the stage read back")
	}
}

// The same path against a real repository and the real store: the note is drawn
// with an ID of peel's own and is the store's by the time the write has landed.
func TestANoteWrittenAgainstARealRepositoryLandsInTheStore(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\n")
	repo.Commit("base")
	repo.Write("list.txt", "two\n")

	m := realModel(t, repo)
	press(t, m, "c")
	typeText(t, m, "why two")
	press(t, m, "enter")

	if len(m.comments) != 1 {
		t.Fatalf("comments = %+v, want the one just written", m.comments)
	}
	if got := m.comments[0]; unsaved(got) || got.Body != "why two" {
		t.Errorf("comment = %+v, want it saved with an ID of the store's", got)
	}
	if m.doc.RowOfComment(m.comments[0].ID) < 0 {
		t.Error("the saved note is not in the diff")
	}
}

func TestStagePredictsWhatGitWillDoToAFile(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	entries[1].Untracked = true
	before := sessionOf(entries)

	after := restaged(before, true, only("alpha.go"))
	if after.Files[0].Staged == nil || after.Files[0].Unstaged != nil {
		t.Error("staging alpha.go did not move its changes to the index")
	}
	if after.Files[1].Staged != nil {
		t.Error("staging alpha.go moved beta.txt as well")
	}
	if before.Files[0].Staged != nil {
		t.Error("the session on screen was changed in place, not copied")
	}

	back := restaged(after, false, every)
	if back.Files[0].Staged != nil || back.Files[0].Unstaged == nil {
		t.Error("unstaging did not bring the changes back to the working tree")
	}
	if tracked := restaged(before, true, every); tracked.Files[1].Untracked {
		t.Error("a staged file is tracked; the guess left it untracked")
	}
}
