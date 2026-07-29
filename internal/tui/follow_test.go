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
	comments, err := backend.Comments()
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

func TestInitStartsTheTimerOnlyWhenFollowing(t *testing.T) {
	_, m := followModel(t)
	if m.Init() == nil {
		t.Error("follow mode started without a timer")
	}
	m.follow = false
	if m.Init() != nil {
		t.Error("a timer started without follow mode")
	}
}
