package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ziadalzarka/peel/internal/exec"
)

const codexCommand = "codex exec --sandbox read-only --ephemeral -"

func TestCodexWalkthrough(t *testing.T) {
	runner := exec.NewFakeRunner().Respond(codexCommand, "## 1. Read the change\n`f.go`\n\nIt moves keys.")
	p := NewCodex(runner, WithCodexLookPath(func(string) bool { return true }))

	got, err := p.Walkthrough(context.Background(), Request{
		Diff:   "diff --git a/f.go b/f.go\n+x\n",
		Target: "the working tree",
	})
	if err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if !strings.Contains(got, "It moves keys.") {
		t.Errorf("Walkthrough() = %q", got)
	}

	if stdin := runner.LastStdin(); !strings.Contains(stdin, "diff --git a/f.go b/f.go") {
		t.Errorf("diff not piped on stdin, got %q", stdin)
	}
	call := runner.Calls()[0].Cmd
	if got := call.String(); got != codexCommand {
		t.Errorf("command = %q, want %q", got, codexCommand)
	}
}

func TestCodexRunsInWorkdir(t *testing.T) {
	runner := exec.NewFakeRunner().Respond(codexCommand, "text")
	p := NewCodex(runner,
		WithCodexLookPath(func(string) bool { return true }),
		WithCodexWorkdir("/repo/root"),
	)

	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := runner.Calls()[0].Cmd.Dir; got != "/repo/root" {
		t.Errorf("Dir = %q, want /repo/root", got)
	}
}

func TestCodexRejectsEmptyDiffAndOutput(t *testing.T) {
	p := NewCodex(exec.NewFakeRunner(), WithCodexLookPath(func(string) bool { return true }))
	if _, err := p.Walkthrough(context.Background(), Request{Diff: " \n"}); err == nil {
		t.Fatal("Walkthrough succeeded on an empty diff")
	}

	runner := exec.NewFakeRunner().Respond(codexCommand, " \n")
	p = NewCodex(runner, WithCodexLookPath(func(string) bool { return true }))
	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err == nil ||
		!strings.Contains(err.Error(), "no output") {
		t.Fatalf("empty output error = %v, want a no-output error", err)
	}
}

func TestCodexSurfacesCommandFailure(t *testing.T) {
	runner := exec.NewFakeRunner().RespondErr(codexCommand, "not authenticated", 1)
	p := NewCodex(runner, WithCodexLookPath(func(string) bool { return true }))

	_, err := p.Walkthrough(context.Background(), Request{Diff: "x"})
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error = %v, want the CLI's stderr surfaced", err)
	}
}

func TestCodexAvailability(t *testing.T) {
	present := NewCodex(exec.NewFakeRunner(), WithCodexLookPath(func(string) bool { return true }))
	if !present.Available(context.Background()) {
		t.Error("Available() = false when the binary is on PATH")
	}

	absent := NewCodex(exec.NewFakeRunner(), WithCodexLookPath(func(string) bool { return false }))
	if absent.Available(context.Background()) {
		t.Error("Available() = true when the binary is missing")
	}
}

func TestCodexOptions(t *testing.T) {
	const command = "codex-next exec --sandbox read-only --ephemeral --model gpt-test -"
	runner := exec.NewFakeRunner().Respond(command, "text")
	p := NewCodex(runner,
		WithCodexBinary("codex-next"),
		WithCodexExtraArgs("--model", "gpt-test"),
		WithCodexLookPath(func(string) bool { return true }),
	)

	if _, err := p.Walkthrough(context.Background(), Request{Diff: "x"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := runner.Calls()[0].Cmd.String(); got != command {
		t.Errorf("command = %q, want %q", got, command)
	}
}

func TestCodexTimeoutIsReported(t *testing.T) {
	runner := exec.NewFakeRunner().RespondFunc(codexCommand,
		func(ctx context.Context, _ exec.Command) (exec.Result, error) {
			<-ctx.Done()
			return exec.Result{}, ctx.Err()
		})
	p := NewCodex(runner,
		WithCodexTimeout(20*time.Millisecond),
		WithCodexLookPath(func(string) bool { return true }),
	)

	_, err := p.Walkthrough(context.Background(), Request{Diff: "x"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout message", err)
	}
}

var _ Provider = (*CodexProvider)(nil)
