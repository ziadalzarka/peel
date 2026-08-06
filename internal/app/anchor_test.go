package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// anchorRepo opens an app on a repository holding one committed file, with the
// working tree left as given.
func anchorRepo(t *testing.T, committed, working string) (*app.App, *gittest.Repo) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write("svc.go", committed)
	repo.Commit("initial")
	repo.Write("svc.go", working)

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a, repo
}

// note writes a comment anchored to a line of the working tree.
func note(t *testing.T, a *app.App, s *app.Session, line int, body string) store.Comment {
	t.Helper()
	c := store.Comment{
		File: "svc.go", Line: line, Side: store.SideNew,
		Origin: store.OriginWorktree, Body: body, Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(context.Background(), s, c)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	c.Blob = blob
	created, err := a.Local.Comments.Add(c)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return created
}

// partStaged leaves a file whose three versions all differ: one committed, a
// second in the index, a third on disk. Which of them a note is measured against
// is the whole of what origin and side decide.
func partStaged(t *testing.T) (*app.App, *gittest.Repo) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write("svc.go", "committed\nb\nc\nd\n")
	repo.Commit("initial")
	repo.Write("svc.go", "staged\nb\nc\nd\n")
	repo.Git("add", "svc.go")
	repo.Write("svc.go", "on disk\nb\nc\nd\n")

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a, repo
}

func TestSnapshotFreezesTheVersionTheLineCountsIn(t *testing.T) {
	ctx := context.Background()
	a, repo := partStaged(t)
	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}

	committed := repo.Git("rev-parse", "HEAD:svc.go")
	staged := repo.Git("rev-parse", ":svc.go")
	onDisk := repo.Git("hash-object", "svc.go")

	tests := []struct {
		name   string
		origin store.Origin
		side   store.Side
		want   string
		which  string
	}{
		// The staged diff runs HEAD → index, so its old side is the commit.
		{"a removal in the staged half", store.OriginIndex, store.SideOld, committed, "the committed file"},
		{"an addition in the staged half", store.OriginIndex, store.SideNew, staged, "the index"},
		// The unstaged diff runs index → working tree, so its old side is the
		// index and not the commit — the case easiest to get wrong.
		{"a removal in the unstaged half", store.OriginWorktree, store.SideOld, staged, "the index"},
		{"an addition in the unstaged half", store.OriginWorktree, store.SideNew, onDisk, "the file on disk"},
		// A note that names no origin is one written before the distinction, and
		// the file on disk is what it always meant.
		{"a note naming no origin", "", store.SideNew, onDisk, "the file on disk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.Snapshot(ctx, s, store.Comment{
				File: "svc.go", Line: 1, Side: tt.side, Origin: tt.origin,
			})
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if got != tt.want {
				t.Errorf("snapshot = %s, want %s (%s)", got, tt.want, tt.which)
			}
		})
	}
}

func TestSnapshotTakesNothingForANoteOnAWholeFile(t *testing.T) {
	ctx := context.Background()
	a, _ := partStaged(t)
	s, _ := a.LoadWorkingTree(ctx)

	// A note on the file as a whole has no line to keep track of, so freezing a
	// version of the file would hold an object open for nothing.
	got, err := a.Snapshot(ctx, s, store.Comment{File: "svc.go", Side: store.SideNew})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got != "" {
		t.Errorf("snapshot = %q, want none taken for a file-level note", got)
	}
}

func TestRelocateFollowsTheStagedHalfWithoutReadingTheDisk(t *testing.T) {
	ctx := context.Background()
	a, repo := partStaged(t)
	s, _ := a.LoadWorkingTree(ctx)

	// A note on line 3 of what is staged: "c".
	c := store.Comment{
		File: "svc.go", Line: 3, Side: store.SideNew, Origin: store.OriginIndex,
		Body: "note on the staged c", Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(ctx, s, c)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	c.Blob = blob
	if _, err := a.Local.Comments.Add(c); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// More is staged, pushing "c" down inside the index. The working tree is
	// left alone, so a note that read the disk instead would not move at all.
	repo.Write("svc.go", "staged\nEXTRA\nb\nc\nd\n")
	repo.Git("add", "svc.go")
	repo.Write("svc.go", "on disk\nb\nc\nd\n")

	s, _ = a.LoadWorkingTree(ctx)
	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Outdated {
		t.Fatal("the staged c is still there; the note must not read as outdated")
	}
	if got[0].Line != 4 {
		t.Errorf("line = %d, want 4 — where c sits in the index now", got[0].Line)
	}
}

func TestRelocateTellsTwoSnapshotsOfOneFileApart(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nd\n")
	s, _ := a.LoadWorkingTree(ctx)

	// One note written now, on "c" at line 3.
	early := note(t, a, s, 3, "note on c")

	// A line arrives above, then a second note is written on "d" — which is now
	// line 5, and measured against a different version of the file.
	repo.Write("svc.go", "NEW\na\nb\nc\nd\n")
	s, _ = a.LoadWorkingTree(ctx)
	late := note(t, a, s, 5, "note on d")
	if early.Blob == late.Blob {
		t.Fatal("the two notes share a snapshot; this test is not exercising anything")
	}

	// A third line arrives above both.
	repo.Write("svc.go", "NEWER\nNEW\na\nb\nc\nd\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	byID := map[string]store.Comment{}
	for _, c := range got {
		byID[c.ID] = c
	}
	// c is line 5 now, d is line 6 — each measured from its own snapshot.
	if line := byID[early.ID].Line; line != 5 {
		t.Errorf("the early note is on line %d, want 5 — where c sits now", line)
	}
	if line := byID[late.ID].Line; line != 6 {
		t.Errorf("the late note is on line %d, want 6 — where d sits now", line)
	}
}

func TestRelocateFollowsALineThatShiftedUp(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\nCHANGED\n")
	s, _ := a.LoadWorkingTree(ctx)
	note(t, a, s, 5, "note on e")

	// Two lines above it go away.
	repo.Write("svc.go", "a\nd\ne\nCHANGED\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Outdated {
		t.Fatal("e is still there; the note must not read as outdated")
	}
	if got[0].Line != 3 || got[0].MovedFrom != 5 {
		t.Errorf("note is on line %d (from %d), want line 3 from 5",
			got[0].Line, got[0].MovedFrom)
	}
}

func TestRelocateFollowsANoteLeftOnARemovedLine(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nGONE\nd\n", "a\nb\nd\n")
	s, _ := a.LoadWorkingTree(ctx)

	// A note on the line being deleted is numbered against the old side, which
	// for the unstaged diff is the index — not the file on disk.
	c := store.Comment{
		File: "svc.go", Line: 3, Side: store.SideOld, Origin: store.OriginWorktree,
		Body: "why is this going?", Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(ctx, s, c)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	c.Blob = blob
	if _, err := a.Local.Comments.Add(c); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The index gains a line above, moving GONE down within the old side.
	repo.Write("svc.go", "a\nEXTRA\nb\nGONE\nd\n")
	repo.Git("add", "svc.go")
	repo.Write("svc.go", "a\nEXTRA\nb\nd\n")

	s, _ = a.LoadWorkingTree(ctx)
	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Outdated {
		t.Fatal("GONE is still in the index; the note must not read as outdated")
	}
	if got[0].Line != 4 {
		t.Errorf("line = %d, want 4 — where GONE sits on the old side now", got[0].Line)
	}
}

func TestRelocateLeavesAPullRequestAlone(t *testing.T) {
	ctx := context.Background()
	a, _ := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nd\n")

	// A pull request is not in this working tree, so the local file says nothing
	// about where its lines are, and diffing against it would be nonsense.
	s := &app.Session{
		Target: "github:cli/cli#412", Title: "pr",
		PR:    &forge.PullRequest{},
		Files: []git.FileEntry{{Path: "svc.go"}},
	}
	got := a.Relocate(ctx, s, []store.Comment{{
		ID: "p1", File: "svc.go", Line: 3, Side: store.SideNew,
		Blob: "0000000000000000000000000000000000000000", Body: "note",
	}})
	if got[0].Line != 3 || got[0].Outdated {
		t.Errorf("comment = line %d, outdated %v; want it untouched",
			got[0].Line, got[0].Outdated)
	}
}

func TestSnapshotAnchorsAFileGitIsNotTrackingYet(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\n", "a\nb\n")
	repo.Write("brand-new.go", "one\ntwo\nthree\n")

	s, _ := a.LoadWorkingTree(ctx)
	c := store.Comment{
		File: "brand-new.go", Line: 2, Side: store.SideNew,
		Origin: store.OriginWorktree, Body: "on an untracked file", Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(ctx, s, c)
	if err != nil {
		t.Fatalf("Snapshot on an untracked file: %v", err)
	}
	c.Blob = blob
	if _, err := a.Local.Comments.Add(c); err != nil {
		t.Fatalf("Add: %v", err)
	}

	repo.Write("brand-new.go", "zero\none\ntwo\nthree\n")
	s, _ = a.LoadWorkingTree(ctx)
	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Line != 3 {
		t.Errorf("line = %d, want 3 — an untracked file's lines move like any other", got[0].Line)
	}
}

func TestRelocateLeavesANoteAloneWhenTheFileHasGone(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nCHANGED\n")
	s, _ := a.LoadWorkingTree(ctx)
	note(t, a, s, 3, "note on c")

	// There is no file left to diff against. Working out nothing is the right
	// answer; falling over, or inventing a line, is not.
	repo.Remove("svc.go")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if len(got) != 1 {
		t.Fatalf("comments = %d, want the note kept", len(got))
	}
	if got[0].Line != 3 {
		t.Errorf("line = %d, want the stored 3 left alone", got[0].Line)
	}
}

func TestRelocateIsStableWhenRunTwice(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nCHANGED\n")
	s, _ := a.LoadWorkingTree(ctx)
	note(t, a, s, 3, "note on c")

	repo.Write("svc.go", "NEW\na\nb\nc\nCHANGED\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	// Relocating is worked out from what is stored, never from the last answer,
	// so repeating it must not walk the line further each time.
	first := a.Relocate(ctx, s, all)
	second := a.Relocate(ctx, s, all)
	third := a.Relocate(ctx, s, first)
	if first[0].Line != 4 || second[0].Line != 4 {
		t.Errorf("lines = %d then %d, want 4 both times", first[0].Line, second[0].Line)
	}
	if third[0].Line != 4 {
		t.Errorf("relocating an already-relocated note gave %d, want 4 — it must not drift",
			third[0].Line)
	}
	if stored, _ := a.Local.Comments.List(store.Filter{}); stored[0].Line != 3 {
		t.Errorf("stored line = %d, want the original 3 left on disk", stored[0].Line)
	}
}

func TestRelocateFollowsALineThatShiftedDown(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	// The note is written on doWork(ctx), which is line 4 right now.
	note(t, a, s, 4, "pass a real ctx")

	// Two lines arrive above it, the way an agent adding an import would.
	repo.Write("svc.go", "package svc\n\nimport \"context\"\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")
	s, err = a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	all, err := a.Local.Comments.List(store.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := a.Relocate(ctx, s, all)
	if len(got) != 1 {
		t.Fatalf("comments = %d, want 1", len(got))
	}
	if got[0].Outdated {
		t.Fatal("the code is still there; the note must not read as outdated")
	}
	if got[0].Line != 6 {
		t.Errorf("line = %d, want 6 — the line doWork(ctx) moved to", got[0].Line)
	}
	if got[0].MovedFrom != 4 {
		t.Errorf("moved from = %d, want the 4 it was written on", got[0].MovedFrom)
	}
}

func TestRelocateMarksARewrittenLineOutdatedRatherThanMovingIt(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	note(t, a, s, 4, "pass a real ctx")

	// The commented line itself is replaced. There is no line to carry the note
	// to, and the number 4 now names something the reviewer never read.
	repo.Write("svc.go", "package svc\n\nfunc Run() {\n\tdoNothing()\n}\n")
	s, err = a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if !got[0].Outdated {
		t.Fatal("outdated = false, want the note to admit its code is gone")
	}
	if got[0].Line != 4 {
		t.Errorf("line = %d, want the 4 it was written on kept", got[0].Line)
	}
}

func TestRelocateLeavesAnUnchangedFileAlone(t *testing.T) {
	ctx := context.Background()
	a, _ := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, _ := a.LoadWorkingTree(ctx)
	note(t, a, s, 4, "pass a real ctx")

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Outdated || got[0].Line != 4 || got[0].MovedFrom != 0 {
		t.Errorf("comment = line %d, from %d, outdated %v; want it left at 4",
			got[0].Line, got[0].MovedFrom, got[0].Outdated)
	}
}

func TestRelocateLeavesANoteWithNoAnchorWhereItWas(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, _ := a.LoadWorkingTree(ctx)
	// A note written before peel took snapshots has no blob to measure from.
	if _, err := a.Local.Comments.Add(store.Comment{
		File: "svc.go", Line: 4, Side: store.SideNew,
		Origin: store.OriginWorktree, Body: "old note", Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	repo.Write("svc.go", "package svc\n\nimport \"context\"\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Line != 4 || got[0].Outdated {
		t.Errorf("comment = line %d, outdated %v; want it left exactly as stored",
			got[0].Line, got[0].Outdated)
	}
}

func TestKeepAnchorsHoldsSnapshotsAgainstGitCollection(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, _ := a.LoadWorkingTree(ctx)
	created := note(t, a, s, 4, "pass a real ctx")
	if created.Blob == "" {
		t.Fatal("the note was stored with no snapshot")
	}
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}

	// gc collects every object nothing points at. The snapshot has to survive
	// it, or the anchor rots and the note silently goes back to being a number.
	repo.Git("gc", "--prune=now", "--quiet")
	if _, err := repo.TryGit("cat-file", "-e", created.Blob); err != nil {
		t.Fatalf("gc collected the snapshot the note is anchored to: %v", err)
	}
}

func TestKeepAnchorsReleasesSnapshotsWhenTheNoteGoes(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, _ := a.LoadWorkingTree(ctx)
	created := note(t, a, s, 4, "pass a real ctx")
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}

	if err := a.Local.Comments.Remove(created.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors after remove: %v", err)
	}

	refs := repo.Git("for-each-ref", "--format=%(refname)", "refs/peel/")
	if strings.TrimSpace(refs) != "" {
		t.Errorf("refs left behind after the last note went: %q", refs)
	}
	// Nothing holds the blob now, so git is free to collect it — which is the
	// whole cleanup story: peel lets go, git reclaims.
	repo.Git("gc", "--prune=now", "--quiet")
	if _, err := repo.TryGit("cat-file", "-e", created.Blob); err == nil {
		t.Error("the snapshot outlived the note that needed it")
	}
}

func TestRelocateReadsWithoutWritingObjects(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t,
		"package svc\n\nfunc Run() {\n\tdoWork()\n}\n",
		"package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")

	s, _ := a.LoadWorkingTree(ctx)
	note(t, a, s, 4, "pass a real ctx")
	if err := a.KeepAnchors(ctx); err != nil {
		t.Fatalf("KeepAnchors: %v", err)
	}
	repo.Git("gc", "--prune=now", "--quiet")
	before := repo.Git("count-objects", "-v")

	// Follow mode re-reads constantly. If a read wrote an object each time, a
	// review left open would litter the repository it is reviewing.
	repo.Write("svc.go", "package svc\n\nimport \"context\"\n\nfunc Run() {\n\tdoWork(ctx)\n}\n")
	for range 5 {
		s, _ = a.LoadWorkingTree(ctx)
		all, _ := a.Local.Comments.List(store.Filter{})
		if got := a.Relocate(ctx, s, all); got[0].Line != 6 {
			t.Fatalf("line = %d, want 6", got[0].Line)
		}
	}

	if after := repo.Git("count-objects", "-v"); after != before {
		t.Errorf("reading the review wrote objects:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// runNote writes a comment on a run of the working tree's lines.
func runNote(t *testing.T, a *app.App, s *app.Session, line, end int, body string) store.Comment {
	t.Helper()
	c := store.Comment{
		File: "svc.go", Line: line, EndLine: end, Side: store.SideNew,
		Origin: store.OriginWorktree, Body: body, Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(context.Background(), s, c)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	c.Blob = blob
	created, err := a.Local.Comments.Add(c)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return created
}

// A note written on a run of lines is a note about all of them, so both ends go
// through the same mapping: an end left behind would name a stretch of the file
// growing or shrinking with every edit above it.
func TestRelocateCarriesBothEndsOfARun(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\n")
	s, _ := a.LoadWorkingTree(ctx)
	runNote(t, a, s, 3, 4, "c and d belong together")

	// Two lines arrive above the run.
	repo.Write("svc.go", "X\nY\na\nb\nc\nd\ne\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if got[0].Outdated {
		t.Fatal("c and d are both still there; the note must not read as outdated")
	}
	if got[0].Line != 5 || got[0].EndLine != 6 || got[0].MovedFrom != 3 {
		t.Errorf("note covers %d-%d (from %d), want 5-6 from 3",
			got[0].Line, got[0].EndLine, got[0].MovedFrom)
	}
}

// A run is a claim about a continuous stretch of code, so code arriving between
// its ends is not the note growing to cover it. Both numbers stay findable while
// what sits between them is no longer what was read.
func TestARunWithCodeInsertedInsideItIsOutdated(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\n")
	s, _ := a.LoadWorkingTree(ctx)
	runNote(t, a, s, 3, 4, "c and d belong together")

	// c and d are both still there, with something new between them.
	repo.Write("svc.go", "a\nb\nc\nBETWEEN\nd\ne\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if !got[0].Outdated {
		t.Error("the note reads as current, and now covers a line nobody wrote about")
	}
	if got[0].Line != 3 || got[0].EndLine != 4 {
		t.Errorf("note covers %d-%d, want the 3-4 it was written on", got[0].Line, got[0].EndLine)
	}
}

// The same the other way round: a run with a line taken out of the middle is
// shorter than the one that was read.
func TestARunWithALineTakenOutOfItIsOutdated(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\ne\nf\n", "a\nb\nc\nd\ne\nf\n")
	s, _ := a.LoadWorkingTree(ctx)
	runNote(t, a, s, 3, 5, "c through e")

	repo.Write("svc.go", "a\nb\nc\ne\nf\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if !got[0].Outdated {
		t.Error("the note reads as current, with a line out of the middle of what it covers")
	}
}

// Half a run is not a run. A note whose far end has been rewritten covers a
// stretch of code nobody read, and saying so is the only true thing left.
func TestARunWithOneEndRewrittenIsOutdated(t *testing.T) {
	ctx := context.Background()
	a, repo := anchorRepo(t, "a\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\n")
	s, _ := a.LoadWorkingTree(ctx)
	runNote(t, a, s, 3, 4, "c and d belong together")

	// c stays; d is rewritten out from under the note.
	repo.Write("svc.go", "a\nb\nc\nREWRITTEN\ne\n")
	s, _ = a.LoadWorkingTree(ctx)

	all, _ := a.Local.Comments.List(store.Filter{})
	got := a.Relocate(ctx, s, all)
	if !got[0].Outdated {
		t.Error("the note reads as current, with half the run it covers gone")
	}
	if got[0].Line != 3 || got[0].EndLine != 4 {
		t.Errorf("note covers %d-%d, want the 3-4 it was written on", got[0].Line, got[0].EndLine)
	}
}
