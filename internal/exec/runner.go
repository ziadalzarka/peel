// Package exec abstracts running external commands so that packages depending
// on git, gh or claude can be unit tested without those binaries present.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Result is the outcome of a single command invocation.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes an external command and returns its output.
//
// Implementations must not interpret the command; they only run it. A non-zero
// exit status is reported through the returned error as an *ExitError, with the
// Result still populated so callers can inspect stderr.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// Command describes a single external invocation.
type Command struct {
	Name  string
	Args  []string
	Dir   string
	Stdin io.Reader
	Env   []string
}

// String renders the command for error messages and test assertions.
func (c Command) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

// ExitError reports a command that ran but exited non-zero.
type ExitError struct {
	Cmd      Command
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("%s: exit status %d", e.Cmd, e.ExitCode)
	}
	return fmt.Sprintf("%s: exit status %d: %s", e.Cmd, e.ExitCode, msg)
}

// OSRunner runs commands against the real operating system.
type OSRunner struct{}

// NewOSRunner returns a Runner backed by os/exec.
func NewOSRunner() *OSRunner { return &OSRunner{} }

// Run implements Runner.
func (r *OSRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.Dir = cmd.Dir
	c.Stdin = cmd.Stdin
	if len(cmd.Env) > 0 {
		c.Env = cmd.Env
	}

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, &ExitError{Cmd: cmd, ExitCode: res.ExitCode, Stderr: stderr.String()}
		}
		return res, fmt.Errorf("%s: %w", cmd, err)
	}
	return res, nil
}

// LookPath reports whether an executable is resolvable on PATH.
func LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
