package tui

import (
	"context"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// realModel drives the UI against an actual repository, so `s` is checked
// against what lands in the index rather than against a recorded call.
func realModel(t *testing.T, repo *gittest.Repo) *Model {
	t.Helper()
	ctx := context.Background()

	// No providers: a test must never shell out to claude or gh.
	a, err := app.Open(ctx, repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	session, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	backend := NewBackend(a, session)
	comments, err := backend.Comments()
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	return New(ctx, backend, session, comments,
		WithTheme(Theme{}), WithoutSyntax(), WithSize(100, 30))
}

func TestStageWritesTheWholeFileToTheIndex(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\ntwo\nthree\n")
	repo.Commit("base")
	repo.Write("list.txt", "ONE\ntwo\nTHREE\n")

	m := realModel(t, repo)
	press(t, m, "s")

	if got, want := repo.StagedRaw("list.txt"), "ONE\ntwo\nTHREE\n"; got != want {
		t.Errorf("index contents = %q, want the whole file at %q", got, want)
	}
	if m.doc.Files[0].Entry.Unstaged != nil {
		t.Error("something was left in the working tree; staging takes the whole file")
	}
	if !m.doc.Files[0].Collapsed {
		t.Error("the staged file did not fold away")
	}
}

// The cursor is usually inside a hunk when the decision is made, not on the file
// header, and that has to stage the same thing.
func TestStageFromInsideTheDiffStagesTheFile(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\ntwo\nthree\n")
	repo.Commit("base")
	repo.Write("list.txt", "one\ntwo\nTHREE\n")

	m := realModel(t, repo)
	m.moveTo(lineRowOf(t, m, 0, 1))
	press(t, m, "s")

	if got, want := repo.StagedRaw("list.txt"), "one\ntwo\nTHREE\n"; got != want {
		t.Errorf("index contents = %q, want %q", got, want)
	}
}

func TestUnstageReturnsTheIndexToHeadAndOpensTheFile(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\ntwo\nthree\n")
	repo.Commit("base")
	repo.Write("list.txt", "one\ntwo\nTHREE\n")

	m := realModel(t, repo)
	press(t, m, "s")
	if !m.doc.Files[0].Collapsed {
		t.Fatal("staging did not fold the file")
	}

	press(t, m, "u")

	if got, want := repo.StagedRaw("list.txt"), "one\ntwo\nthree\n"; got != want {
		t.Errorf("index contents = %q, want HEAD's %q", got, want)
	}
	if got, want := repo.Read("list.txt"), "one\ntwo\nTHREE\n"; got != want {
		t.Errorf("working tree = %q, want it untouched at %q", got, want)
	}
	if m.doc.Files[0].Collapsed {
		t.Error("the unstaged file stayed folded away")
	}
}
