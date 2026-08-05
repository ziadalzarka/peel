package git_test

import (
	"strings"
	"testing"
)

// A diff only carries three lines of context around a change. Reading the code
// either side of a hunk means going back to the file itself, and a part-staged
// file has two of those: the one on disk and the one git holds staged.

func TestWorkingLinesReadsTheFileOnDisk(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", numbered(4))

	lines, err := h.repo.WorkingLines("a.txt")
	if err != nil {
		t.Fatalf("WorkingLines: %v", err)
	}
	want := []string{"line1", "line2", "line3", "line4"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

// A file's last line without a newline after it is still a line.
func TestWorkingLinesKeepsAnUnterminatedLastLine(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "one\ntwo")

	lines, err := h.repo.WorkingLines("a.txt")
	if err != nil {
		t.Fatalf("WorkingLines: %v", err)
	}
	if len(lines) != 2 || lines[1] != "two" {
		t.Errorf("lines = %q, want [one two]", lines)
	}
}

func TestWorkingLinesFailsOnAFileThatIsNotThere(t *testing.T) {
	h := newHarness(t)

	if _, err := h.repo.WorkingLines("missing.txt"); err == nil {
		t.Error("reading a file that is not there succeeded")
	}
}

// The staged copy is a different file from the one on disk, and this is the
// case that proves it: git holds the committed-then-staged text while the
// working tree has moved on.
func TestIndexLinesReadsWhatGitHoldsStaged(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", numbered(3))
	h.fixture.Commit("start")
	h.fixture.Write("a.txt", "line1\nstaged\nline3\n")
	h.fixture.Git("add", "a.txt")
	h.fixture.Write("a.txt", "line1\nstaged\non disk\n")

	staged, err := h.repo.IndexLines(h.ctx, "a.txt")
	if err != nil {
		t.Fatalf("IndexLines: %v", err)
	}
	if got := strings.Join(staged, "|"); got != "line1|staged|line3" {
		t.Errorf("staged lines = %q, want line1|staged|line3", got)
	}

	working, err := h.repo.WorkingLines("a.txt")
	if err != nil {
		t.Fatalf("WorkingLines: %v", err)
	}
	if got := strings.Join(working, "|"); got != "line1|staged|on disk" {
		t.Errorf("working lines = %q, want line1|staged|on disk", got)
	}
}

func TestIndexLinesFailsOnAPathGitDoesNotHold(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", numbered(2))
	h.fixture.Commit("start")

	if _, err := h.repo.IndexLines(h.ctx, "untracked.txt"); err == nil {
		t.Error("reading a path git does not hold succeeded")
	}
}
