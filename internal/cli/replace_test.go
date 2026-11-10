package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func (h *harness) replace(input string, args ...string) (int, string) {
	h.t.Helper()
	h.stdin.Reset()
	h.stdin.WriteString(input)
	code := h.run(append(args, "replace", "--author", "claude")...)
	return code, h.out()
}

func (h *harness) mustReplace(input string, args ...string) string {
	h.t.Helper()
	code, out := h.replace(input, args...)
	if code != 0 {
		h.t.Fatalf("peel replace exited %d: %s", code, h.err())
	}
	return out
}

func (h *harness) commentIDsByBody(args ...string) map[string]string {
	h.t.Helper()
	var listed []map[string]any
	out := h.mustRun(append(args, "comment", "list", "--json")...)
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		h.t.Fatalf("comment list --json: %v\n%s", err, out)
	}
	ids := map[string]string{}
	for _, c := range listed {
		ids[c["author"].(string)+": "+c["body"].(string)] = c["id"].(string)
	}
	return ids
}

func (h *harness) foldsListed(args ...string) []foldOut {
	h.t.Helper()
	var listed []foldOut
	out := h.mustRun(append(args, "fold", "list", "--json")...)
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		h.t.Fatalf("fold list --json: %v\n%s", err, out)
	}
	return listed
}

func TestReplaceKeepsWhatDidNotChangeOnAPullRequest(t *testing.T) {
	h := newHarness(t)
	pr := []string{"--pr", "412"}
	h.mustRun("--pr", "412", "comment", "add", "--author", "someone", "--file", "pr.go", "--body", "theirs")
	h.mustRun("--pr", "412", "fold", "add", "--author", "someone", "--file", "pr.go")

	out := h.mustReplace(`{"comments": [
		{"file": "pr.go", "line": 1, "origin": "worktree", "body": "first"},
		{"file": "pr.go", "body": "second"}
	], "folds": [{"file": "pr.go", "line": 1, "label": "the body"}]}`, pr...)
	if !strings.Contains(out, "comments: 0 kept, 2 added, 0 removed") || !strings.Contains(out, "folds: 0 kept, 1 added, 0 removed") {
		t.Errorf("first replace said %q", out)
	}
	before := h.commentIDsByBody(pr...)
	foldsBefore := h.foldsListed(pr...)

	out = h.mustReplace(`{"comments": [
		{"file": "pr.go", "line": 1, "origin": "worktree", "body": "first"},
		{"file": "pr.go", "body": "third"}
	], "folds": [{"file": "pr.go", "line": 1, "label": "the body"}]}`, pr...)
	if !strings.Contains(out, "comments: 1 kept, 1 added, 1 removed") || !strings.Contains(out, "folds: 1 kept, 0 added, 0 removed") {
		t.Errorf("second replace said %q", out)
	}

	after := h.commentIDsByBody(pr...)
	if after["claude: first"] != before["claude: first"] {
		t.Errorf("the unchanged comment got a new id: %q, was %q", after["claude: first"], before["claude: first"])
	}
	if _, ok := after["claude: second"]; ok {
		t.Error("the comment left out of the second replace is still there")
	}
	if after["claude: third"] == "" || after["someone: theirs"] != before["someone: theirs"] {
		t.Errorf("comments after = %v", after)
	}
	if foldsAfter := h.foldsListed(pr...); len(foldsAfter) != 2 || !sameFolds(foldsAfter, foldsBefore) {
		t.Errorf("folds after = %+v, before = %+v", foldsAfter, foldsBefore)
	}
}

func sameFolds(a, b []foldOut) bool {
	ids := map[string]bool{}
	for _, f := range a {
		ids[f.ID] = true
	}
	for _, f := range b {
		if !ids[f.ID] {
			return false
		}
	}
	return len(a) == len(b)
}

func TestReplaceWorksOnTheWorkingTree(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	input := `{"comments": [{"file": "a.txt", "line": 2, "origin": "worktree", "body": "look here"}],
		"folds": [{"file": "a.txt", "line": 3, "endLine": 4, "label": "noise"}, {"file": "gen.json"}]}`

	h.mustReplace(input)
	out := h.mustReplace(input)
	if !strings.Contains(out, "comments: 1 kept, 0 added, 0 removed") || !strings.Contains(out, "folds: 2 kept, 0 added, 0 removed") {
		t.Errorf("an identical replace said %q", out)
	}
	if folds := h.foldsListed(); len(folds) != 2 {
		t.Errorf("folds = %+v", folds)
	}

	h.mustReplace(`{}`)
	if folds := h.foldsListed(); len(folds) != 0 {
		t.Errorf("an empty replace left folds %+v", folds)
	}
	if ids := h.commentIDsByBody(); len(ids) != 0 {
		t.Errorf("an empty replace left comments %v", ids)
	}
}

func TestReplaceLeavesOutWhatItCannotPlace(t *testing.T) {
	h := newHarness(t)
	code, _ := h.replace(`{"comments": [{"file": "pr.go", "line": 1, "body": " "}],
		"folds": [{"file": "other.go"}, {"file": "pr.go"}]}`, "--pr", "412")
	if code != 1 || !strings.Contains(h.err(), "left out 2 notes") || !strings.Contains(h.err(), "other.go") {
		t.Errorf("exit %d, stderr %q", code, h.err())
	}
	if folds := h.foldsListed("--pr", "412"); len(folds) != 1 || folds[0].File != "pr.go" {
		t.Errorf("folds = %+v, want the one on pr.go", folds)
	}
}

func TestReplaceRefusesBadInputWithoutTouchingTheReview(t *testing.T) {
	h := newHarness(t)
	h.mustRun("--pr", "412", "fold", "add", "--author", "claude", "--file", "pr.go")

	for name, input := range map[string]string{
		"empty stdin":   "",
		"unknown field": `{"notes": []}`,
		"not json":      "fold pr.go",
	} {
		if code, _ := h.replace(input, "--pr", "412"); code != 2 {
			t.Errorf("%s: exit %d, stderr %q", name, code, h.err())
		}
	}
	h.stdin.Reset()
	h.stdin.WriteString("{}")
	if code := h.run("--pr", "412", "replace"); code != 2 || !strings.Contains(h.err(), "--author") {
		t.Errorf("no author: exit %d, stderr %q", code, h.err())
	}
	if folds := h.foldsListed("--pr", "412"); len(folds) != 1 {
		t.Errorf("folds = %+v, want the one added before", folds)
	}
}
