package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
)

const twoHunkPR = `diff --git a/pr.go b/pr.go
--- a/pr.go
+++ b/pr.go
@@ -1,3 +1,3 @@
 package pr
-var a = 1
+var a = 2

@@ -10,3 +10,3 @@ func B() {
 	x := 1
-	return x
+	return x + 1
 }
diff --git a/logo.png b/logo.png
Binary files a/logo.png and b/logo.png differ
`

const pushedPR = `diff --git a/pr.go b/pr.go
--- a/pr.go
+++ b/pr.go
@@ -1,3 +1,4 @@
 package pr
-var a = 1
+var a = 3
+var extra = true

@@ -10,3 +11,3 @@ func B() {
 	x := 1
-	return x
+	return x + 1
 }
diff --git a/logo.png b/logo.png
Binary files a/logo.png and b/logo.png differ
`

func prSessionOf(t *testing.T, diff string) *app.Session {
	t.Helper()
	parsed, err := git.ParseDiff(diff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	var files []git.FileEntry
	for i := range parsed.Files {
		f := parsed.Files[i]
		files = append(files, git.FileEntry{Path: f.Path(), Unstaged: &f})
	}
	return &app.Session{
		Target:    "github:o/r#7",
		Stageable: true,
		Files:     files,
		PR:        &forge.PullRequest{Ref: forge.Ref{Owner: "o", Repo: "r", Number: 7}, Diff: diff},
	}
}

func viewedSession(t *testing.T, v *app.Viewer, s *app.Session) *app.Session {
	t.Helper()
	got, err := v.Session(s)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	return got
}

func entryOf(t *testing.T, s *app.Session, path string) git.FileEntry {
	t.Helper()
	e, ok := s.Entry(path)
	if !ok {
		t.Fatalf("%s is not in the session", path)
	}
	return e
}

func TestViewingAFileMovesAllOfItToTheViewedSide(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	ctx := context.Background()
	s := prSessionOf(t, twoHunkPR)
	v := f.app.Viewer(s)

	if err := v.StageFile(ctx, "pr.go"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	got := entryOf(t, viewedSession(t, v, s), "pr.go")
	if got.State() != git.StateStaged || len(got.Staged.Hunks) != 2 {
		t.Fatalf("pr.go = %v with %v, want both hunks viewed", got.State(), got.Staged)
	}
	if entryOf(t, viewedSession(t, v, s), "logo.png").State() != git.StateUnstaged {
		t.Error("viewing pr.go marked logo.png too")
	}

	again := entryOf(t, viewedSession(t, f.app.Viewer(s), s), "pr.go")
	if again.State() != git.StateStaged {
		t.Error("a second viewer over the same pull request lost what was viewed")
	}
}

func TestViewingOneHunkLeavesTheOtherToView(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	ctx := context.Background()
	s := prSessionOf(t, twoHunkPR)
	v := f.app.Viewer(s)

	first := s.Files[0].Unstaged
	if err := v.StageHunk(ctx, first.ID(first.Hunks[1])); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	got := entryOf(t, viewedSession(t, v, s), "pr.go")
	if got.State() != git.StatePartial {
		t.Fatalf("pr.go = %v, want part viewed", got.State())
	}
	if got.Staged.Hunks[0].NewStart != 10 || got.Unstaged.Hunks[0].NewStart != 1 {
		t.Errorf("viewed %v, left %v, want the second hunk viewed with its numbers kept", got.Staged.Hunks, got.Unstaged.Hunks)
	}
}

func TestAPushBringsBackOnlyTheHunksItChanged(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	ctx := context.Background()
	before := prSessionOf(t, twoHunkPR)
	if err := f.app.Viewer(before).StageAll(ctx); err != nil {
		t.Fatalf("StageAll: %v", err)
	}

	after := prSessionOf(t, pushedPR)
	got := viewedSession(t, f.app.Viewer(after), after)

	code := entryOf(t, got, "pr.go")
	if code.State() != git.StatePartial {
		t.Fatalf("pr.go = %v, want the rewritten hunk back to view and the other still viewed", code.State())
	}
	if !strings.Contains(code.Unstaged.Hunks[0].Lines[2].Text, "var a = 3") {
		t.Errorf("left to view %v, want the rewritten hunk", code.Unstaged.Hunks)
	}
	if code.Staged.Hunks[0].NewStart != 11 {
		t.Errorf("viewed %v, want the untouched hunk kept though it moved down a line", code.Staged.Hunks)
	}
	if entryOf(t, got, "logo.png").State() != git.StateStaged {
		t.Error("an unchanged binary file lost its viewed mark")
	}
}

func TestIdenticalHunksAreViewedOneAtATime(t *testing.T) {
	diff := `diff --git a/twice.go b/twice.go
--- a/twice.go
+++ b/twice.go
@@ -1,1 +1,1 @@
-x
+y
@@ -9,1 +9,1 @@
-x
+y
`
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	s := prSessionOf(t, diff)
	v := f.app.Viewer(s)

	d := s.Files[0].Unstaged
	if err := v.StageHunk(context.Background(), d.ID(d.Hunks[0])); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	if got := entryOf(t, viewedSession(t, v, s), "twice.go"); got.State() != git.StatePartial {
		t.Errorf("twice.go = %v, want only the first of two identical hunks viewed", got.State())
	}
}

func TestTakingBackAViewPutsTheListBack(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	ctx := context.Background()
	s := prSessionOf(t, twoHunkPR)
	v := f.app.Viewer(s)

	before, err := v.IndexTree(ctx)
	if err != nil {
		t.Fatalf("IndexTree: %v", err)
	}
	if err := v.StageFile(ctx, "logo.png"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	after, _ := v.IndexTree(ctx)
	if after == before {
		t.Fatal("viewing a file did not change what the list reads as")
	}

	if err := v.RestoreIndex(ctx, after, before); err != nil {
		t.Fatalf("RestoreIndex: %v", err)
	}
	if entryOf(t, viewedSession(t, v, s), "logo.png").State() != git.StateUnstaged {
		t.Error("taking the view back left logo.png viewed")
	}

	if err := v.StageFile(ctx, "pr.go"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if err := v.RestoreIndex(ctx, after, before); err == nil {
		t.Error("a put-back over a list that has moved on was allowed")
	}
}

func TestUnviewingAFileOpensAllOfIt(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	ctx := context.Background()
	s := prSessionOf(t, twoHunkPR)
	v := f.app.Viewer(s)

	if err := v.StageAll(ctx); err != nil {
		t.Fatalf("StageAll: %v", err)
	}
	if err := v.UnstageFile(ctx, "pr.go"); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}
	got := viewedSession(t, v, s)
	if entryOf(t, got, "pr.go").State() != git.StateUnstaged || entryOf(t, got, "logo.png").State() != git.StateStaged {
		t.Errorf("pr.go = %v, logo.png = %v", entryOf(t, got, "pr.go").State(), entryOf(t, got, "logo.png").State())
	}

	if err := v.UnstageAll(ctx); err != nil {
		t.Fatalf("UnstageAll: %v", err)
	}
	if entryOf(t, viewedSession(t, v, s), "logo.png").State() != git.StateUnstaged {
		t.Error("unmarking everything left logo.png viewed")
	}
}

func TestAWorkingTreeHasNoViewer(t *testing.T) {
	f := newFixture(t, app.WithGlobalDir(t.TempDir()))
	if v := f.app.Viewer(&app.Session{Title: "working tree", Stageable: true}); v != nil {
		t.Error("the working tree was given a viewed list instead of the index")
	}
}

func TestFileAndHunkViewedPredictTheSplit(t *testing.T) {
	s := prSessionOf(t, twoHunkPR)
	entry := s.Files[0]
	d := entry.Unstaged

	part, ok := app.HunkViewed(entry, d.ID(d.Hunks[1]))
	if !ok || part.State() != git.StatePartial {
		t.Fatalf("HunkViewed = %v, %v", part.State(), ok)
	}
	whole := app.FileViewed(part, true)
	if whole.State() != git.StateStaged || whole.Staged.Hunks[0].NewStart != 1 || whole.Staged.Hunks[1].NewStart != 10 {
		t.Errorf("FileViewed = %v %v, want both hunks viewed in file order", whole.State(), whole.Staged)
	}
	if back := app.FileViewed(whole, false); back.State() != git.StateUnstaged || len(back.Unstaged.Hunks) != 2 {
		t.Errorf("FileViewed(false) = %v", back.State())
	}
}
