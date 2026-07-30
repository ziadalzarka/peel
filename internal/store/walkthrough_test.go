package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWalkthroughSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peel", "walkthrough.json")
	c := NewJSONWalkthroughCache(path)

	want := Walkthrough{
		Target:      "",
		Fingerprint: "abc123",
		Provider:    "claude-code",
		Body:        "## What changed\n\nThis branch moves document keys off the model.",
	}
	if err := c.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported nothing cached")
	}
	if got.Body != want.Body || got.Fingerprint != want.Fingerprint || got.Provider != want.Provider {
		t.Errorf("Load returned %+v, want %+v", got, want)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Save did not set CreatedAt")
	}
}

func TestWalkthroughLoadMissing(t *testing.T) {
	c := NewJSONWalkthroughCache(filepath.Join(t.TempDir(), "walkthrough.json"))
	_, ok, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("Load reported a cache hit on a missing file")
	}
}

func TestWalkthroughCorruptCacheIsIgnored(t *testing.T) {
	// A corrupt cache should regenerate silently, not fail the command.
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, ok, err := NewJSONWalkthroughCache(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("Load reported a hit on a corrupt file")
	}
}

func TestWalkthroughFresh(t *testing.T) {
	w := Walkthrough{Target: "github:o/r#7", Fingerprint: "abc", Body: "text"}

	tests := []struct {
		name        string
		target      string
		fingerprint string
		want        bool
	}{
		{"same target and diff", "github:o/r#7", "abc", true},
		{"diff moved on", "github:o/r#7", "def", false},
		{"different target", "", "abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Fresh(tt.target, tt.fingerprint); got != tt.want {
				t.Errorf("Fresh() = %v, want %v", got, tt.want)
			}
		})
	}

	empty := Walkthrough{Target: "", Fingerprint: "abc"}
	if empty.Fresh("", "abc") {
		t.Error("an empty body counted as fresh")
	}
}

func TestWalkthroughClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	c := NewJSONWalkthroughCache(path)
	if err := c.Save(Walkthrough{Body: "text", Fingerprint: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := c.Load(); ok {
		t.Error("Load reported a hit after Clear")
	}
	// Clearing an already-absent cache is not an error.
	if err := c.Clear(); err != nil {
		t.Errorf("Clear on a missing file: %v", err)
	}
}

func TestWalkthroughOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	c := NewJSONWalkthroughCache(path, WithWalkthroughClock(func() time.Time {
		return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	}))

	if err := c.Save(Walkthrough{Body: "first", Fingerprint: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := c.Save(Walkthrough{Body: "second", Fingerprint: "b"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := c.Load()
	if err != nil || !ok {
		t.Fatalf("Load: %v ok=%v", err, ok)
	}
	if got.Body != "second" {
		t.Errorf("Body = %q, want the most recent narrative", got.Body)
	}
}

func TestFingerprintChangesWithDiff(t *testing.T) {
	a := Fingerprint("diff --git a/f b/f\n+one\n")
	b := Fingerprint("diff --git a/f b/f\n+two\n")
	if a == b {
		t.Error("different diffs produced the same fingerprint")
	}
	if a != Fingerprint("diff --git a/f b/f\n+one\n") {
		t.Error("Fingerprint is not deterministic")
	}
	if a == "" {
		t.Error("Fingerprint returned an empty string")
	}
}

var _ WalkthroughCache = (*JSONWalkthroughCache)(nil)

const groupedNarrative = "## 1. The parsed shape everything rests on\n" +
	"`internal/git/parse.go` · `internal/git/hunk.go`\n" +
	"\n" +
	"`ParseDiff` gains a `NoNewline` line kind, so a file whose last line\n" +
	"lost its newline round-trips.\n" +
	"\n" +
	"The **hunk** header keeps its own copy.\n" +
	"\n" +
	"## 2. What reads it\n" +
	"`internal/tui/render.go`\n" +
	"\n" +
	"The renderer dims the marker instead of colouring it.\n" +
	"\n" +
	"## Worth a close look\n" +
	"\n" +
	"- `internal/git/parse.go:88` — the fallback swallows a short read.\n"

func TestParseStepsGroupsFilesUnderTheirStep(t *testing.T) {
	steps := ParseSteps(groupedNarrative)

	if len(steps) != 3 {
		t.Fatalf("parsed %d steps, want 3: %+v", len(steps), steps)
	}

	first := steps[0]
	if first.Number != 1 {
		t.Errorf("first step number = %d, want 1", first.Number)
	}
	if first.Title != "The parsed shape everything rests on" {
		t.Errorf("first step title = %q", first.Title)
	}
	want := []string{"internal/git/parse.go", "internal/git/hunk.go"}
	if len(first.Files) != 2 || first.Files[0] != want[0] || first.Files[1] != want[1] {
		t.Errorf("first step files = %v, want %v", first.Files, want)
	}
	if strings.Contains(first.Body, "internal/git/hunk.go`\n") {
		t.Error("the path line leaked into the body")
	}
	if !strings.HasPrefix(first.Body, "`ParseDiff` gains") {
		t.Errorf("first step body starts %q", first.Body)
	}
	if !strings.Contains(first.Body, "The **hunk** header") {
		t.Error("the second paragraph was dropped")
	}
}

func TestParseStepsLeavesAClosingSectionUnnumbered(t *testing.T) {
	steps := ParseSteps(groupedNarrative)

	last := steps[len(steps)-1]
	if last.Number != 0 {
		t.Errorf("closing section number = %d, want 0 — it does not advance the reading order", last.Number)
	}
	if last.Title != "Worth a close look" {
		t.Errorf("closing section title = %q", last.Title)
	}
	if len(last.Files) != 0 {
		t.Errorf("closing section files = %v, want none: its bullet is prose, not a path line", last.Files)
	}
	if !strings.Contains(last.Body, "swallows a short read") {
		t.Errorf("closing section body = %q", last.Body)
	}
}

func TestParseStepsKeepsProseThatOpensWithAnIdentifier(t *testing.T) {
	steps := ParseSteps("## 1. A step\n\n`ParseDiff` is the entry point.\n")

	if len(steps) != 1 {
		t.Fatalf("parsed %d steps, want 1", len(steps))
	}
	if len(steps[0].Files) != 0 {
		t.Errorf("files = %v, want none — that line is a sentence, not a path list", steps[0].Files)
	}
	if steps[0].Body != "`ParseDiff` is the entry point." {
		t.Errorf("body = %q", steps[0].Body)
	}
}

func TestParseStepsFallsBackToOneStepWithoutHeadings(t *testing.T) {
	steps := ParseSteps("A provider that ignored the format entirely.\n")

	if len(steps) != 1 {
		t.Fatalf("parsed %d steps, want the whole narrative as one", len(steps))
	}
	if steps[0].Title != "" || steps[0].Number != 0 {
		t.Errorf("step = %+v, want an untitled one", steps[0])
	}
	if steps[0].Body != "A provider that ignored the format entirely." {
		t.Errorf("body = %q", steps[0].Body)
	}
}

func TestWalkthroughStepsReadsItsOwnBody(t *testing.T) {
	w := Walkthrough{Body: groupedNarrative}

	if got := len(w.Steps()); got != 3 {
		t.Errorf("Steps() = %d steps, want 3", got)
	}
	if len(Walkthrough{}.Steps()) != 0 {
		t.Error("an empty walkthrough parsed into steps")
	}
}
