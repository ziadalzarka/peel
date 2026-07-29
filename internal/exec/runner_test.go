package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOSRunnerCapturesStdout(t *testing.T) {
	r := NewOSRunner()
	res, err := r.Run(context.Background(), Command{Name: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "hello" {
		t.Errorf("Stdout = %q, want hello", got)
	}
}

func TestOSRunnerPipesStdin(t *testing.T) {
	r := NewOSRunner()
	res, err := r.Run(context.Background(), Command{
		Name:  "cat",
		Stdin: strings.NewReader("piped input"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := string(res.Stdout); got != "piped input" {
		t.Errorf("Stdout = %q, want the piped input echoed back", got)
	}
}

func TestOSRunnerReportsExitCode(t *testing.T) {
	r := NewOSRunner()
	_, err := r.Run(context.Background(), Command{Name: "sh", Args: []string{"-c", "echo oops >&2; exit 3"}})
	if err == nil {
		t.Fatal("Run succeeded on a failing command")
	}

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T, want *ExitError", err)
	}
	if exitErr.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", exitErr.ExitCode)
	}
	if !strings.Contains(exitErr.Error(), "oops") {
		t.Errorf("error = %q, want it to include stderr", exitErr.Error())
	}
}

func TestOSRunnerMissingBinary(t *testing.T) {
	r := NewOSRunner()
	_, err := r.Run(context.Background(), Command{Name: "peel-does-not-exist-xyz"})
	if err == nil {
		t.Fatal("Run succeeded for a missing binary")
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Error("a missing binary was reported as an exit error")
	}
}

func TestOSRunnerHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewOSRunner().Run(ctx, Command{Name: "sleep", Args: []string{"5"}})
	if err == nil {
		t.Fatal("Run ignored a cancelled context")
	}
}

func TestCommandString(t *testing.T) {
	tests := []struct {
		cmd  Command
		want string
	}{
		{Command{Name: "git"}, "git"},
		{Command{Name: "git", Args: []string{"diff", "--cached"}}, "git diff --cached"},
	}
	for _, tt := range tests {
		if got := tt.cmd.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestExitErrorMessage(t *testing.T) {
	withStderr := &ExitError{Cmd: Command{Name: "git"}, ExitCode: 1, Stderr: "  bad patch \n"}
	if got := withStderr.Error(); !strings.Contains(got, "bad patch") {
		t.Errorf("Error() = %q, want it to include trimmed stderr", got)
	}

	silent := &ExitError{Cmd: Command{Name: "git"}, ExitCode: 1}
	if got := silent.Error(); !strings.Contains(got, "exit status 1") {
		t.Errorf("Error() = %q, want the exit status", got)
	}
}

func TestFakeRunnerExactMatch(t *testing.T) {
	f := NewFakeRunner().Respond("git status --porcelain", "M f.go\n")

	res, err := f.Run(context.Background(), Command{Name: "git", Args: []string{"status", "--porcelain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := string(res.Stdout); got != "M f.go\n" {
		t.Errorf("Stdout = %q", got)
	}
}

func TestFakeRunnerSubstringMatch(t *testing.T) {
	f := NewFakeRunner().Respond("rev-parse", "/repo\n")

	res, err := f.Run(context.Background(), Command{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "/repo" {
		t.Errorf("Stdout = %q", got)
	}
}

func TestFakeRunnerUnmatchedCommandFails(t *testing.T) {
	// An unscripted command must fail loudly, or tests pass on empty output.
	f := NewFakeRunner()
	_, err := f.Run(context.Background(), Command{Name: "git", Args: []string{"log"}})
	if err == nil {
		t.Fatal("Run succeeded for an unscripted command")
	}
	if !strings.Contains(err.Error(), "git log") {
		t.Errorf("error = %v, want it to name the command", err)
	}
}

func TestFakeRunnerRecordsCallsAndStdin(t *testing.T) {
	f := NewFakeRunner().Respond("apply", "")

	_, err := f.Run(context.Background(), Command{
		Name:  "git",
		Args:  []string{"apply", "--cached", "-"},
		Stdin: strings.NewReader("PATCH BODY"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Cmd.String() != "git apply --cached -" {
		t.Errorf("recorded %q", calls[0].Cmd.String())
	}
	if f.LastStdin() != "PATCH BODY" {
		t.Errorf("LastStdin() = %q", f.LastStdin())
	}
}

func TestFakeRunnerScriptedError(t *testing.T) {
	f := NewFakeRunner().RespondErr("apply", "corrupt patch at line 3", 1)

	_, err := f.Run(context.Background(), Command{Name: "git", Args: []string{"apply", "-"}})
	if err == nil {
		t.Fatal("Run succeeded, want the scripted failure")
	}
	if !strings.Contains(err.Error(), "corrupt patch") {
		t.Errorf("error = %v", err)
	}
}

func TestLookPath(t *testing.T) {
	if !LookPath("sh") {
		t.Error("LookPath(sh) = false")
	}
	if LookPath("peel-does-not-exist-xyz") {
		t.Error("LookPath reported a nonexistent binary as present")
	}
}

var _ Runner = (*OSRunner)(nil)
var _ Runner = (*FakeRunner)(nil)
