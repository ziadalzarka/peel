package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// A review is filed by what it is about, so the path says which pull request it
// holds and nothing about where it was read.
func TestReviewPathIsNamedAfterThePullRequest(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir("/state"))

	got := f.app.ReviewPath("github:cli/cli#412")
	want := filepath.Join("/state", "reviews", "github", "cli", "cli", "412.json")
	if got != want {
		t.Errorf("ReviewPath = %q, want %q", got, want)
	}
}

// Nothing a code host can put in a name reaches outside the reviews directory.
func TestReviewPathStaysUnderTheReviewsDirectory(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir("/state"))
	reviews := filepath.Join("/state", "reviews")

	for _, target := range []string{
		"github:../../etc/passwd#1",
		"gh/../..:o/r#2",
		"weird target with spaces",
	} {
		got := f.app.ReviewPath(target)
		if !strings.HasPrefix(filepath.Clean(got), reviews+string(filepath.Separator)) {
			t.Errorf("ReviewPath(%q) = %q, want it under %q", target, got, reviews)
		}
	}
}

// The working tree is the checkout's, so its state stays in .git; a pull request
// is not, so its state goes to the file named after it.
func TestStateForRoutesByWhatIsBeingReviewed(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")
	f.repo.Write("f.txt", "y\n")

	working, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	if f.app.StateFor(working).Comments != f.app.Local.Comments {
		t.Error("the working tree is not being kept in the repository")
	}

	pr, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	if f.app.StateFor(pr).Comments == f.app.Local.Comments {
		t.Error("the pull request is being kept in the repository")
	}
}

// Everything the review is — the notes, what has been folded away, how it was
// being looked at, and the narrative — travels with the pull request.
func TestAPullRequestReviewIsReadableFromAnotherCheckout(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	state := f.app.StateFor(s)
	if _, err := state.Comments.Add(store.Comment{File: "pr.go", Line: 1, Body: "this leaks"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := state.Folds.Save(s.Target, []string{"pr.go"}); err != nil {
		t.Fatalf("Save folds: %v", err)
	}
	if err := state.Views.Save(s.Target, store.View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save view: %v", err)
	}
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}

	// A second repository entirely, sharing only the state directory the first
	// one filed the review in — which is what another worktree or clone is.
	elsewhere := newFixtureSharingState(t, f)
	other, err := elsewhere.app.LoadPullRequest(elsewhere.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest elsewhere: %v", err)
	}
	read := elsewhere.app.StateFor(other)

	comments, err := read.Comments.List(other.CommentFilter())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "this leaks" {
		t.Errorf("comments read elsewhere = %v", comments)
	}
	if folded, _ := read.Folds.Load(other.Target); len(folded) != 1 || folded[0] != "pr.go" {
		t.Errorf("folds read elsewhere = %v", folded)
	}
	if view, _ := read.Views.Load(other.Target); !view.AgentCommentsHidden {
		t.Error("the view did not travel with the review")
	}
	if _, err := elsewhere.app.Walkthrough(elsewhere.ctx, other, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough elsewhere: %v", err)
	}
	if len(elsewhere.ai.requests) != 0 {
		t.Error("the narrative was generated again rather than read from the review")
	}
}

// A pull request is reviewable with no repository around it at all.
func TestAPullRequestIsReviewableOutsideARepository(t *testing.T) {
	stateDir := t.TempDir()
	forgeProvider := &fakeForge{
		name:      "fake-forge",
		available: true,
		pr: &forge.PullRequest{
			Ref:   forge.Ref{Owner: "o", Repo: "r", Number: 412},
			Title: "Drop the document key",
			Diff:  "diff --git a/pr.go b/pr.go\n--- a/pr.go\n+++ b/pr.go\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}

	a, err := app.Open(context.Background(), t.TempDir(),
		app.WithGlobalDir(stateDir),
		app.WithForgeRegistry(forge.NewRegistry(forgeProvider)),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s, err := a.LoadPullRequest(context.Background(), "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest outside a repository: %v", err)
	}
	if _, err := a.StateFor(s).Comments.Add(store.Comment{File: "pr.go", Line: 1, Body: "note"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	path := filepath.Join(stateDir, "reviews", "fake-forge", "o", "r", "412.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the review was not written to %s: %v", path, err)
	}
}

// A note written on a pull request before reviews were filed this way is in
// some checkout's .git, which is the one place it cannot be read back from
// anywhere else. Opening that pull request from that checkout moves it.
func TestOpeningAPullRequestAdoptsNotesLeftInTheRepository(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	target := "fake-forge:o/r#412"
	stranded, err := f.app.Local.Comments.Add(store.Comment{
		File: "pr.go", Line: 1, Body: "written before the move", Target: target,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}

	moved, err := f.app.StateFor(s).Comments.List(s.CommentFilter())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(moved) != 1 || moved[0].Body != "written before the move" {
		t.Fatalf("the pull request's review = %v", moved)
	}
	if moved[0].ID != stranded.ID {
		t.Errorf("id = %q, want the note it was, not a copy", moved[0].ID)
	}
	if left, _ := f.app.Local.Comments.List(store.Filter{}); len(left) != 0 {
		t.Errorf("the note is still in the repository as well: %v", left)
	}
}

// newFixtureSharingState is a second repository that keeps its reviews in the
// same state directory as f — another clone, as far as peel is concerned.
func newFixtureSharingState(t *testing.T, f *fixture) *fixture {
	t.Helper()
	stateDir := os.Getenv("PEEL_STATE_DIR")
	if stateDir == "" {
		t.Fatal("the test repository did not set a state directory")
	}

	repo := gittest.New(t)
	// gittest.New has just given this repository a state directory of its own;
	// the point here is the two sharing one.
	t.Setenv("PEEL_STATE_DIR", stateDir)

	aiProvider := &fakeAI{name: "fake-ai", available: true, body: "## What changed\n\nStuff."}
	a, err := app.Open(context.Background(), repo.Dir,
		app.WithGlobalDir(stateDir),
		app.WithAIRegistry(ai.NewRegistry(aiProvider)),
		app.WithForgeRegistry(forge.NewRegistry(f.gh)),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return &fixture{t: t, repo: repo, app: a, ai: aiProvider, gh: f.gh, ctx: context.Background()}
}
