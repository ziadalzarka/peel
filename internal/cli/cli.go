// Package cli parses peel's command line and runs its subcommands.
//
// Every command writes to the CLI's own writers rather than os.Stdout, so the
// whole surface can be tested without a terminal.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
)

// Version is the build version, overridden at link time.
var Version = "dev"

// UIOptions are the display choices the review UI takes from the command line.
// They are named here rather than imported so the CLI does not depend on the UI.
type UIOptions struct {
	// Follow re-reads the repository as it changes.
	Follow bool
	// Split starts in the side-by-side layout.
	Split bool
	// Provider selects the AI provider used for the walkthrough.
	Provider string
}

// TUIRunner launches the interactive review UI.
type TUIRunner func(ctx context.Context, a *app.App, s *app.Session, ui UIOptions) error

// CLI holds the environment one invocation runs in. Tests substitute the
// writers and the two injection points to exercise commands headlessly.
type CLI struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Dir is where peel looks for the repository.
	Dir string
	// OpenApp builds the App. Defaults to app.Open.
	OpenApp func(ctx context.Context, dir string) (*app.App, error)
	// RunTUI launches the interactive UI when no subcommand is given.
	RunTUI TUIRunner

	// pr is the global --pr flag, shared by the TUI and the pr subcommands.
	pr string
	// forgeName is the global --forge flag.
	forgeName string
	// ui holds the global flags that only affect the review UI.
	ui UIOptions
}

// command is one entry in the dispatch table.
type command struct {
	name string
	// usage is the one-line form shown in help.
	usage string
	// summary is the one-line description shown in help.
	summary string
	run     func(ctx context.Context, c *CLI, args []string) error
}

// errUsage marks a failure caused by bad input rather than a runtime problem,
// so Run can print usage alongside the message.
type errUsage struct{ error }

func usageErrorf(format string, args ...any) error {
	return errUsage{fmt.Errorf(format, args...)}
}

// commands is the dispatch table. Order here is the order in help.
func commands() []command {
	return []command{
		{"comment", "comment <list|add|rm|resolve|clear>", "read and write review comments", runComment},
		{"hunks", "hunks list [--json]", "list hunks and whether they are staged", runHunks},
		{"walkthrough", "walkthrough [--regen]", "generate or show the AI narrative", runWalkthrough},
		{"pr", "pr <view|submit>", "inspect a pull request or post a review to it", runPR},
		{"providers", "providers", "list the AI and forge providers and their status", runProviders},
		{"version", "version", "print the version", runVersion},
	}
}

// Run parses args and executes the requested command, returning a process exit
// code.
func (c *CLI) Run(ctx context.Context, args []string) int {
	c.applyDefaults()

	fs := flag.NewFlagSet("peel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.pr, "pr", "", "review a pull request instead of the working tree")
	fs.StringVar(&c.forgeName, "forge", "", "forge provider to use (default: first available)")
	fs.StringVar(&c.ui.Provider, "provider", "", "AI provider to use for walkthroughs (default: first available)")
	fs.BoolVar(&c.ui.Follow, "watch", true, "re-read the repository as it changes (default)")
	noWatch := fs.Bool("no-watch", false, "start with repository following disabled")
	fs.BoolVar(&c.ui.Split, "split", false, "start in the side-by-side layout")
	showVersion := fs.Bool("version", false, "print the version and exit")
	showHelp := fs.Bool("help", false, "show this help")
	fs.BoolVar(showHelp, "h", false, "show this help")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(c.Stderr, "peel: %v\n\n", err)
		c.printUsage(c.Stderr)
		return 2
	}
	if *noWatch {
		c.ui.Follow = false
	}
	if *showHelp {
		c.printUsage(c.Stdout)
		return 0
	}
	if *showVersion {
		fmt.Fprintln(c.Stdout, Version)
		return 0
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return c.report(c.runTUI(ctx))
	}

	name := rest[0]
	for _, cmd := range commands() {
		if cmd.name == name {
			return c.report(cmd.run(ctx, c, rest[1:]))
		}
	}

	fmt.Fprintf(c.Stderr, "peel: unknown command %q\n\n", name)
	c.printUsage(c.Stderr)
	return 2
}

// applyDefaults fills in anything the caller left unset.
func (c *CLI) applyDefaults() {
	if c.Stdout == nil {
		c.Stdout = io.Discard
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	if c.Stdin == nil {
		c.Stdin = strings.NewReader("")
	}
	if c.OpenApp == nil {
		c.OpenApp = func(ctx context.Context, dir string) (*app.App, error) {
			return app.Open(ctx, dir)
		}
	}
}

// report prints an error and maps it to an exit code.
func (c *CLI) report(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.Stderr, "peel: %v\n", err)

	var usage errUsage
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

// open builds the App for this invocation.
func (c *CLI) open(ctx context.Context) (*app.App, error) {
	c.applyDefaults()
	return c.OpenApp(ctx, c.Dir)
}

// session loads whatever is being reviewed: the pull request named by --pr, or
// the working tree.
func (c *CLI) session(ctx context.Context, a *app.App) (*app.Session, error) {
	if c.pr != "" {
		return a.LoadPullRequest(ctx, c.forgeName, c.pr)
	}
	return a.LoadWorkingTree(ctx)
}

// openSession is the common preamble: build the App and load the session.
func (c *CLI) openSession(ctx context.Context) (*app.App, *app.Session, error) {
	a, err := c.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	s, err := c.session(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	return a, s, nil
}

// runTUI launches the interactive review UI.
func (c *CLI) runTUI(ctx context.Context) error {
	if c.RunTUI == nil {
		return fmt.Errorf("no interactive UI is available in this build")
	}
	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	return c.RunTUI(ctx, a, s, c.ui)
}

func (c *CLI) printUsage(w io.Writer) {
	fmt.Fprint(w, `peel — review a diff and stage what you just reviewed

Usage:
  peel [flags]                 open the review UI on the working tree
  peel --pr <ref> [flags]      open the review UI on a pull request
  peel <command> [args]

Commands:
`)
	for _, cmd := range commands() {
		fmt.Fprintf(w, "  %-28s %s\n", cmd.usage, cmd.summary)
	}
	fmt.Fprint(w, `
Flags:
  --pr <ref>       review a pull request: a number, owner/repo#number, or a URL
  --forge <name>   forge provider to use (default: first available)
  --provider <name>
                   AI provider to use for walkthroughs (default: first available)
  --watch          re-read the repository as it changes (default)
  --no-watch       start with repository following disabled
  --split          start in the side-by-side layout
  --version        print the version and exit
  -h, --help       show this help

Staging is available in the review UI, not on the command line: peel does not
expose index writes to scripts or agents, which can already run git add.
`)
}

func runVersion(_ context.Context, c *CLI, args []string) error {
	if len(args) > 0 {
		return usageErrorf("version takes no arguments")
	}
	fmt.Fprintln(c.Stdout, Version)
	return nil
}

func runProviders(ctx context.Context, c *CLI, args []string) error {
	if len(args) > 0 {
		return usageErrorf("providers takes no arguments")
	}
	a, err := c.open(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintln(c.Stdout, "AI providers (walkthroughs):")
	for _, p := range a.AI.All() {
		fmt.Fprintf(c.Stdout, "  %-14s %-10s %s\n", p.Name(), availability(p.Available(ctx)), p.Description())
	}
	fmt.Fprintln(c.Stdout, "\nForge providers (pull requests):")
	for _, p := range a.Forges.All() {
		fmt.Fprintf(c.Stdout, "  %-14s %-10s %s\n", p.Name(), availability(p.Available(ctx)), p.Description())
	}
	return nil
}

func availability(ok bool) string {
	if ok {
		return "available"
	}
	return "missing"
}

// subcommandUsage renders the "want one of" message for a command group.
func subcommandUsage(group string, names []string) error {
	sort.Strings(names)
	return usageErrorf("%s needs a subcommand: %s", group, strings.Join(names, ", "))
}

// newFlagSet returns a flag set that reports errors through the CLI rather
// than printing its own and exiting.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
