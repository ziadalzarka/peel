package exec

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// FakeRunner is a Runner that replays scripted responses. It records every
// invocation so tests can assert on the exact commands a package issued.
//
// Responses are matched against Command.String() — either exactly, or by
// substring when no exact match exists. An unmatched command returns an error
// so that tests fail loudly rather than silently seeing empty output.
type FakeRunner struct {
	mu        sync.Mutex
	responses map[string]fakeResponse
	calls     []RecordedCall
}

// RecordedCall is one observed invocation.
type RecordedCall struct {
	Cmd   Command
	Stdin string
}

type fakeResponse struct {
	result Result
	err    error
	// fn, when set, computes the response and may honour ctx — used to
	// exercise timeout and cancellation paths.
	fn func(context.Context, Command) (Result, error)
}

// NewFakeRunner returns an empty FakeRunner.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{responses: map[string]fakeResponse{}}
}

// Respond registers stdout to return for commands matching pattern.
func (f *FakeRunner) Respond(pattern, stdout string) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[pattern] = fakeResponse{result: Result{Stdout: []byte(stdout)}}
	return f
}

// RespondErr registers a failure for commands matching pattern.
func (f *FakeRunner) RespondErr(pattern, stderr string, code int) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[pattern] = fakeResponse{
		result: Result{Stderr: []byte(stderr), ExitCode: code},
		err:    &ExitError{ExitCode: code, Stderr: stderr},
	}
	return f
}

// RespondFunc registers a handler for commands matching pattern. The handler
// receives the context, so it can block until cancellation.
func (f *FakeRunner) RespondFunc(pattern string, fn func(context.Context, Command) (Result, error)) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[pattern] = fakeResponse{fn: fn}
	return f
}

// Run implements Runner.
func (f *FakeRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	var stdin string
	if cmd.Stdin != nil {
		b, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return Result{}, fmt.Errorf("fake runner: read stdin: %w", err)
		}
		stdin = string(b)
	}

	resp, ok := f.record(cmd, stdin)
	if !ok {
		return Result{}, fmt.Errorf("fake runner: no response registered for %q", cmd)
	}
	// Delivered outside the lock: a handler may block on ctx, and holding the
	// mutex there would deadlock any concurrent caller.
	if resp.fn != nil {
		return resp.fn(ctx, cmd)
	}
	return resp.result, resp.err
}

// record logs the call and looks up its response, preferring an exact match
// over a substring one.
func (f *FakeRunner) record(cmd Command, stdin string) (fakeResponse, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, RecordedCall{Cmd: cmd, Stdin: stdin})

	key := cmd.String()
	if resp, ok := f.responses[key]; ok {
		return resp, true
	}
	for pattern, resp := range f.responses {
		if strings.Contains(key, pattern) {
			return resp, true
		}
	}
	return fakeResponse{}, false
}

// Calls returns every recorded invocation, in order.
func (f *FakeRunner) Calls() []RecordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RecordedCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// LastStdin returns the stdin of the most recent call, or "" if none.
func (f *FakeRunner) LastStdin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1].Stdin
}
