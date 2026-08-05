package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/store"
)

// runComment dispatches the `peel comment` group.
func runComment(ctx context.Context, c *CLI, args []string) error {
	subs := map[string]func(context.Context, *CLI, []string) error{
		"list":    commentList,
		"add":     commentAdd,
		"rm":      commentRemove,
		"resolve": commentResolve,
		"clear":   commentClear,
	}
	if len(args) == 0 {
		return subcommandUsage("comment", keysOf(subs))
	}
	run, ok := subs[args[0]]
	if !ok {
		return usageErrorf("unknown comment subcommand %q; want one of %s", args[0], strings.Join(sorted(keysOf(subs)), ", "))
	}
	return run(ctx, c, args[1:])
}

func commentList(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("comment list")
	asJSON := fs.Bool("json", false, "emit JSON")
	file := fs.String("file", "", "only comments on this path")
	unresolved := fs.Bool("unresolved", false, "only comments not yet resolved")
	author := fs.String("author", "", "only comments by this author (user or agent)")
	allTargets := fs.Bool("all", false, "include comments from other review targets")
	if err := parse(fs, args); err != nil {
		return err
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	filter := s.CommentFilter()
	if *allTargets {
		filter = store.Filter{}
	}
	filter.File = *file
	filter.Unresolved = *unresolved
	if err := narrowToAuthor(&filter, *author); err != nil {
		return err
	}

	comments, err := a.Comments.List(filter)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(c.Stdout, commentsToJSON(comments))
	}
	return writeCommentTable(c.Stdout, comments)
}

func commentAdd(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("comment add")
	file := fs.String("file", "", "path the comment applies to (required)")
	line := fs.Int("line", 0, "line number; omit for a file-level comment")
	body := fs.String("body", "", "comment text; omit to read from stdin")
	summary := fs.String("summary", "", "alias for --body")
	side := fs.String("side", string(store.SideNew), "which side the line is on: new or old")
	origin := fs.String("origin", "", "which diff the line number is from: index or worktree")
	author := fs.String("author", string(store.AuthorAgent), "who is writing: user or agent")
	hunk := fs.String("hunk", "", "hunk id the comment was written against")
	asJSON := fs.Bool("json", false, "emit the created comment as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}

	text := *body
	if text == "" {
		text = *summary
	}
	if text == "" {
		read, err := io.ReadAll(c.Stdin)
		if err != nil {
			return fmt.Errorf("read comment body from stdin: %w", err)
		}
		text = strings.TrimSpace(string(read))
	}
	if strings.TrimSpace(text) == "" {
		return usageErrorf("comment add needs --body, or text on stdin")
	}
	if strings.TrimSpace(*file) == "" {
		return usageErrorf("comment add needs --file")
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	comment := store.Comment{
		File:   *file,
		Line:   *line,
		Body:   text,
		Side:   store.Side(*side),
		Origin: store.Origin(*origin),
		Author: store.Author(*author),
		Hunk:   *hunk,
		Target: s.Target,
	}
	created, err := a.Comments.Add(comment)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(c.Stdout, commentToJSON(created))
	}
	fmt.Fprintf(c.Stdout, "added %s on %s\n", created.ID, created.Location())
	return nil
}

func commentRemove(ctx context.Context, c *CLI, args []string) error {
	if len(args) == 0 {
		return usageErrorf("comment rm needs at least one comment id")
	}
	a, err := c.open(ctx)
	if err != nil {
		return err
	}
	for _, id := range args {
		if err := a.Comments.Remove(id); err != nil {
			return err
		}
		fmt.Fprintf(c.Stdout, "removed %s\n", id)
	}
	return nil
}

func commentResolve(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("comment resolve")
	reopen := fs.Bool("undo", false, "mark the comment unresolved again")
	ids, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return usageErrorf("comment resolve needs at least one comment id")
	}

	a, err := c.open(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		got, err := a.Comments.Update(id, func(c *store.Comment) { c.Resolved = !*reopen })
		if err != nil {
			return err
		}
		state := "resolved"
		if *reopen {
			state = "reopened"
		}
		fmt.Fprintf(c.Stdout, "%s %s on %s\n", state, got.ID, got.Location())
	}
	return nil
}

func commentClear(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("comment clear")
	file := fs.String("file", "", "only clear comments on this path")
	resolved := fs.Bool("resolved", false, "only clear comments already resolved")
	author := fs.String("author", "", "only clear comments by this author (user or agent)")
	allTargets := fs.Bool("all", false, "clear comments from every review target")
	if err := parse(fs, args); err != nil {
		return err
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	filter := s.CommentFilter()
	if *allTargets {
		filter = store.Filter{}
	}
	filter.File = *file
	if err := narrowToAuthor(&filter, *author); err != nil {
		return err
	}

	// Clear has no "resolved only" filter of its own, so select the ids first.
	if *resolved {
		return clearResolved(c, a, filter)
	}

	n, err := a.Comments.Clear(filter)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "cleared %s\n", plural(n, "comment"))
	return nil
}

// narrowToAuthor restricts a filter to one author, so `--author agent` clears
// the review an agent left without touching the notes the user wrote. An empty
// name leaves the filter alone.
func narrowToAuthor(filter *store.Filter, name string) error {
	if name == "" {
		return nil
	}
	filter.Author = store.Author(name)
	if !filter.Author.Valid() {
		return usageErrorf("unknown author %q; want user or agent", name)
	}
	return nil
}

// clearResolved removes only the resolved comments matching filter.
func clearResolved(c *CLI, a *app.App, filter store.Filter) error {
	comments, err := a.Comments.List(filter)
	if err != nil {
		return err
	}
	n := 0
	for _, comment := range comments {
		if !comment.Resolved {
			continue
		}
		if err := a.Comments.Remove(comment.ID); err != nil {
			return err
		}
		n++
	}
	fmt.Fprintf(c.Stdout, "cleared %s\n", plural(n, "resolved comment"))
	return nil
}

// commentJSON is the wire shape agents consume. It is defined separately from
// store.Comment so the on-disk format can change without breaking the contract.
type commentJSON struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Line int    `json:"line"`
	Side string `json:"side"`
	// Origin says which diff Line counts lines in — "index" or "worktree" — on a
	// file git holds in both places at once. Empty where the note named neither.
	Origin   string `json:"origin,omitempty"`
	Body     string `json:"body"`
	Hunk     string `json:"hunk,omitempty"`
	Author   string `json:"author"`
	Resolved bool   `json:"resolved"`
	Created  string `json:"createdAt"`
	Target   string `json:"target,omitempty"`
}

func commentToJSON(c store.Comment) commentJSON {
	return commentJSON{
		ID:       c.ID,
		File:     c.File,
		Line:     c.Line,
		Side:     string(c.Side),
		Origin:   string(c.Origin),
		Body:     c.Body,
		Hunk:     c.Hunk,
		Author:   string(c.Author),
		Resolved: c.Resolved,
		Created:  c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Target:   c.Target,
	}
}

func commentsToJSON(cs []store.Comment) []commentJSON {
	out := make([]commentJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, commentToJSON(c))
	}
	return out
}

// writeCommentTable renders comments for a human reader.
func writeCommentTable(w io.Writer, comments []store.Comment) error {
	if len(comments) == 0 {
		fmt.Fprintln(w, "no comments")
		return nil
	}
	for _, c := range comments {
		marker := " "
		if c.Resolved {
			marker = "✓"
		}
		fmt.Fprintf(w, "%s %-10s %-28s %s\n", marker, c.ID, c.Location(), firstLine(c.Body))
		for _, line := range strings.Split(c.Body, "\n")[1:] {
			fmt.Fprintf(w, "  %-10s %-28s %s\n", "", "", line)
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// parse runs a flag set for a command that takes no positional arguments.
func parse(fs *flag.FlagSet, args []string) error {
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("%s takes no arguments, got %q", fs.Name(), rest[0])
	}
	return nil
}

// parseArgs runs a flag set for a command that also takes positional
// arguments, and returns them.
//
// The standard library stops parsing at the first positional, which would make
// `peel pr submit 412 --yes` silently ignore --yes. Parsing repeatedly and
// peeling off one positional each round accepts flags in any position, which is
// how people actually type them.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageErrorf("%s: %v", fs.Name(), err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sorted(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
