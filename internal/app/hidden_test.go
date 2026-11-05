package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

const (
	hiddenBase = "a\nb\nc\nd\ne\nf\ng\nh\n"
	hiddenWork = "a\nb\nC\nD\nE\nf\ng\nh\n"
)

func addRun(t *testing.T, a *app.App, line, end int) store.HiddenRun {
	t.Helper()
	s, err := a.LoadWorkingTree(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	run, err := a.AddHiddenRun(context.Background(), s, store.HiddenRun{
		File: "svc.go", Line: line, EndLine: end, Label: "body", Author: "claude",
	})
	if err != nil {
		t.Fatalf("AddHiddenRun: %v", err)
	}
	return run
}

func runsNow(t *testing.T, a *app.App) []store.HiddenRun {
	t.Helper()
	ctx := context.Background()
	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	runs, err := a.HiddenRuns(ctx, s, store.HiddenFilter{})
	if err != nil {
		t.Fatalf("HiddenRuns: %v", err)
	}
	return runs
}

func onlyRun(t *testing.T, a *app.App) store.HiddenRun {
	t.Helper()
	runs := runsNow(t, a)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1: %+v", len(runs), runs)
	}
	return runs[0]
}

func TestARunFollowsItsCodeWhenLinesAreAddedAbove(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Write("svc.go", "new one\nnew two\n"+hiddenWork)

	got := onlyRun(t, a)
	if got.Outdated {
		t.Fatal("nothing in the run changed, so it should still hide it")
	}
	if got.Line != 5 || got.EndLine != 7 || got.MovedFrom != 3 {
		t.Errorf("run is on %d-%d from %d, want 5-7 from 3", got.Line, got.EndLine, got.MovedFrom)
	}
}

func TestARunStopsHidingCodeRewrittenInsideIt(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Write("svc.go", strings.Replace(hiddenWork, "D\n", "REWRITTEN\n", 1))

	if !onlyRun(t, a).Outdated {
		t.Error("a line inside the run was rewritten and the run still hides it")
	}
}

func TestARunStopsHidingWhenLinesAreAddedInsideIt(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Write("svc.go", strings.Replace(hiddenWork, "D\n", "D\nnew\n", 1))

	if !onlyRun(t, a).Outdated {
		t.Error("a line was added inside the run and the run still hides it")
	}
}

func TestARunIsNotOutdatedByAChangeBesideIt(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Write("svc.go", strings.Replace(hiddenWork, "g\n", "CHANGED\n", 1))

	if onlyRun(t, a).Outdated {
		t.Error("the change is outside the run, so the run should keep hiding its own lines")
	}
}

func TestARunWhoseSnapshotIsGoneStopsHiding(t *testing.T) {
	a, _ := anchorRepo(t, hiddenBase, hiddenWork)
	if _, err := a.Local.Hidden.Add(store.HiddenRun{
		File: "svc.go", Line: 3, EndLine: 5, Author: "claude", Origin: store.OriginWorktree,
		Blob: strings.Repeat("1", 40),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if !onlyRun(t, a).Outdated {
		t.Error("peel cannot tell where the code went, so the run must not hide anything")
	}
}

func TestARunWithNoSnapshotStopsHiding(t *testing.T) {
	a, _ := anchorRepo(t, hiddenBase, hiddenWork)
	if _, err := a.Local.Hidden.Add(store.HiddenRun{File: "svc.go", Line: 3, Author: "claude"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if !onlyRun(t, a).Outdated {
		t.Error("a run with no snapshot cannot be checked, so it must not hide anything")
	}
}

func TestARunStaysWithItsCodeWhenTheFileIsStaged(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Git("add", "svc.go")

	got := onlyRun(t, a)
	if got.Outdated || got.Line != 3 || got.Origin != store.OriginIndex {
		t.Errorf("got %+v, want it still on 3 and read against the index", got)
	}
}

func TestAWholeFileRunStopsHidingOnceTheFileChanges(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 0, 0)

	if onlyRun(t, a).Outdated {
		t.Fatal("nothing has changed since the file was hidden")
	}

	repo.Git("add", "svc.go")
	if onlyRun(t, a).Outdated {
		t.Error("staging rewrites nothing, so the file should stay hidden")
	}

	repo.Write("svc.go", hiddenWork+"more\n")
	if !onlyRun(t, a).Outdated {
		t.Error("the file changed and is still hidden")
	}
}

func TestADeletedFileCanBeHiddenWhole(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenBase)
	repo.Remove("svc.go")
	addRun(t, a, 0, 0)

	if onlyRun(t, a).Outdated {
		t.Fatal("the file is still deleted")
	}

	repo.Write("svc.go", "back\n")
	if !onlyRun(t, a).Outdated {
		t.Error("the file came back with new code and is still hidden")
	}
}

func TestARunOnAFileThatLeftTheReviewIsNotCalledOutdated(t *testing.T) {
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)

	repo.Commit("committed")

	if onlyRun(t, a).Outdated {
		t.Error("the file has no changes to hide, which says nothing about the code in the run")
	}
}

func TestAddingTheSameRunAgainReplacesIt(t *testing.T) {
	a, _ := anchorRepo(t, hiddenBase, hiddenWork)
	first := addRun(t, a, 3, 5)
	second := addRun(t, a, 3, 5)

	got := onlyRun(t, a)
	if got.ID != second.ID || got.ID == first.ID {
		t.Errorf("kept %s, want the newer %s and not %s", got.ID, second.ID, first.ID)
	}

	addRun(t, a, 0, 0)
	addRun(t, a, 0, 0)
	if n := len(runsNow(t, a)); n != 2 {
		t.Errorf("got %d runs, want the line run and one whole-file run", n)
	}
}

func TestAddingARunRefusesWhatCannotBeHidden(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	repo.WriteBytes("logo.png", []byte{0, 1, 2, 3})
	repo.Write("gone.txt", "x\n")
	repo.Write("svc.go", hiddenBase)
	repo.Commit("more files")
	repo.WriteBytes("logo.png", []byte{0, 9, 9, 9})
	repo.Remove("gone.txt")
	repo.Write("svc.go", hiddenWork)

	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	for name, c := range map[string]struct {
		run  store.HiddenRun
		want string
	}{
		"not in the diff": {store.HiddenRun{File: "other.go"}, "no changes"},
		"past the end":    {store.HiddenRun{File: "svc.go", Line: 8, EndLine: 90}, "past its end"},
		"binary lines":    {store.HiddenRun{File: "logo.png", Line: 1}, "binary"},
		"deleted lines":   {store.HiddenRun{File: "gone.txt", Line: 1}, "not on disk"},
	} {
		c.run.Author = "claude"
		_, err := a.AddHiddenRun(ctx, s, c.run)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.want)
		}
	}
}

func TestAMergeConflictCannotBeHidden(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "base\n", "base\n")
	repo.Conflict("theirs",
		func(r *gittest.Repo) { r.Write("svc.go", "theirs\n") },
		func(r *gittest.Repo) { r.Write("svc.go", "ours\n") })

	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	_, err = a.AddHiddenRun(ctx, s, store.HiddenRun{File: "svc.go", Author: "claude"})
	if err == nil || !strings.Contains(err.Error(), "merge conflict") {
		t.Errorf("err = %v, want it refused as a merge conflict", err)
	}
}

func TestARunKeepsItsSnapshotWhenANoteIsRemoved(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, hiddenBase, hiddenWork)
	addRun(t, a, 3, 5)
	blob := onlyRun(t, a).Blob

	s, _ := a.LoadWorkingTree(ctx)
	n := note(t, a, s, 1, "a note")
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}
	if err := a.Local.Comments.Remove(n.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}

	if !strings.Contains(repo.Git("for-each-ref", "refs/peel/anchors/"), blob) {
		t.Error("removing a note dropped the snapshot a run still needs")
	}

	if _, err := a.Local.Hidden.Clear(store.HiddenFilter{}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}
	if strings.Contains(repo.Git("for-each-ref", "refs/peel/anchors/"), blob) {
		t.Error("clearing the run left its snapshot held")
	}
}
