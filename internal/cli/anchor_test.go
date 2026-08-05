package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// listed is the shape an agent reads out of `comment list --json`.
type listed struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	MovedFrom int    `json:"movedFrom"`
	Outdated  bool   `json:"outdated"`
	Body      string `json:"body"`
}

// review leaves one note on line 4 of a file mid-change, and returns the harness.
func review(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.repo.Write("svc.go", "a\nb\nc\nd\ne\n")
	h.repo.Commit("initial")
	h.repo.Write("svc.go", "a\nb\nc\nd\nCHANGED\n")
	h.mustRun("comment", "add", "--file", "svc.go", "--line", "4",
		"--origin", "worktree", "--body", "note on d")
	return h
}

func (h *harness) list(t *testing.T) []listed {
	t.Helper()
	var got []listed
	out := h.mustRun("comment", "list", "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse comment list --json: %v\n%s", err, out)
	}
	return got
}

func TestCommentListReportsWhereTheCodeIsNow(t *testing.T) {
	h := review(t)

	// The agent's own fix shifts every line below it — which is the loop that
	// used to hand out numbers measured against a file that no longer existed.
	h.repo.Write("svc.go", "a\nIMPORT\nb\nc\nd\nCHANGED\n")

	got := h.list(t)
	if len(got) != 1 {
		t.Fatalf("comments = %d, want 1", len(got))
	}
	if got[0].Line != 5 {
		t.Errorf("line = %d, want 5 — where d sits after the edit", got[0].Line)
	}
	if got[0].MovedFrom != 4 {
		t.Errorf("movedFrom = %d, want the 4 it was written on", got[0].MovedFrom)
	}
	if got[0].Outdated {
		t.Error("outdated = true, but d is still there")
	}
}

func TestCommentListSaysWhenTheCodeIsGone(t *testing.T) {
	h := review(t)

	// The commented line itself is rewritten. Something unrelated sits at 4 now.
	h.repo.Write("svc.go", "a\nb\nc\nREPLACED\nCHANGED\n")

	got := h.list(t)
	if !got[0].Outdated {
		t.Fatal("outdated = false; an agent would edit whatever took the line")
	}
	if got[0].Line != 4 {
		t.Errorf("line = %d, want the 4 it was written on kept", got[0].Line)
	}
	if got[0].MovedFrom != 0 {
		t.Errorf("movedFrom = %d, want none — it did not move, it went", got[0].MovedFrom)
	}

	// The human-readable table has to say it too, or the same mistake is one
	// `comment list` without --json away.
	if out := h.mustRun("comment", "list"); !strings.Contains(out, "(outdated)") {
		t.Errorf("comment list did not mark the note outdated:\n%s", out)
	}
}

func TestCommentListLeavesAnUnmovedNoteUnadorned(t *testing.T) {
	h := review(t)

	got := h.list(t)
	if got[0].Line != 4 || got[0].MovedFrom != 0 || got[0].Outdated {
		t.Errorf("comment = line %d, movedFrom %d, outdated %v; want a plain line 4",
			got[0].Line, got[0].MovedFrom, got[0].Outdated)
	}
	// movedFrom and outdated are omitempty, so a note that has not moved carries
	// neither — an agent should not have to reason about fields that mean "no".
	out := h.mustRun("comment", "list", "--json")
	for _, field := range []string{"movedFrom", "outdated"} {
		if strings.Contains(out, field) {
			t.Errorf("unmoved note carries %q:\n%s", field, out)
		}
	}
}

func TestCommentAddHoldsItsSnapshotAndLetsGoOnRemoval(t *testing.T) {
	h := review(t)

	refs := h.repo.Git("for-each-ref", "--format=%(refname)", "refs/peel/anchors")
	if strings.TrimSpace(refs) == "" {
		t.Fatal("no ref holding the snapshot; git gc would collect it")
	}

	// A second note on the same version of the same file shares the snapshot
	// rather than taking another, so the cost is per file version and not per
	// note.
	h.mustRun("comment", "add", "--file", "svc.go", "--line", "2",
		"--origin", "worktree", "--body", "second note")
	after := h.repo.Git("for-each-ref", "--format=%(refname)", "refs/peel/anchors")
	if after != refs {
		t.Errorf("a second note on the same file version took another snapshot:\n%s", after)
	}

	h.mustRun("comment", "clear")
	if left := h.repo.Git("for-each-ref", "refs/peel/"); strings.TrimSpace(left) != "" {
		t.Errorf("refs outlived the notes that needed them: %q", left)
	}
}
