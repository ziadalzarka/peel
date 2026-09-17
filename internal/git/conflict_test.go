package git_test

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// conflicted leaves the harness mid-merge over f.txt, with the working copy
// holding the markers git wrote into it.
func conflicted(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fixture.Write("f.txt", "a\nb\nc\nd\ne\nf\ng\n")
	h.fixture.Commit("base")
	h.fixture.Conflict("side",
		func(r *gittest.Repo) { r.Write("f.txt", "a\nb\nTHEIRS\nd\ne\nf\ng\n") },
		func(r *gittest.Repo) { r.Write("f.txt", "a\nb\nOURS\nd\ne\nf\ng\n") },
	)
	return h
}

func TestStatusReadsAConflictAsAChangeAgainstTheLastCommit(t *testing.T) {
	h := conflicted(t)

	e := h.entry("f.txt")
	if !e.Conflicted {
		t.Fatal("f.txt not marked conflicted")
	}
	if e.Staged != nil {
		t.Errorf("Staged = %+v, want nil — an unmerged path has no one version in the index", e.Staged)
	}
	if e.Unstaged == nil {
		t.Fatal("Unstaged = nil, want the merge git left on disk")
	}

	var added []string
	for _, hunk := range e.Unstaged.Hunks {
		for _, l := range hunk.Lines {
			if l.Kind == git.LineAdded {
				added = append(added, l.Text)
			}
		}
	}
	body := strings.Join(added, "\n")
	for _, want := range []string{"<<<<<<< HEAD", "=======", "THEIRS"} {
		if !strings.Contains(body, want) {
			t.Errorf("added lines %q do not show %q", body, want)
		}
	}
}

func TestStatusListsAConflictWithNothingToDiff(t *testing.T) {
	// Ours modified, theirs deleted: git leaves our version in the tree, so the
	// working copy matches the last commit and there is no diff at all. The file
	// is still waiting on a decision and still has to be listed.
	h := newHarness(t)
	h.fixture.Write("f.txt", "a\nb\nc\n")
	h.fixture.Commit("base")
	h.fixture.Conflict("side",
		func(r *gittest.Repo) { r.Git("rm", "--quiet", "f.txt") },
		func(r *gittest.Repo) { r.Write("f.txt", "a\nOURS\nc\n") },
	)

	e := h.entry("f.txt")
	if !e.Conflicted {
		t.Error("f.txt not marked conflicted")
	}
	if e.Unstaged != nil || e.Staged != nil {
		t.Errorf("entry has a diff (%+v / %+v), want neither", e.Staged, e.Unstaged)
	}
}

func TestStatusListsAConflictAlongsideOrdinaryChanges(t *testing.T) {
	// What used to fail outright: `git diff --cached` names the unmerged path in
	// among the files, so the marker falls in another file's hunk body. Both
	// neighbours are there to put it between two of them.
	h := newHarness(t)
	h.fixture.Write("a.txt", "one\ntwo\nthree\n")
	h.fixture.Write("m.txt", "x\ny\nz\n")
	h.fixture.Write("z.txt", "p\nq\nr\n")
	h.fixture.Commit("base")
	h.fixture.Conflict("side",
		func(r *gittest.Repo) { r.Write("m.txt", "x\nTHEIRS\nz\n") },
		func(r *gittest.Repo) { r.Write("m.txt", "x\nOURS\nz\n") },
	)

	h.fixture.Write("a.txt", "one\ntwo\nthree\nfour\n")
	h.fixture.Git("add", "a.txt")
	h.fixture.Write("z.txt", "p\nq\nr\ns\n")

	s := h.status()
	if got := pathsOf(s); len(got) != 3 {
		t.Fatalf("paths = %v, want all three", got)
	}
	if e, _ := s.Entry("a.txt"); e.State() != git.StateStaged {
		t.Errorf("a.txt = %v, want staged", e.State())
	}
	if e, _ := s.Entry("z.txt"); e.State() != git.StateUnstaged {
		t.Errorf("z.txt = %v, want unstaged", e.State())
	}
	if e, _ := s.Entry("m.txt"); !e.Conflicted {
		t.Error("m.txt not marked conflicted")
	}
}

func TestUnmergedPathsNamesEveryConflict(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("one.txt", "a\n")
	h.fixture.Write("two.txt", "a\n")
	h.fixture.Commit("base")
	h.fixture.Conflict("side",
		func(r *gittest.Repo) {
			r.Write("one.txt", "theirs\n")
			r.Write("two.txt", "theirs\n")
		},
		func(r *gittest.Repo) {
			r.Write("one.txt", "ours\n")
			r.Write("two.txt", "ours\n")
		},
	)

	paths, err := h.repo.UnmergedPaths(h.ctx)
	if err != nil {
		t.Fatalf("UnmergedPaths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "one.txt" || paths[1] != "two.txt" {
		t.Errorf("UnmergedPaths() = %v, want [one.txt two.txt] once each", paths)
	}
}

func TestStageHunkRefusesAConflict(t *testing.T) {
	h := conflicted(t)

	e := h.entry("f.txt")
	if len(e.Unstaged.Hunks) == 0 {
		t.Fatal("no hunk to try to stage")
	}
	err := h.stager.StageHunk(h.ctx, e.Unstaged.ID(e.Unstaged.Hunks[0]))
	if err == nil {
		t.Fatal("StageHunk on a conflict succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "merge conflict") {
		t.Errorf("StageHunk error = %q, want it to name the conflict", err)
	}
}

func TestStageFileRefusesMarkersAndTakesTheResolution(t *testing.T) {
	h := conflicted(t)

	err := h.stager.StageFile(h.ctx, "f.txt")
	if err == nil {
		t.Fatal("staged a file still holding conflict markers")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error = %q, want the line the first marker is on", err)
	}

	h.fixture.Write("f.txt", "a\nb\nOURS\nTHEIRS\nd\ne\nf\ng\n")
	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile after resolving: %v", err)
	}
	if lines := h.fixture.StatusLines(); len(lines) != 1 || !strings.HasPrefix(lines[0], "M ") {
		t.Errorf("status = %v, want f.txt resolved and staged", lines)
	}
}

func TestStageFileAllowsMarkersInAFileNoMergeTouched(t *testing.T) {
	// A file about merge conflicts is not a merge conflict.
	h := newHarness(t)
	h.fixture.Write("doc.md", "intro\n")
	h.fixture.Commit("base")
	h.fixture.Write("doc.md", "intro\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> other\n")

	if err := h.stager.StageFile(h.ctx, "doc.md"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.fixture.Staged("doc.md"); !strings.Contains(got, "<<<<<<< HEAD") {
		t.Errorf("index contents = %q, want the markers kept", got)
	}
}

func TestRevisionSessionLabelsAConflict(t *testing.T) {
	h := conflicted(t)
	base := strings.TrimSpace(h.fixture.Git("rev-parse", "HEAD~1"))

	s, err := h.repo.LoadStatusSince(h.ctx, base)
	if err != nil {
		t.Fatalf("LoadStatusSince: %v", err)
	}
	e, ok := s.Entry("f.txt")
	if !ok {
		t.Fatalf("f.txt missing: %v", pathsOf(s))
	}
	if !e.Conflicted {
		t.Error("f.txt not marked conflicted in a revision session")
	}
	if e.Unstaged == nil || len(e.Unstaged.Hunks) == 0 {
		t.Error("revision session shows no change for the conflict")
	}
}
