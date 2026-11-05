package cli

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
)

func runGoto(ctx context.Context, c *CLI, args []string) error {
	if len(args) != 1 {
		return usageErrorf("goto needs one place: <file> or <file>:<line>")
	}
	file, line, err := parsePlace(args[0])
	if err != nil {
		return err
	}
	a, err := c.open(ctx)
	if err != nil {
		return err
	}
	target := ""
	if c.pr != "" {
		if target, err = a.PullRequestTarget(ctx, c.forgeName, c.pr); err != nil {
			return err
		}
	}
	if err := a.AskToJump(target, file, line); err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "asked the open review to go to %s\n", args[0])
	return nil
}

func parsePlace(place string) (string, int, error) {
	file, line := place, 0
	if at := strings.LastIndex(place, ":"); at >= 0 {
		if n, err := strconv.Atoi(place[at+1:]); err == nil {
			if n <= 0 {
				return "", 0, usageErrorf("line %d is not a line number", n)
			}
			file, line = place[:at], n
		}
	}
	if strings.TrimSpace(file) == "" {
		return "", 0, usageErrorf("goto needs a file")
	}
	return path.Clean(file), line, nil
}
