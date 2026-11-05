package app_test

import (
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/store"
)

func loadPR(t *testing.T, f *fixture) *app.Session {
	t.Helper()
	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	return s
}

func TestFoldsAreKeptInThePullRequestsReview(t *testing.T) {
	f := newFixture(t)
	f.gh.pr.HeadSHA = "head-1"
	s := loadPR(t, f)

	created, err := f.app.AddHiddenRun(f.ctx, s, store.HiddenRun{File: "pr.go", Line: 1, Label: "body", Author: "claude"})
	if err != nil {
		t.Fatalf("AddHiddenRun: %v", err)
	}
	if created.Blob != "head-1" {
		t.Errorf("Blob = %q, want the head commit", created.Blob)
	}

	local, err := f.app.Local.Hidden.List(store.HiddenFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(local) != 0 {
		t.Errorf("the working tree's folds = %+v, want none", local)
	}

	runs, err := f.app.HiddenRuns(f.ctx, s, store.HiddenFilter{})
	if err != nil {
		t.Fatalf("HiddenRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outdated {
		t.Errorf("runs = %+v, want one that still hides", runs)
	}
}

func TestAFoldStopsHidingOnceThePullRequestMoves(t *testing.T) {
	f := newFixture(t)
	f.gh.pr.HeadSHA = "head-1"
	s := loadPR(t, f)
	if _, err := f.app.AddHiddenRun(f.ctx, s, store.HiddenRun{File: "pr.go", Author: "claude"}); err != nil {
		t.Fatalf("AddHiddenRun: %v", err)
	}

	f.gh.pr.HeadSHA = "head-2"
	runs, err := f.app.HiddenRuns(f.ctx, loadPR(t, f), store.HiddenFilter{})
	if err != nil {
		t.Fatalf("HiddenRuns: %v", err)
	}
	if len(runs) != 1 || !runs[0].Outdated {
		t.Errorf("runs = %+v, want one marked outdated", runs)
	}
}

func TestAFoldOnAFileOutsideThePullRequestIsRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.app.AddHiddenRun(f.ctx, loadPR(t, f), store.HiddenRun{File: "other.go", Author: "claude"}); err == nil {
		t.Error("AddHiddenRun accepted a file with no changes")
	}
}

func TestAnAgentsNotesAreNotPostedWithTheReview(t *testing.T) {
	f := newFixture(t)
	s := loadPR(t, f)
	me := f.app.Author(f.ctx)
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "mine", Target: s.Target, Author: me})
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "from the agent", Target: s.Target, Author: "claude"})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got := f.gh.submitted[0].Comments; len(got) != 1 || got[0].Body != "mine" {
		t.Errorf("submitted %+v, want only the reviewer's note", got)
	}
}

func TestAJumpIsFiledUnderWhatItIsAbout(t *testing.T) {
	f := newFixture(t)
	s := loadPR(t, f)
	if err := f.app.AskToJump(s.Target, "pr.go", 1); err != nil {
		t.Fatalf("AskToJump: %v", err)
	}
	got, err := f.app.PendingJump(s.Target)
	if err != nil {
		t.Fatalf("PendingJump: %v", err)
	}
	if got.File != "pr.go" || got.Line != 1 || got.At.IsZero() {
		t.Errorf("PendingJump = %+v", got)
	}
	tree, err := f.app.PendingJump("")
	if err != nil {
		t.Fatalf("PendingJump for the working tree: %v", err)
	}
	if !tree.At.IsZero() {
		t.Errorf("the working tree picked up %+v", tree)
	}
	target, err := f.app.PullRequestTarget(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("PullRequestTarget: %v", err)
	}
	if target != s.Target {
		t.Errorf("PullRequestTarget = %q, want %q", target, s.Target)
	}
}
