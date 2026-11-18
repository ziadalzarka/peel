package cli_test

import (
	"strings"
	"testing"
)

func TestWalkthroughSetKeepsStepOrderAndTheGeneratedCache(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.mustRun("walkthrough")

	const steps = `[
	  {"title":"Start with the output","text":"Read the output first.","file":"gen.json","line":1},
	  {"title":"Read the caller","text":"Follow the caller.","file":"a.txt","line":4},
	  {"title":"Return to the start","text":"Read the first branch.","file":"a.txt","line":2}
	]`
	h.stdin.WriteString(steps)
	h.mustRun("walkthrough", "set", "--author", "claude")

	check := func() {
		t.Helper()
		want := "## 1. Start with the output\n`gen.json:1`\n\nRead the output first.\n\n" +
			"## 2. Read the caller\n`a.txt:4`\n\nFollow the caller.\n\n" +
			"## 3. Return to the start\n`a.txt:2`\n\nRead the first branch.\n"
		if out := h.mustRun("walkthrough"); out != want {
			t.Errorf("walkthrough = %q, want %q", out, want)
		}
	}
	check()
	h.stdin.WriteString(strings.Replace(steps, `"line":2`, `"line":42`, 1))
	if code := h.run("walkthrough", "set", "--author", "claude"); code != 1 || !strings.Contains(h.err(), "step 3") {
		t.Fatalf("invalid anchor: exit %d, stderr %q", code, h.err())
	}
	h.mustRun("walkthrough", "clear", "--author", "someone-else")
	check()
	if h.ai.calls != 1 {
		t.Errorf("provider called %d times", h.ai.calls)
	}
	if out := h.mustRun("walkthrough", "--regen"); !strings.Contains(out, "It moves keys.") {
		t.Errorf("regenerated output = %q", out)
	}
	check()
	h.mustRun("walkthrough", "clear", "--author", "claude")
	if out := h.mustRun("walkthrough"); !strings.Contains(out, "It moves keys.") || h.ai.calls != 2 {
		t.Errorf("generated output after clear = %q, provider called %d times", out, h.ai.calls)
	}
}

func TestWalkthroughSetUsesTheSelectedRevision(t *testing.T) {
	h := newHarness(t)
	foldRepo(h)
	h.repo.Commit("change")
	h.stdin.WriteString(`[{"title":"Committed","text":"Read the second line.","file":"a.txt","line":2}]`)
	h.mustRun("--rev", "HEAD~1", "walkthrough", "set", "--author", "claude")

	if code := h.run("walkthrough"); code != 1 || !strings.Contains(h.err(), "no changes") {
		t.Errorf("working tree: exit %d, stderr %q", code, h.err())
	}
	base := strings.TrimSpace(h.repo.Git("rev-parse", "HEAD~1"))
	if out := h.mustRun("--rev", base, "walkthrough"); !strings.Contains(out, "Committed") || h.ai.calls != 0 {
		t.Errorf("revision walkthrough = %q, provider called %d times", out, h.ai.calls)
	}
	h.mustRun("--rev", "HEAD~1", "fold", "add", "--author", "claude", "--file", "a.txt", "--line", "2", "--end-line", "3")
	if out := h.mustRun("--rev", base, "fold", "list", "--json"); !strings.Contains(out, `"endLine": 3`) || strings.Contains(out, "outdated") {
		t.Errorf("revision folds = %q", out)
	}
}

func TestWalkthroughSetSupportsUntrackedFiles(t *testing.T) {
	h := newHarness(t)
	h.repo.Write("a.txt", "base\n")
	h.repo.Commit("base")
	h.repo.Write("b.txt", "new\n")
	h.stdin.WriteString(`[{"title":"New file","text":"Read the first line.","file":"b.txt","line":1}]`)
	h.mustRun("walkthrough", "set", "--author", "claude")
	if out := h.mustRun("walkthrough"); !strings.Contains(out, "New file") || h.ai.calls != 0 {
		t.Errorf("untracked walkthrough = %q, provider called %d times", out, h.ai.calls)
	}
	h.repo.Write("b.txt", "changed\n")
	if code := h.run("walkthrough"); code != 1 || !strings.Contains(h.err(), "no changes") {
		t.Errorf("stale walkthrough: exit %d, stderr %q", code, h.err())
	}
}
