package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
)

// runWalkthrough generates or shows the AI narrative for the session.
func runWalkthrough(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("walkthrough")
	regen := fs.Bool("regen", false, "ignore the cached narrative and generate a new one")
	provider := fs.String("provider", c.ui.Provider, "AI provider to use (default: first available)")
	instruction := fs.String("prompt", "", "replace the default instruction given to the provider")
	if err := parse(fs, args); err != nil {
		return err
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	got, err := a.Walkthrough(ctx, s, app.WalkthroughRequest{
		Provider:    *provider,
		Regenerate:  *regen,
		Instruction: *instruction,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Stdout, got.Body)
	return nil
}

// runPR dispatches the `peel pr` group.
func runPR(ctx context.Context, c *CLI, args []string) error {
	subs := map[string]func(context.Context, *CLI, []string) error{
		"view":   prView,
		"submit": prSubmit,
	}
	if len(args) == 0 {
		return subcommandUsage("pr", keysOf(subs))
	}
	run, ok := subs[args[0]]
	if !ok {
		return usageErrorf("unknown pr subcommand %q; want one of %s", args[0], strings.Join(sorted(keysOf(subs)), ", "))
	}
	return run(ctx, c, args[1:])
}

func prView(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("pr view")
	asJSON := fs.Bool("json", false, "emit JSON")
	positional, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		c.pr = positional[0]
	}
	if c.pr == "" {
		return usageErrorf("pr view needs a pull request: peel pr view <ref>, or peel --pr <ref> pr view")
	}

	_, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	pr := s.PR
	if pr == nil {
		return fmt.Errorf("no pull request loaded")
	}

	if *asJSON {
		return writeJSON(c.Stdout, map[string]any{
			"number":  pr.Ref.Number,
			"repo":    pr.Ref.Slug(),
			"title":   pr.Title,
			"author":  pr.Author,
			"base":    pr.BaseRef,
			"head":    pr.HeadRef,
			"url":     pr.URL,
			"state":   pr.State,
			"draft":   pr.Draft,
			"files":   s.Paths(),
			"comment": pr.Body,
		})
	}

	added, removed := s.Stats()
	fmt.Fprintf(c.Stdout, "%s\n", pr.Describe())
	fmt.Fprintf(c.Stdout, "%s → %s · %s\n", pr.HeadRef, pr.BaseRef, pr.State)
	fmt.Fprintf(c.Stdout, "%s, +%d -%d\n", plural(len(s.Files), "file"), added, removed)
	if pr.URL != "" {
		fmt.Fprintf(c.Stdout, "%s\n", pr.URL)
	}
	return nil
}

func prSubmit(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("pr submit")
	body := fs.String("body", "", "summary comment for the review")
	event := fs.String("event", string(forge.EventComment), "comment, approve, or request-changes")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "show what would be posted and exit")
	keep := fs.Bool("keep-unresolved", false, "leave submitted comments unresolved locally")
	positional, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		c.pr = positional[0]
	}
	if c.pr == "" {
		return usageErrorf("pr submit needs a pull request: peel pr submit <ref>, or peel --pr <ref> pr submit")
	}

	reviewEvent, err := parseEvent(*event)
	if err != nil {
		return err
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	opts := app.SubmitOptions{
		Body:         *body,
		Event:        reviewEvent,
		ResolveAfter: !*keep,
	}
	preview, err := a.PreviewSubmission(s, opts)
	if err != nil {
		return err
	}

	printPreview(c, s, preview)
	if *dryRun {
		fmt.Fprintln(c.Stdout, "\ndry run — nothing was posted")
		return nil
	}

	// Posting a review is visible to other people and cannot be undone from
	// here, so it needs an explicit yes.
	if !*yes {
		ok, err := confirm(c, fmt.Sprintf("Post this review to %s?", s.PR.Ref))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(c.Stdout, "cancelled — nothing was posted")
			return nil
		}
	}

	if err := a.SubmitReview(ctx, s, opts); err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "posted %s to %s\n", plural(len(preview.Comments), "comment"), s.PR.Ref)
	if pr := s.PR; pr.URL != "" {
		fmt.Fprintln(c.Stdout, pr.URL)
	}
	return nil
}

func parseEvent(s string) (forge.ReviewEvent, error) {
	switch strings.ToLower(strings.ReplaceAll(s, "_", "-")) {
	case "comment":
		return forge.EventComment, nil
	case "approve":
		return forge.EventApprove, nil
	case "request-changes":
		return forge.EventRequestChanges, nil
	default:
		return "", usageErrorf("unknown review event %q; want comment, approve, or request-changes", s)
	}
}

// printPreview shows exactly what would be sent, so the confirmation is
// against something concrete rather than a count.
func printPreview(c *CLI, s *app.Session, review forge.Review) {
	fmt.Fprintf(c.Stdout, "Review for %s (%s)\n", s.PR.Ref, strings.ToLower(string(review.Event)))
	if review.Body != "" {
		fmt.Fprintf(c.Stdout, "\n%s\n", review.Body)
	}
	if len(review.Comments) == 0 {
		fmt.Fprintln(c.Stdout, "\nno inline comments")
		return
	}
	fmt.Fprintf(c.Stdout, "\n%s:\n", plural(len(review.Comments), "inline comment"))
	for _, comment := range review.Comments {
		fmt.Fprintf(c.Stdout, "  %s:%d (%s)\n", comment.Path, comment.Line, strings.ToLower(comment.Side))
		for _, line := range strings.Split(comment.Body, "\n") {
			fmt.Fprintf(c.Stdout, "    %s\n", line)
		}
	}
}

// confirm asks a yes/no question on stdin, defaulting to no.
func confirm(c *CLI, question string) (bool, error) {
	fmt.Fprintf(c.Stdout, "\n%s [y/N] ", question)

	line, err := bufio.NewReader(c.Stdin).ReadString('\n')
	if err != nil && line == "" {
		// No answer available — treat as declined rather than assuming yes.
		fmt.Fprintln(c.Stdout)
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
