package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/store"
)

func runWalkthrough(ctx context.Context, c *CLI, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "set", "clear":
			return changeWalkthrough(ctx, c, args[0], args[1:])
		}
	}
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
	fmt.Fprintln(c.Stdout, got.Markdown())
	return nil
}

func changeWalkthrough(ctx context.Context, c *CLI, action string, args []string) error {
	fs := newFlagSet("walkthrough " + action)
	author := fs.String("author", "", "who wrote the walkthrough (required)")
	if err := parse(fs, args); err != nil {
		return err
	}
	name := store.Author(*author)
	if *author == "" {
		return usageErrorf("walkthrough %s needs --author", action)
	}
	if !name.Valid() {
		return usageErrorf("--author %q is not a name", *author)
	}

	var steps []store.WalkthroughStep
	if action == "set" {
		decoder := json.NewDecoder(c.Stdin)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&steps); err != nil {
			return usageErrorf("walkthrough set reads a JSON array of steps on stdin: %v", err)
		}
		if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
			return usageErrorf("walkthrough set reads one JSON array on stdin")
		}
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	if action == "clear" {
		return a.ClearWalkthrough(s, name)
	}
	if err := a.SetWalkthrough(s, name, steps); err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "saved %s by %s\n", plural(len(steps), "walkthrough step"), name)
	return nil
}
