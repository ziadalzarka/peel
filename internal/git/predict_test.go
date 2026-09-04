package git_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/git"
)

// show renders everything the review draws a file from: which side holds what,
// each hunk's header, and every line with the numbers it carries.
func show(e git.FileEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %v\n", e.Path, e.State())
	for _, side := range []struct {
		name string
		diff *git.FileDiff
	}{{"index", e.Staged}, {"worktree", e.Unstaged}} {
		if side.diff == nil {
			fmt.Fprintf(&b, "%s: nothing\n", side.name)
			continue
		}
		fmt.Fprintf(&b, "%s: %s %s\n", side.name, side.diff.Status, side.diff.Path())
		for _, h := range side.diff.Hunks {
			fmt.Fprintf(&b, "  %s\n", h.Header())
			for _, l := range h.Lines {
				fmt.Fprintf(&b, "    %3d %3d %s\n", l.OldLine, l.NewLine, l.Render())
			}
		}
	}
	return b.String()
}

// The screen is drawn from what the prediction says and corrected by what git
// says, so a reviewer only ever sees the difference between them. Here there is
// none: each case works out the file, stages the hunk for real, and reads back
// what git makes of it.
func TestPredictingAStagedHunkAgreesWithGit(t *testing.T) {
	tests := []struct {
		name  string
		build func(h *harness)
		hunk  int
	}{
		{
			name: "the first of two hunks",
			build: func(h *harness) {
				h.commit("f.txt", numbered(30))
				h.write("f.txt", edited(numbered(30), "line3", "line25"))
			},
			hunk: 0,
		},
		{
			name: "the second of two hunks",
			build: func(h *harness) {
				h.commit("f.txt", numbered(30))
				h.write("f.txt", edited(numbered(30), "line3", "line25"))
			},
			hunk: 1,
		},
		{
			name: "the only hunk there is",
			build: func(h *harness) {
				h.commit("f.txt", numbered(10))
				h.write("f.txt", edited(numbered(10), "line4"))
			},
			hunk: 0,
		},
		{
			name: "a hunk that only adds lines",
			build: func(h *harness) {
				h.commit("f.txt", numbered(30))
				h.write("f.txt", strings.Replace(numbered(30), "line5\n", "line5\nADDED-A\nADDED-B\n", 1))
			},
			hunk: 0,
		},
		{
			name: "a hunk that only removes lines",
			build: func(h *harness) {
				h.commit("f.txt", numbered(30))
				h.write("f.txt", strings.Replace(numbered(30), "line5\nline6\n", "", 1))
			},
			hunk: 0,
		},
		{
			name: "a hunk under one already staged",
			build: func(h *harness) {
				h.commit("f.txt", numbered(40))
				h.write("f.txt", strings.Replace(numbered(40), "line3\n", "line3\nSTAGED-A\nSTAGED-B\n", 1))
				h.fixture.Git("add", "f.txt")
				h.write("f.txt", edited(h.read("f.txt"), "line30"))
			},
			hunk: 0,
		},
		{
			name: "a hunk over one already staged",
			build: func(h *harness) {
				h.commit("f.txt", numbered(40))
				h.write("f.txt", strings.Replace(numbered(40), "line30\n", "line30\nSTAGED-A\nSTAGED-B\n", 1))
				h.fixture.Git("add", "f.txt")
				h.write("f.txt", edited(h.read("f.txt"), "line3"))
			},
			hunk: 0,
		},
		{
			name: "a file that ends without a newline",
			build: func(h *harness) {
				h.commit("f.txt", "one\ntwo\nthree")
				h.write("f.txt", "one\ntwo\nTHREE")
			},
			hunk: 0,
		},
		{
			name: "the whole file, deleted",
			build: func(h *harness) {
				h.commit("f.txt", numbered(6))
				h.fixture.Remove("f.txt")
			},
			hunk: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.build(h)

			before := h.entry("f.txt")
			file, hunks := h.unstagedHunks("f.txt")
			if tt.hunk >= len(hunks) {
				t.Fatalf("the file has %d hunks, wanted number %d", len(hunks), tt.hunk)
			}
			id := file.ID(hunks[tt.hunk])

			want, ok := before.WithHunkStaged(id)
			if !ok {
				t.Fatalf("nothing predicted for %v", id)
			}
			if err := h.stager.StageHunk(h.ctx, id); err != nil {
				t.Fatalf("StageHunk: %v", err)
			}

			got := h.entry("f.txt")
			if show(got) != show(want) {
				t.Errorf("the prediction and git disagree:\n--- git ---\n%s\n--- predicted ---\n%s", show(got), show(want))
			}
		})
	}
}

// A hunk the file no longer has is the screen being older than the tree, which
// is not something to guess at: the read-back is the only thing that can say.
func TestNothingIsPredictedForAHunkTheFileHasNot(t *testing.T) {
	h := newHarness(t)
	h.commit("f.txt", numbered(10))
	h.write("f.txt", edited(numbered(10), "line4"))

	entry := h.entry("f.txt")
	stale := git.HunkID{Path: "f.txt", OldStart: 99, OldCount: 3, NewStart: 99, NewCount: 3}
	if _, ok := entry.WithHunkStaged(stale); ok {
		t.Error("a hunk that is not in the file was predicted anyway")
	}
}

// An untracked file cannot have one hunk staged at all, so there is nothing to
// draw ahead of a write that will be refused.
func TestNothingIsPredictedForAnUntrackedFile(t *testing.T) {
	h := newHarness(t)
	h.commit("kept.txt", "a\n")
	h.write("new.txt", numbered(4))

	entry := h.entry("new.txt")
	file, hunks := h.unstagedHunks("new.txt")
	if _, ok := entry.WithHunkStaged(file.ID(hunks[0])); ok {
		t.Error("an untracked file was predicted into the index")
	}
}

// edited replaces each of the named lines with an upper-cased version of itself,
// which is a change git reads as one hunk per line named.
func edited(content string, lines ...string) string {
	for _, line := range lines {
		content = strings.Replace(content, line+"\n", strings.ToUpper(line)+"\n", 1)
	}
	return content
}
