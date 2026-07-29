package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ziadalzarka/peel/internal/exec"
)

// ClaudeCodeProvider generates walkthroughs by shelling out to the Claude Code
// CLI in print mode.
//
// Going through the CLI rather than the API is deliberate: it reuses the user's
// existing session, so peel owns no API key, no model configuration and no
// separate billing path.
type ClaudeCodeProvider struct {
	runner    exec.Runner
	binary    string
	dir       string
	timeout   time.Duration
	extraArgs []string
	// lookPath reports whether the binary is on PATH. Injected for tests.
	lookPath func(string) bool
}

// ClaudeCodeOption configures a ClaudeCodeProvider.
type ClaudeCodeOption func(*ClaudeCodeProvider)

// WithBinary overrides the executable name, for tests or a custom install.
func WithBinary(name string) ClaudeCodeOption {
	return func(p *ClaudeCodeProvider) { p.binary = name }
}

// WithWorkdir sets the directory the CLI runs in, so it picks up the
// repository's CLAUDE.md and settings.
func WithWorkdir(dir string) ClaudeCodeOption {
	return func(p *ClaudeCodeProvider) { p.dir = dir }
}

// WithTimeout bounds how long a generation may take.
func WithTimeout(d time.Duration) ClaudeCodeOption {
	return func(p *ClaudeCodeProvider) { p.timeout = d }
}

// WithLookPath overrides binary detection, for tests.
func WithLookPath(fn func(string) bool) ClaudeCodeOption {
	return func(p *ClaudeCodeProvider) { p.lookPath = fn }
}

// WithExtraArgs appends arguments after -p, so a user can pin a model or pass
// any other flag the CLI accepts.
func WithExtraArgs(args ...string) ClaudeCodeOption {
	return func(p *ClaudeCodeProvider) { p.extraArgs = append(p.extraArgs, args...) }
}

// NewClaudeCode returns a provider backed by the `claude` CLI.
func NewClaudeCode(runner exec.Runner, opts ...ClaudeCodeOption) *ClaudeCodeProvider {
	p := &ClaudeCodeProvider{
		runner:   runner,
		binary:   "claude",
		timeout:  3 * time.Minute,
		lookPath: exec.LookPath,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements Provider.
func (p *ClaudeCodeProvider) Name() string { return "claude-code" }

// Description implements Provider.
func (p *ClaudeCodeProvider) Description() string {
	return "shells out to the `claude` CLI, using your existing Claude Code session"
}

// Available implements Provider.
func (p *ClaudeCodeProvider) Available(context.Context) bool {
	return p.lookPath(p.binary)
}

// Walkthrough implements Provider. The prompt goes in on stdin rather than as
// an argument, so a large diff cannot overflow the platform's argument limit.
func (p *ClaudeCodeProvider) Walkthrough(ctx context.Context, req Request) (string, error) {
	if strings.TrimSpace(req.Diff) == "" {
		return "", fmt.Errorf("nothing to summarise: the diff is empty")
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	res, err := p.runner.Run(ctx, exec.Command{
		Name:  p.binary,
		Args:  append([]string{"-p"}, p.extraArgs...),
		Dir:   p.dir,
		Stdin: strings.NewReader(BuildPrompt(req)),
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s timed out after %s", p.binary, p.timeout)
		}
		return "", fmt.Errorf("%s: %w", p.binary, err)
	}

	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", fmt.Errorf("%s returned no output", p.binary)
	}
	return out, nil
}
