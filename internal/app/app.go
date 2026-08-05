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
	"strings"

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
	// Folds remembers which files have been folded away.
	Folds store.FoldStore
	// Views remembers how each review was last being looked at.
	Views store.ViewStore
	// AI holds the walkthrough providers, in preference order.
	AI *ai.Registry
	// Forges holds the pull request providers, in preference order.
	Forges *forge.Registry

	// Runner runs the external commands peel shells out to.
	Runner exec.Runner

	// Root is the working tree root.
	Root string
	// StateDir is where peel keeps its files, inside the git directory.
	StateDir string

	// lookPath reports whether an executable is on PATH, which is how the
	// clipboard tool is chosen. Injected for tests.
	lookPath func(string) bool
}

// OpenCommand hands a file to whatever the desktop opens it with, and is what
// `o` runs when git config names nothing better.
const OpenCommand = "open"

// ConfigSection is the git config section peel reads its settings from, and
// OpenKey is the setting inside it that names the command `o` runs:
// `peel.open` for every file, `peel.open.<extension>` for one kind of file.
const (
	ConfigSection = "peel"
	OpenKey       = ConfigSection + ".open"
)

// OpenFile opens a file of the working tree the way double clicking it would,
// unless git config names a command for that kind of file. The path is the one
// the diff carries, relative to the root.
func (a *App) OpenFile(ctx context.Context, path string) error {
	argv, err := a.openCommand(ctx, path)
	if err != nil {
		return err
	}
	_, err = a.Runner.Run(ctx, exec.Command{
		Name: argv[0],
		Args: append(argv[1:], filepath.Join(a.Root, path)),
		Dir:  a.Root,
	})
	return err
}

// openCommand is the command line that opens path, most specific setting first:
// `peel.open.<extension>` for that kind of file, then `peel.open` for the rest,
// then the desktop's own opener. A setting is split on spaces rather than run
// through a shell, so `open -a Marked` works and `cmd && other` does not.
//
// Config is read on every open rather than at startup, so changing it takes
// effect in the session already running.
func (a *App) openCommand(ctx context.Context, path string) ([]string, error) {
	cfg, err := a.Repo.ConfigSection(ctx, ConfigSection)
	if err != nil {
		return nil, err
	}

	// Lower-cased because that is the case git stores the key's last component
	// in, whatever the extension on disk looks like.
	if ext := strings.TrimPrefix(filepath.Ext(path), "."); ext != "" {
		if argv := strings.Fields(cfg[OpenKey+"."+strings.ToLower(ext)]); len(argv) > 0 {
			return argv, nil
		}
	}
	if argv := strings.Fields(cfg[OpenKey]); len(argv) > 0 {
		return argv, nil
	}
	return []string{OpenCommand}, nil
}

// ClipboardCommands are the ways peel knows to reach the system clipboard, most
// preferred first: the desktop's own tool, then either X11 selection helper,
// then Windows' one for a working tree under WSL. The first one on PATH wins.
var ClipboardCommands = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
	{"clip.exe"},
}

// Copy puts text on the system clipboard, by piping it into whichever of
// ClipboardCommands this machine has.
func (a *App) Copy(ctx context.Context, text string) error {
	for _, argv := range ClipboardCommands {
		if !a.lookPath(argv[0]) {
			continue
		}
		_, err := a.Runner.Run(ctx, exec.Command{
			Name:  argv[0],
			Args:  argv[1:],
			Dir:   a.Root,
			Stdin: strings.NewReader(text),
		})
		return err
	}
	return fmt.Errorf("nothing on PATH to copy with; install one of %s", strings.Join(clipboardNames(), ", "))
}

// clipboardNames lists the clipboard tools by name, for the error that says none
// of them is installed.
func clipboardNames() []string {
	out := make([]string, 0, len(ClipboardCommands))
	for _, argv := range ClipboardCommands {
		out = append(out, argv[0])
	}
	return out
}

// Option customises how an App is opened. Tests use these to substitute fakes;
// nothing in normal operation needs them.
type Option func(*config)

type config struct {
	runner    exec.Runner
	lookPath  func(string) bool
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

// WithLookPath overrides binary detection, for tests: it decides which clipboard
// tool Copy pipes into.
func WithLookPath(fn func(string) bool) Option {
	return func(c *config) { c.lookPath = fn }
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
	cfg := &config{runner: exec.NewOSRunner(), lookPath: exec.LookPath}
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
		Folds:        store.NewJSONFoldStore(filepath.Join(stateDir, "folds.json")),
		Views:        store.NewJSONViewStore(filepath.Join(stateDir, "view.json")),
		AI:           aiRegistry,
		Forges:       forgeRegistry,
		Runner:       cfg.runner,
		Root:         root,
		StateDir:     stateDir,
		lookPath:     cfg.lookPath,
	}, nil
}
