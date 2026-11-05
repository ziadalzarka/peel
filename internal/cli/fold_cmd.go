package cli

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ziadalzarka/peel/internal/store"
)

func runFold(ctx context.Context, c *CLI, args []string) error {
	subs := map[string]func(context.Context, *CLI, []string) error{
		"add":   foldAdd,
		"list":  foldList,
		"rm":    foldRemove,
		"clear": foldClear,
	}
	if len(args) == 0 {
		return subcommandUsage("fold", keysOf(subs))
	}
	run, ok := subs[args[0]]
	if !ok {
		return usageErrorf("unknown fold subcommand %q; want one of %s", args[0], strings.Join(sorted(keysOf(subs)), ", "))
	}
	if c.pr != "" {
		return usageErrorf("peel fold only works on the working tree, not on a pull request")
	}
	return run(ctx, c, args[1:])
}

func foldAdd(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("fold add")
	file := fs.String("file", "", "path to hide lines in (required)")
	line := fs.Int("line", 0, "first line to hide; omit to hide the whole file")
	endLine := fs.Int("end-line", 0, "last line to hide; omit to hide only --line")
	label := fs.String("label", "", "short text shown on the collapsed row")
	author := fs.String("author", string(store.AuthorUnknown), "the name the fold is signed with, e.g. claude")
	asJSON := fs.Bool("json", false, "emit the created fold as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}

	if strings.TrimSpace(*file) == "" {
		return usageErrorf("fold add needs --file")
	}
	if *line < 0 {
		return usageErrorf("--line %d is negative", *line)
	}
	if *endLine != 0 && *line == 0 {
		return usageErrorf("--end-line needs --line")
	}
	if *endLine != 0 && *endLine < *line {
		return usageErrorf("--end-line %d is before --line %d", *endLine, *line)
	}
	name := store.Author(*author)
	if !name.Valid() {
		return usageErrorf("--author %q is not a name", *author)
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	created, err := a.AddHiddenRun(ctx, s, store.HiddenRun{
		File:    path.Clean(*file),
		Line:    *line,
		EndLine: *endLine,
		Label:   strings.Join(strings.Fields(*label), " "),
		Author:  name,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(c.Stdout, foldToJSON(created))
	}
	fmt.Fprintf(c.Stdout, "added %s on %s\n", created.ID, created.Location())
	return nil
}

func foldList(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("fold list")
	asJSON := fs.Bool("json", false, "emit JSON")
	file := fs.String("file", "", "only folds in this path")
	author := fs.String("author", "", "only folds signed with this name, e.g. claude")
	if err := parse(fs, args); err != nil {
		return err
	}
	filter, err := foldFilter(*file, *author)
	if err != nil {
		return err
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	runs, err := a.HiddenRuns(ctx, s, filter)
	if err != nil {
		return err
	}
	if *asJSON {
		out := make([]foldJSON, 0, len(runs))
		for _, r := range runs {
			out = append(out, foldToJSON(r))
		}
		return writeJSON(c.Stdout, out)
	}
	return writeFoldTable(c.Stdout, runs)
}

func foldRemove(ctx context.Context, c *CLI, args []string) error {
	if len(args) == 0 {
		return usageErrorf("fold rm needs at least one fold id")
	}
	a, _, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	for _, id := range args {
		if err := a.Local.Hidden.Remove(id); err != nil {
			return err
		}
		fmt.Fprintf(c.Stdout, "removed %s\n", id)
	}
	return a.KeepAnchors(ctx)
}

func foldClear(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("fold clear")
	file := fs.String("file", "", "only clear folds in this path")
	author := fs.String("author", "", "only clear folds signed with this name, e.g. claude")
	if err := parse(fs, args); err != nil {
		return err
	}
	filter, err := foldFilter(*file, *author)
	if err != nil {
		return err
	}

	a, _, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	n, err := a.Local.Hidden.Clear(filter)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "cleared %s\n", plural(n, "fold"))
	return a.KeepAnchors(ctx)
}

func foldFilter(file, author string) (store.HiddenFilter, error) {
	filter := store.HiddenFilter{Author: store.Author(author)}
	if file != "" {
		filter.File = path.Clean(file)
	}
	if author != "" && !filter.Author.Valid() {
		return filter, usageErrorf("--author %q is not a name", author)
	}
	return filter, nil
}

type foldJSON struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	EndLine  int    `json:"endLine,omitempty"`
	Label    string `json:"label,omitempty"`
	Author   string `json:"author"`
	Created  string `json:"createdAt"`
	Moved    int    `json:"movedFrom,omitempty"`
	Outdated bool   `json:"outdated,omitempty"`
}

func foldToJSON(r store.HiddenRun) foldJSON {
	return foldJSON{
		ID:       r.ID,
		File:     r.File,
		Line:     r.Line,
		EndLine:  r.EndLine,
		Label:    r.Label,
		Author:   string(r.Author),
		Created:  r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Moved:    r.MovedFrom,
		Outdated: r.Outdated,
	}
}

func writeFoldTable(w io.Writer, runs []store.HiddenRun) error {
	if len(runs) == 0 {
		fmt.Fprintln(w, "no folds")
		return nil
	}
	for _, r := range runs {
		where := r.Location()
		if r.Outdated {
			where += " (outdated, no longer hiding)"
		}
		fmt.Fprintf(w, "%-10s %-28s %s  (%s)\n", r.ID, where, r.Label, r.Author)
	}
	return nil
}
