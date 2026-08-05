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
	comments, err := backend.Comments(t.Context())
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

// Against real git: `s` walks the review queue. Each press stages the file the
// cursor is in and lands on the next one still open, so a three-file pass is
// three presses.
func TestStagingWalksTheQueueOneFileAtATime(t *testing.T) {
	repo := gittest.New(t)
	paths := []string{"first.txt", "second.txt", "third.txt"}
	for _, path := range paths {
		repo.Write(path, "old\n")
	}
	repo.Commit("base")
	for _, path := range paths {
		repo.Write(path, "new\n")
	}
	// second.txt is staged already — an agent, or a git add outside peel — but its
	// diff has not been read here, so the walk still stops on it.
	repo.Git("add", "second.txt")

	m := realModel(t, repo)
	if got := m.currentPath(); got != "first.txt" {
		t.Fatalf("the pass starts on %q, want first.txt", got)
	}

	press(t, m, "s")
	if got := m.currentPath(); got != "second.txt" {
		t.Fatalf("after staging first.txt the cursor is on %q, want second.txt", got)
	}

	press(t, m, "s")
	if got := m.currentPath(); got != "third.txt" {
		t.Fatalf("after folding the staged second.txt the cursor is on %q, want third.txt", got)
	}

	press(t, m, "s")
	if got := m.currentPath(); got != "third.txt" {
		t.Errorf("staging the last file moved the cursor to %q, want it left on third.txt", got)
	}
	if got := repo.StatusLines(); len(got) != 3 {
		t.Fatalf("status = %v, want three files", got)
	}
	for _, line := range repo.StatusLines() {
		if line[1] != ' ' {
			t.Errorf("%q is not fully staged after the pass", line)
		}
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
