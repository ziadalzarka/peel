package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// followModel builds a model over a real repository with follow mode on.
func followModel(t *testing.T) (*gittest.Repo, *Model) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write("main.go", "package main\n\nfunc main() {}\n")
	repo.Commit("initial")
	repo.Write("main.go", "package main\n\nfunc main() { println(1) }\n")

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	session, err := a.LoadWorkingTree(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	backend := NewBackend(a, session)
	comments, err := backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	m := New(context.Background(), backend, session, comments,
		WithoutSyntax(), WithSize(120, 40), WithFollow(true),
		WithPollInterval(time.Millisecond))
	return repo, m
}

// poll runs one follow check the way the tick handler does.
func poll(t *testing.T, m *Model) {
	t.Helper()
	msg := m.pollCmd()()
	if msg == nil {
		t.Fatal("poll produced no message")
	}
	m.Update(msg)
}

// body is everything the diff pane is showing, for substring assertions.
func body(m *Model) string {
	var out []string
	for i := range m.doc.Rows {
		out = append(out, m.renderer.Row(m.doc, i, RowState{}))
	}
	return strings.Join(out, "\n")
}

func TestFollowPicksUpAFileChangedOnDisk(t *testing.T) {
	repo, m := followModel(t)

	if got := body(m); !strings.Contains(got, "println(1)") {
		t.Fatalf("initial diff missing the first change:\n%s", got)
	}

	repo.Write("main.go", "package main\n\nfunc main() { println(2) }\n")
	poll(t, m)

	got := body(m)
	if !strings.Contains(got, "println(2)") {
		t.Errorf("follow did not pick up the new content:\n%s", got)
	}
	if strings.Contains(got, "println(1)") {
		t.Errorf("follow kept the stale content:\n%s", got)
	}
}

func TestFollowPicksUpABrandNewFile(t *testing.T) {
	repo, m := followModel(t)

	repo.Write("added.txt", "hello\n")
	poll(t, m)

	if len(m.doc.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(m.doc.Files))
	}
	if got := body(m); !strings.Contains(got, "added.txt") {
		t.Errorf("follow did not pick up the new file:\n%s", got)
	}
}

// A change made by git rather than an editor has to show up too: that is the
// agent-staged-something-underneath-you case.
func TestFollowPicksUpAnIndexChange(t *testing.T) {
	repo, m := followModel(t)

	if m.doc.Hunks[0].Staged {
		t.Fatal("the hunk is staged before anything staged it")
	}
	repo.Git("add", "main.go")
	poll(t, m)

	if !m.doc.Hunks[0].Staged {
		t.Error("follow did not notice that the hunk became staged")
	}
}

func TestFollowIgnoresAPollThatFoundNothingNew(t *testing.T) {
	_, m := followModel(t)
	m.status = ""

	poll(t, m)

	if m.status != "" {
		t.Errorf("an unchanged poll reported %q, want silence", m.status)
	}
}

func TestFollowLeavesTheCursorAloneWhileCommenting(t *testing.T) {
	repo, m := followModel(t)
	press(t, m, "c")
	if m.mode != modeComment {
		t.Fatalf("mode = %v, want the comment editor", m.mode)
	}

	repo.Write("main.go", "package main\n\nfunc main() { println(3) }\n")
	poll(t, m)

	if got := body(m); strings.Contains(got, "println(3)") {
		t.Error("a poll redrew the diff while a comment was open")
	}
}

func TestFollowLeavesTheCursorOnTheLineItWasReading(t *testing.T) {
	repo, m := followModel(t)

	m.moveTo(lineRowOf(t, m, 0, 3))
	ref, line, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatal("the cursor is not on a diff line")
	}

	repo.Write("added.txt", "hello\n")
	poll(t, m)

	gotRef, gotLine, ok := m.doc.LineAt(m.cursor)
	if !ok {
		t.Fatalf("a poll put the cursor on a %v, want the line it was reading", m.doc.Rows[m.cursor].Kind)
	}
	if gotRef.ID != ref.ID || gotLine != line {
		t.Errorf("cursor is on %v line %d, want %v line %d", gotRef.ID, gotLine, ref.ID, line)
	}
}

func TestTickSchedulesAnotherTickAndAPoll(t *testing.T) {
	_, m := followModel(t)

	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("a tick scheduled nothing, so follow mode stops after one check")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("a tick produced %T, want a batch of the poll and the next tick", cmd())
	}
	if len(batch) != 2 {
		t.Errorf("batch has %d commands, want the poll and the next tick", len(batch))
	}
}

func TestTickWithoutFollowDoesNothing(t *testing.T) {
	_, m := followModel(t)
	m.follow = false

	if _, cmd := m.Update(tickMsg{}); cmd != nil {
		t.Error("a stale tick kept the timer alive after follow was turned off")
	}
}

// Every review reads the files behind it, for the code the diff leaves out.
// Only a following one also starts a timer.
func TestInitStartsTheTimerOnlyWhenFollowing(t *testing.T) {
	_, m := followModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("follow mode started without a timer")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init produced %T, want a batch of the file read and the tick", cmd())
	}
	if len(batch) != 2 {
		t.Errorf("batch has %d commands, want the file read and the tick", len(batch))
	}

	m.follow = false
	cmd = m.Init()
	if cmd == nil {
		t.Fatal("nothing read the files the diff leaves code out of")
	}
	if _, ok := cmd().(contextMsg); !ok {
		t.Errorf("without follow Init produced %T, want the file read alone", cmd())
	}
}

// A follow check reads the repository on its own goroutine, so a key pressed
// while it is reading leaves it holding a diff from before that press. Applying
// it afterwards reopens the file `s` has just folded away, which is the one
// thing a background check must never do.
func TestAStalePollDoesNotReopenAFileStagedWhileItWasReading(t *testing.T) {
	_, m := followModel(t)

	// The read lands before the press, the way it does when the poll goroutine
	// is already in git when `s` is hit.
	stale := m.pollCmd()()
	if stale == nil {
		t.Fatal("the poll produced no message")
	}

	press(t, m, "s")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("`s` did not fold the file it staged")
	}

	m.Update(stale)

	if !m.doc.Files[0].Collapsed {
		t.Errorf("a follow check from before the press reopened the staged file: %q", m.status)
	}
	if m.doc.Files[0].Entry.Unstaged != nil {
		t.Error("a follow check from before the press put the change back in the working tree")
	}
}

// The guard on a stale check is not a latch: once the press has been read back,
// the next check is about a repository nothing is mid-write on, and follow mode
// goes on noticing what changes under the review.
func TestFollowKeepsWatchingAfterAStage(t *testing.T) {
	repo, m := followModel(t)
	press(t, m, "s")

	repo.Write("added.txt", "hello\n")
	poll(t, m)

	if len(m.doc.Files) != 2 {
		t.Fatalf("files = %d, want the staged one and the new one", len(m.doc.Files))
	}
	if got := body(m); !strings.Contains(got, "added.txt") {
		t.Errorf("follow stopped noticing new files after a stage:\n%s", got)
	}
	if !m.doc.Files[fileIndexOf(t, m, "main.go")].Collapsed {
		t.Error("a later follow check reopened the staged file")
	}
}

func noteStore(t *testing.T, repo *gittest.Repo) store.CommentStore {
	t.Helper()
	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	session, err := a.LoadWorkingTree(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	return a.StateFor(session).Comments
}

func leaveNote(t *testing.T, repo *gittest.Repo, body string) store.Comment {
	t.Helper()
	created, err := noteStore(t, repo).Add(store.Comment{
		File:   "main.go",
		Line:   3,
		Side:   store.SideNew,
		Body:   body,
		Author: store.AuthorAgent,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return created
}

func TestFollowPicksUpACommentAnAgentLeft(t *testing.T) {
	repo, m := followModel(t)

	leaveNote(t, repo, "this never returns an error")
	poll(t, m)

	if got := body(m); !strings.Contains(got, "this never returns an error") {
		t.Errorf("follow did not pick up the agent's comment:\n%s", got)
	}
	if m.status != "1 new comment" {
		t.Errorf("status = %q, want it to say a comment arrived", m.status)
	}
}

func TestFollowDropsACommentAnAgentRemoved(t *testing.T) {
	repo, m := followModel(t)
	note := leaveNote(t, repo, "this never returns an error")
	poll(t, m)

	if err := noteStore(t, repo).Remove(note.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	poll(t, m)

	if got := body(m); strings.Contains(got, "this never returns an error") {
		t.Errorf("follow kept a comment the agent removed:\n%s", got)
	}
	if m.status != "reloaded — the comments changed" {
		t.Errorf("status = %q, want it to say the comments changed", m.status)
	}
}

func TestFollowIgnoresAPollWhoseCommentsHaveNotChanged(t *testing.T) {
	repo, m := followModel(t)
	leaveNote(t, repo, "this never returns an error")
	poll(t, m)
	m.status = ""

	poll(t, m)

	if m.status != "" {
		t.Errorf("a poll over the same comments reported %q, want silence", m.status)
	}
}

func TestFollowKeepsANewAgentCommentHiddenWhileAgentCommentsAreHidden(t *testing.T) {
	repo, m := followModel(t)
	leaveNote(t, repo, "first pass")
	poll(t, m)
	press(t, m, "A")
	if !m.othersHidden {
		t.Fatal("A did not hide the agent's comments")
	}

	leaveNote(t, repo, "second pass")
	poll(t, m)

	if got := body(m); strings.Contains(got, "second pass") {
		t.Errorf("a new agent comment was drawn while agent comments are hidden:\n%s", got)
	}
	if !strings.Contains(m.status, "A shows them") {
		t.Errorf("status = %q, want it to say the new comment is hidden", m.status)
	}
}

func TestFollowLiftsTheAgentFilterOnceEveryAgentCommentIsGone(t *testing.T) {
	repo, m := followModel(t)
	note := leaveNote(t, repo, "first pass")
	poll(t, m)
	press(t, m, "A")
	if !m.othersHidden {
		t.Fatal("A did not hide the agent's comments")
	}

	if err := noteStore(t, repo).Remove(note.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	poll(t, m)

	if m.othersHidden {
		t.Error("agent comments still read as hidden after the agent removed every one of them")
	}
	leaveNote(t, repo, "second pass")
	poll(t, m)
	if got := body(m); !strings.Contains(got, "second pass") {
		t.Errorf("a new agent pass stayed behind a filter with nothing left under it:\n%s", got)
	}
}
