package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ziadalzarka/peel/internal/exec"
)

// CodexProvider generates walkthroughs by running the Codex CLI
// non-interactively. The CLI reuses the user's existing Codex authentication.
type CodexProvider struct {
	runner    exec.Runner
	binary    string
	dir       string
	timeout   time.Duration
	extraArgs []string
	// lookPath reports whether the binary is on PATH. Injected for tests.
	lookPath func(string) bool
}

// CodexOption configures a CodexProvider.
type CodexOption func(*CodexProvider)

// WithCodexBinary overrides the executable name, for tests or a custom install.
func WithCodexBinary(name string) CodexOption {
	return func(p *CodexProvider) { p.binary = name }
}

// WithCodexWorkdir sets the repository the CLI may inspect while producing the
// walkthrough.
func WithCodexWorkdir(dir string) CodexOption {
	return func(p *CodexProvider) { p.dir = dir }
}

// WithCodexTimeout bounds how long a generation may take.
func WithCodexTimeout(d time.Duration) CodexOption {
	return func(p *CodexProvider) { p.timeout = d }
}

// WithCodexLookPath overrides binary detection, for tests.
func WithCodexLookPath(fn func(string) bool) CodexOption {
	return func(p *CodexProvider) { p.lookPath = fn }
}

// WithCodexExtraArgs appends arguments before the stdin prompt marker, so a
// user can pin a model or pass another `codex exec` flag.
func WithCodexExtraArgs(args ...string) CodexOption {
	return func(p *CodexProvider) { p.extraArgs = append(p.extraArgs, args...) }
}

// NewCodex returns a provider backed by `codex exec`.
func NewCodex(runner exec.Runner, opts ...CodexOption) *CodexProvider {
	p := &CodexProvider{
		runner:   runner,
		binary:   "codex",
		timeout:  3 * time.Minute,
		lookPath: exec.LookPath,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements Provider.
func (p *CodexProvider) Name() string { return "codex" }

// Description implements Provider.
func (p *CodexProvider) Description() string {
	return "shells out to `codex exec`, using your existing Codex session"
}

// Available implements Provider.
func (p *CodexProvider) Available(context.Context) bool {
	return p.lookPath(p.binary)
}

// Walkthrough implements Provider. Codex reads the complete prompt from stdin.
// The explicit read-only sandbox prevents a walkthrough from changing the
// repository, and ephemeral mode avoids adding a throwaway session to history.
func (p *CodexProvider) Walkthrough(ctx context.Context, req Request) (string, error) {
	if strings.TrimSpace(req.Diff) == "" {
		return "", fmt.Errorf("nothing to summarise: the diff is empty")
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	args := []string{"exec", "--sandbox", "read-only", "--ephemeral"}
	args = append(args, p.extraArgs...)
	args = append(args, "-")
	res, err := p.runner.Run(ctx, exec.Command{
		Name:  p.binary,
		Args:  args,
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
