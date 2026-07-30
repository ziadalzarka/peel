package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/store"
)

// stubProvider is a Provider whose behaviour tests control directly.
type stubProvider struct {
	name      string
	available bool
	body      string
	err       error
	calls     int
}

func (s *stubProvider) Name() string        { return s.name }
func (s *stubProvider) Description() string { return "stub provider" }

func (s *stubProvider) Available(context.Context) bool { return s.available }

func (s *stubProvider) Walkthrough(context.Context, Request) (string, error) {
	s.calls++
	return s.body, s.err
}

func TestNewRegistryWiresProvidersThrough(t *testing.T) {
	// The registry itself is covered in internal/registry; this checks the AI
	// package's constructor produces one that resolves its own Provider type.
	missing := &stubProvider{name: "missing", available: false}
	present := &stubProvider{name: "present", available: true}

	got, err := NewRegistry(missing, present).Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name() != "present" {
		t.Errorf("Resolve() = %q, want the first available provider", got.Name())
	}
	if _, err := got.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("resolved provider is not usable: %v", err)
	}
}

func TestRegistryReportsNoProvider(t *testing.T) {
	_, err := NewRegistry(&stubProvider{name: "a", available: false}).Resolve(context.Background(), "")
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("error = %v, want ErrNoProvider", err)
	}
}

func TestBuildPromptIncludesDiffAndContext(t *testing.T) {
	got := BuildPrompt(Request{
		Diff:   "diff --git a/f.go b/f.go\n+added\n",
		Target: "pull request #412",
		Files:  []string{"f.go", "g.go"},
	})

	for _, want := range []string{
		"pull request #412",
		"Files changed (2)",
		"- f.go",
		"- g.go",
		"+added",
		"```diff",
		"## Worth a close look",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestBuildPromptCustomInstruction(t *testing.T) {
	got := BuildPrompt(Request{Diff: "x", Instruction: "Just list the files."})
	if !strings.Contains(got, "Just list the files.") {
		t.Error("custom instruction not used")
	}
	if strings.Contains(got, "## Worth a close look") {
		t.Error("default instruction still present alongside a custom one")
	}
}

func TestBuildPromptOmitsEmptySections(t *testing.T) {
	got := BuildPrompt(Request{Diff: "x"})
	if strings.Contains(got, "Reviewing:") {
		t.Error("empty target rendered a Reviewing line")
	}
	if strings.Contains(got, "Files changed") {
		t.Error("empty file list rendered a Files changed section")
	}
}

func TestTruncateDiffCutsAtFileBoundary(t *testing.T) {
	var b strings.Builder
	for range 50 {
		b.WriteString("diff --git a/f.go b/f.go\n")
		b.WriteString(strings.Repeat("+line of content here\n", 200))
	}

	got, truncated := truncateDiff(b.String(), 10_000)
	if !truncated {
		t.Fatal("truncateDiff reported no truncation on oversized input")
	}
	if len(got) > 10_000 {
		t.Errorf("result is %d bytes, want at most 10000", len(got))
	}
	// A cut mid-hunk would hand the provider an unparseable fragment.
	if !strings.HasSuffix(got, "\n") {
		t.Error("truncated diff does not end at a line boundary")
	}
	if strings.Count(got, "diff --git") == 0 {
		t.Error("truncation removed every file header")
	}
}

func TestTruncateDiffLeavesSmallInputAlone(t *testing.T) {
	in := "diff --git a/f.go b/f.go\n+one\n"
	got, truncated := truncateDiff(in, 10_000)
	if truncated {
		t.Error("small diff reported as truncated")
	}
	if got != in {
		t.Error("small diff was modified")
	}
}

func TestBuildPromptFlagsTruncation(t *testing.T) {
	huge := strings.Repeat("+line\n", MaxDiffBytes)
	got := BuildPrompt(Request{Diff: huge})
	if !strings.Contains(got, "truncated") {
		t.Error("prompt does not tell the provider the diff was truncated")
	}
}

func TestClaudeCodeWalkthrough(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("claude -p", "## What changed\n\nIt moves keys.")
	p := NewClaudeCode(runner, WithLookPath(func(string) bool { return true }))

	got, err := p.Walkthrough(context.Background(), Request{
		Diff:   "diff --git a/f.go b/f.go\n+x\n",
		Target: "the working tree",
	})
	if err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got != "## What changed\n\nIt moves keys." {
		t.Errorf("Walkthrough() = %q", got)
	}

	// The prompt must arrive on stdin — a large diff would overflow argv.
	stdin := runner.LastStdin()
	if !strings.Contains(stdin, "diff --git a/f.go b/f.go") {
		t.Errorf("diff not piped on stdin, got %q", stdin)
	}
	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	for _, arg := range calls[0].Cmd.Args {
		if strings.Contains(arg, "diff --git") {
			t.Error("diff was passed as an argument instead of on stdin")
		}
	}
}

func TestClaudeCodeRunsInWorkdir(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("claude -p", "text")
	p := NewClaudeCode(runner,
		WithLookPath(func(string) bool { return true }),
		WithWorkdir("/repo/root"),
	)

	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := runner.Calls()[0].Cmd.Dir; got != "/repo/root" {
		t.Errorf("Dir = %q, want /repo/root", got)
	}
}

func TestClaudeCodeEmptyDiff(t *testing.T) {
	p := NewClaudeCode(exec.NewFakeRunner(), WithLookPath(func(string) bool { return true }))

	_, err := p.Walkthrough(context.Background(), Request{Diff: "   \n"})
	if err == nil {
		t.Fatal("Walkthrough succeeded on an empty diff")
	}
}

func TestClaudeCodeEmptyOutput(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("claude -p", "   \n")
	p := NewClaudeCode(runner, WithLookPath(func(string) bool { return true }))

	_, err := p.Walkthrough(context.Background(), Request{Diff: "diff"})
	if err == nil {
		t.Fatal("Walkthrough succeeded with no model output")
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("error = %v", err)
	}
}

func TestClaudeCodeCommandFailure(t *testing.T) {
	runner := exec.NewFakeRunner().RespondErr("claude -p", "not authenticated", 1)
	p := NewClaudeCode(runner, WithLookPath(func(string) bool { return true }))

	_, err := p.Walkthrough(context.Background(), Request{Diff: "diff"})
	if err == nil {
		t.Fatal("Walkthrough succeeded despite a CLI failure")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("error = %v, want the CLI's stderr surfaced", err)
	}
}

func TestClaudeCodeAvailability(t *testing.T) {
	present := NewClaudeCode(exec.NewFakeRunner(), WithLookPath(func(string) bool { return true }))
	if !present.Available(context.Background()) {
		t.Error("Available() = false when the binary is on PATH")
	}

	absent := NewClaudeCode(exec.NewFakeRunner(), WithLookPath(func(string) bool { return false }))
	if absent.Available(context.Background()) {
		t.Error("Available() = true when the binary is missing")
	}
}

func TestClaudeCodeCustomBinary(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("claude-next -p", "text")
	p := NewClaudeCode(runner,
		WithBinary("claude-next"),
		WithLookPath(func(string) bool { return true }),
	)

	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := runner.Calls()[0].Cmd.Name; got != "claude-next" {
		t.Errorf("binary = %q, want claude-next", got)
	}
}

func TestClaudeCodeExtraArgs(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("claude -p --model opus", "text")
	p := NewClaudeCode(runner,
		WithLookPath(func(string) bool { return true }),
		WithExtraArgs("--model", "opus"),
	)

	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := runner.Calls()[0].Cmd.String(); got != "claude -p --model opus" {
		t.Errorf("command = %q, want the extra args appended after -p", got)
	}
}

func TestClaudeCodeTimeoutIsReported(t *testing.T) {
	// A slow CLI must produce a timeout message, not a raw exec error.
	runner := exec.NewFakeRunner().RespondFunc("claude -p",
		func(ctx context.Context, _ exec.Command) (exec.Result, error) {
			<-ctx.Done()
			return exec.Result{}, ctx.Err()
		})
	p := NewClaudeCode(runner,
		WithTimeout(20*time.Millisecond),
		WithLookPath(func(string) bool { return true }),
	)

	_, err := p.Walkthrough(context.Background(), Request{Diff: "x"})
	if err == nil {
		t.Fatal("Walkthrough succeeded past its timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want a timeout message", err)
	}
}

var _ Provider = (*ClaudeCodeProvider)(nil)

// The walkthrough is a guided tour of the changed code, not a summary. These are
// the properties that distinguish the two, so a future edit to the instruction
// cannot quietly turn it back into an overview.
func TestDefaultInstructionAsksForGroupedSteps(t *testing.T) {
	got := BuildPrompt(Request{Diff: "x"})

	for _, want := range []string{
		"step by step",
		"Group the changed files into steps",
		"one markdown section per step, numbered",
		"## 1.",
		"Order the steps so each one sets up the next",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt does not ask for grouped steps: missing %q", want)
		}
	}

	for _, unwanted := range []string{
		"## What changed",
		"the intent of the change as a whole",
		"not a file-by-file list",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("prompt still asks for an overall description: %q", unwanted)
		}
	}
}

func TestDefaultInstructionAsksAboutCodeNotSyntax(t *testing.T) {
	got := BuildPrompt(Request{Diff: "x"})

	for _, want := range []string{
		"Name the functions, types, fields",
		"describe the behaviour rather than",
		"Do not open with a summary",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestDefaultInstructionPinsThePathLineTheToolReads(t *testing.T) {
	got := BuildPrompt(Request{Diff: "x"})

	for _, want := range []string{
		"must appear in at least one step",
		"nothing but the step's file paths",
		"it\nis read by the tool",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt does not pin the machine-read path line: missing %q", want)
		}
	}
}

// TestDefaultInstructionParsesIntoGroups runs the instruction's own example
// through the parser that reads a provider's reply, so the format peel asks for
// and the format it can read cannot drift apart.
func TestDefaultInstructionParsesIntoGroups(t *testing.T) {
	example := "## 1. A short title for what this step does\n" +
		"`path/to/file.go` · `path/to/other.go`\n\n" +
		"Prose about what the step does.\n"

	steps := store.ParseSteps(example)
	if len(steps) != 1 {
		t.Fatalf("the example parsed into %d steps, want 1", len(steps))
	}
	if steps[0].Number != 1 {
		t.Errorf("step number = %d, want 1", steps[0].Number)
	}
	want := []string{"path/to/file.go", "path/to/other.go"}
	if len(steps[0].Files) != 2 || steps[0].Files[0] != want[0] || steps[0].Files[1] != want[1] {
		t.Errorf("files = %v, want %v", steps[0].Files, want)
	}

	if !strings.Contains(BuildPrompt(Request{Diff: "x"}), example[:strings.Index(example, "\n\n")]) {
		t.Error("the instruction no longer contains the example this test parses")
	}
}
