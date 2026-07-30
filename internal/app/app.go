// Package app wires peel's pieces together and exposes the operations the CLI
// and the TUI both need.
//
// It is the only package that knows which concrete providers ship by default;
// everything below it depends on interfaces.
package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// StateDirName is the directory created inside .git to hold peel's state.
const StateDirName = "peel"

// App holds the collaborating pieces of one peel invocation.
type App struct {
	Repo   *git.Repo
	Stager *git.Stager
	// Comments persists review notes.
	Comments store.CommentStore
	// Walkthroughs caches the most recent AI narrative.
	Walkthroughs store.WalkthroughCache
	// AI holds the walkthrough providers, in preference order.
	AI *ai.Registry
	// Forges holds the pull request providers, in preference order.
	Forges *forge.Registry

	// Root is the working tree root.
	Root string
	// StateDir is where peel keeps its files, inside the git directory.
	StateDir string
}

// Option customises how an App is opened. Tests use these to substitute fakes;
// nothing in normal operation needs them.
type Option func(*config)

type config struct {
	runner    exec.Runner
	ai        *ai.Registry
	forges    *forge.Registry
	aiOpts    []ai.ClaudeCodeOption
	codexOpts []ai.CodexOption
	ghOpts    []forge.GitHubOption
	storeOp   []store.Option
}

// WithRunner replaces the command runner used for git and every provider.
func WithRunner(r exec.Runner) Option {
	return func(c *config) { c.runner = r }
}

// WithAIRegistry replaces the default AI provider set.
func WithAIRegistry(r *ai.Registry) Option {
	return func(c *config) { c.ai = r }
}

// WithForgeRegistry replaces the default forge provider set.
func WithForgeRegistry(r *forge.Registry) Option {
	return func(c *config) { c.forges = r }
}

// WithClaudeCodeOptions passes options through to the default AI provider.
func WithClaudeCodeOptions(opts ...ai.ClaudeCodeOption) Option {
	return func(c *config) { c.aiOpts = append(c.aiOpts, opts...) }
}

// WithCodexOptions passes options through to the default Codex AI provider.
func WithCodexOptions(opts ...ai.CodexOption) Option {
	return func(c *config) { c.codexOpts = append(c.codexOpts, opts...) }
}

// WithGitHubOptions passes options through to the default forge provider.
func WithGitHubOptions(opts ...forge.GitHubOption) Option {
	return func(c *config) { c.ghOpts = append(c.ghOpts, opts...) }
}

// WithStoreOptions passes options through to the comment store.
func WithStoreOptions(opts ...store.Option) Option {
	return func(c *config) { c.storeOp = append(c.storeOp, opts...) }
}

// Open discovers the repository containing dir and assembles an App for it.
//
// State lives in the working tree's own git directory rather than the shared
// one, so each worktree keeps its own review notes.
func Open(ctx context.Context, dir string, opts ...Option) (*App, error) {
	cfg := &config{runner: exec.NewOSRunner()}
	for _, opt := range opts {
		opt(cfg)
	}

	repo := git.NewRepo(dir, cfg.runner)
	root, err := repo.Root(ctx)
	if err != nil {
		return nil, fmt.Errorf("peel must run inside a git repository: %w", err)
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return nil, err
	}

	// Commands run from the root so relative paths in diffs resolve the same
	// way regardless of which subdirectory peel was started in.
	repo = git.NewRepo(root, cfg.runner)
	stateDir := filepath.Join(gitDir, StateDirName)

	aiRegistry := cfg.ai
	if aiRegistry == nil {
		claudeOpts := append([]ai.ClaudeCodeOption{ai.WithWorkdir(root)}, cfg.aiOpts...)
		codexOpts := append([]ai.CodexOption{ai.WithCodexWorkdir(root)}, cfg.codexOpts...)
		aiRegistry = ai.NewRegistry(
			ai.NewClaudeCode(cfg.runner, claudeOpts...),
			ai.NewCodex(cfg.runner, codexOpts...),
		)
	}
	forgeRegistry := cfg.forges
	if forgeRegistry == nil {
		forgeRegistry = forge.NewRegistry(forge.NewGitHub(cfg.runner, cfg.ghOpts...))
	}

	return &App{
		Repo:         repo,
		Stager:       git.NewStager(repo),
		Comments:     store.NewJSONStore(filepath.Join(stateDir, "comments.json"), cfg.storeOp...),
		Walkthroughs: store.NewJSONWalkthroughCache(filepath.Join(stateDir, "walkthrough.json")),
		AI:           aiRegistry,
		Forges:       forgeRegistry,
		Root:         root,
		StateDir:     stateDir,
	}, nil
}
