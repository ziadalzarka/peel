package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func foldRepo(h *harness) {
	h.t.Helper()
	h.repo.Write("a.txt", "one\ntwo\nthree\nfour\nfive\n")
	h.repo.Write("gen.json", "{}\n")
	h.repo.Commit("base")
	h.repo.Write("a.txt", "one\nTWO\nTHREE\nFOUR\nfive\n")
	h.repo.Write("gen.json", "{\"a\": 1}\n")
}

type foldOut struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	EndLine  int    `json:"endLine"`
	Label    string `json:"label"`
	Author   string `json:"author"`
	Created  string `json:"createdAt"`
	Moved    int    `json:"movedFrom"`
	Outdated bool   `json:"outdated"`
}

func TestFoldAddListAndRemove(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)

	var added foldOut
	out := h.mustRun("fold", "add", "--author", "claude", "--file", "a.txt", "--line", "2", "--end-line", "4",
		"--label", "test body", "--json")
	if err := json.Unmarshal([]byte(out), &added); err != nil {
		t.Fatalf("fold add --json: %v\n%s", err, out)
	}
	if added.ID == "" || added.File != "a.txt" || added.Line != 2 || added.EndLine != 4 ||
		added.Label != "test body" || added.Author != "claude" || added.Created == "" {
		t.Errorf("added = %+v", added)
	}

	out = h.mustRun("fold", "add", "--author", "claude", "--file", "gen.json")
	if !strings.Contains(out, "on gen.json") {
		t.Errorf("fold add output = %q", out)
	}

	out = h.mustRun("fold", "list")
	if !strings.Contains(out, "a.txt:2-4") || !strings.Contains(out, "test body") || !strings.Contains(out, "gen.json") {
		t.Errorf("fold list = %q", out)
	}

	var listed []foldOut
	if err := json.Unmarshal([]byte(h.mustRun("fold", "list", "--json", "--file", "gen.json")), &listed); err != nil {
		t.Fatalf("fold list --json: %v", err)
	}
	if len(listed) != 1 || listed[0].Line != 0 || listed[0].File != "gen.json" {
		t.Errorf("listed = %+v", listed)
	}

	h.mustRun("fold", "rm", added.ID)
	if out := h.mustRun("fold", "list", "--file", "a.txt"); !strings.Contains(out, "no folds") {
		t.Errorf("fold list after rm = %q", out)
	}
}

func TestFoldListSaysWhereARunMovedAndWhenItStoppedHiding(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.mustRun("fold", "add", "--author", "claude", "--file", "a.txt", "--line", "2", "--end-line", "4")

	h.repo.Write("a.txt", "zero\none\nTWO\nTHREE\nFOUR\nfive\n")
	var listed []foldOut
	if err := json.Unmarshal([]byte(h.mustRun("fold", "list", "--json")), &listed); err != nil {
		t.Fatalf("fold list --json: %v", err)
	}
	if listed[0].Line != 3 || listed[0].EndLine != 5 || listed[0].Moved != 2 || listed[0].Outdated {
		t.Errorf("after moving: %+v", listed[0])
	}

	h.repo.Write("a.txt", "zero\none\nTWO\nREWRITTEN\nFOUR\nfive\n")
	if err := json.Unmarshal([]byte(h.mustRun("fold", "list", "--json")), &listed); err != nil {
		t.Fatalf("fold list --json: %v", err)
	}
	if !listed[0].Outdated {
		t.Errorf("after a rewrite: %+v", listed[0])
	}
	if out := h.mustRun("fold", "list"); !strings.Contains(out, "no longer hiding") {
		t.Errorf("fold list = %q, want it to say the run stopped hiding", out)
	}
}

func TestFoldClearByAuthorLeavesOtherFolds(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.mustRun("fold", "add", "--author", "claude", "--file", "a.txt", "--line", "2")
	h.mustRun("fold", "add", "--author", "claude", "--file", "gen.json")
	h.mustRun("fold", "add", "--author", "ziad", "--file", "a.txt", "--line", "3")

	if out := h.mustRun("fold", "clear", "--author", "claude"); !strings.Contains(out, "cleared 2 folds") {
		t.Errorf("fold clear = %q", out)
	}
	if out := h.mustRun("fold", "list", "--author", "ziad"); !strings.Contains(out, "a.txt:3") {
		t.Errorf("the other author's fold went too: %q", out)
	}
	if out := h.mustRun("fold", "list", "--author", "claude"); !strings.Contains(out, "no folds") {
		t.Errorf("fold list --author claude = %q", out)
	}
	if out := h.mustRun("fold", "clear"); !strings.Contains(out, "cleared 1 fold") {
		t.Errorf("fold clear with no author = %q", out)
	}
}

func TestFoldAddWithoutAnAuthorIsSignedUnknown(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	var added foldOut
	if err := json.Unmarshal([]byte(h.mustRun("fold", "add", "--file", "gen.json", "--json")), &added); err != nil {
		t.Fatalf("fold add --json: %v", err)
	}
	if added.Author != "unknown" {
		t.Errorf("author = %q, want unknown", added.Author)
	}
}

func TestFoldAddCleansThePathAndTheLabel(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	var added foldOut
	out := h.mustRun("fold", "add", "--file", "./gen.json", "--label", "two\nlines  here", "--json")
	if err := json.Unmarshal([]byte(out), &added); err != nil {
		t.Fatalf("fold add --json: %v", err)
	}
	if added.File != "gen.json" || added.Label != "two lines here" {
		t.Errorf("added = %+v", added)
	}
}

func TestFoldUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no subcommand":      {"fold"},
		"unknown subcommand": {"fold", "hide"},
		"no file":            {"fold", "add", "--line", "1"},
		"negative line":      {"fold", "add", "--file", "a.txt", "--line", "-1"},
		"end with no line":   {"fold", "add", "--file", "a.txt", "--end-line", "3"},
		"end before line":    {"fold", "add", "--file", "a.txt", "--line", "3", "--end-line", "2"},
		"author not a name":  {"fold", "add", "--file", "a.txt", "--author", " x"},
		"stray argument":     {"fold", "add", "--file", "a.txt", "extra"},
		"rm with no id":      {"fold", "rm"},
		"list author":        {"fold", "list", "--author", " x"},
		"clear author":       {"fold", "clear", "--author", " x"},
	} {
		h := newHarness(t)
		foldRepo(h)
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, h.err())
		}
	}
}

func TestFoldReportsWhatItCannotHide(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	if code := h.run("fold", "add", "--file", "untouched.txt"); code != 1 || !strings.Contains(h.err(), "no changes") {
		t.Errorf("exit %d, stderr %q", code, h.err())
	}
	if code := h.run("fold", "rm", "nope"); code != 1 || !strings.Contains(h.err(), "fold not found") {
		t.Errorf("exit %d, stderr %q", code, h.err())
	}
}

func TestFoldWorksOnAPullRequest(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.mustRun("--pr", "412", "fold", "add", "--author", "claude", "--file", "pr.go", "--line", "1", "--label", "body")
	h.mustRun("--pr", "412", "fold", "add", "--author", "someone", "--file", "pr.go")

	var runs []foldOut
	if err := json.Unmarshal([]byte(h.mustRun("--pr", "412", "fold", "list", "--json")), &runs); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want two", runs)
	}

	tree := h.mustRun("fold", "list")
	if !strings.Contains(tree, "no folds") {
		t.Errorf("the working tree's folds = %q, want none", tree)
	}

	if out := h.mustRun("--pr", "412", "fold", "clear", "--author", "claude"); !strings.Contains(out, "cleared 1 fold") {
		t.Errorf("clear said %q", out)
	}
	var left []foldOut
	if err := json.Unmarshal([]byte(h.mustRun("--pr", "412", "fold", "list", "--json")), &left); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(left) != 1 || left[0].Author != "someone" {
		t.Fatalf("left = %+v, want the other author's", left)
	}
	h.mustRun("--pr", "412", "fold", "rm", left[0].ID)
}

func TestFoldOutsideThePullRequestIsRefused(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	if code := h.run("--pr", "412", "fold", "add", "--file", "other.go"); code == 0 {
		t.Error("a file with no changes was accepted")
	}
}

func TestGotoAsksTheOpenReviewToMove(t *testing.T) {
	h := newHarness(t)
	if out := h.mustRun("--pr", "412", "goto", "pr.go:3"); !strings.Contains(out, "pr.go:3") {
		t.Errorf("goto said %q", out)
	}
	h.mustRun("goto", "a.txt")
	for _, args := range [][]string{{"goto"}, {"goto", ":3"}, {"goto", "a.txt:0"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d, stderr %q", args, code, h.err())
		}
	}
}

func TestFoldWorksUnderARevision(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.repo.Commit("first")
	h.repo.Write("a.txt", "one\nTWO\nTHREE\nFOUR\nfive\nsix\n")

	h.mustRun("--rev", "HEAD~1", "fold", "add", "--author", "claude", "--file", "a.txt", "--line", "2", "--end-line", "4")
	var listed []foldOut
	if err := json.Unmarshal([]byte(h.mustRun("fold", "list", "--json")), &listed); err != nil {
		t.Fatalf("fold list --json: %v", err)
	}
	if len(listed) != 1 || listed[0].Outdated || listed[0].Line != 2 {
		t.Errorf("listed = %+v", listed)
	}
}

func TestHelpMentionsFolds(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("--help")
	for _, want := range []string{"fold <add|list|rm|clear>", "review mode", "A commented line is never hidden"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}
