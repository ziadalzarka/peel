package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
)

// runHunks dispatches the `peel hunks` group.
//
// There is deliberately no `hunks add`: peel never writes to the index on
// behalf of a script or an agent. Anything automating git can call git add
// directly, so exposing it here would only add a way for changes to be staged
// without having been reviewed.
func runHunks(ctx context.Context, c *CLI, args []string) error {
	if len(args) == 0 {
		return subcommandUsage("hunks", []string{"list"})
	}
	switch args[0] {
	case "list":
		return hunksList(ctx, c, args[1:])
	case "add", "rm", "stage", "unstage":
		return usageErrorf("peel does not stage from the command line — stage in the review UI, " +
			"or use git add directly")
	default:
		return usageErrorf("unknown hunks subcommand %q; want list", args[0])
	}
}

func hunksList(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("hunks list")
	asJSON := fs.Bool("json", false, "emit JSON")
	file := fs.String("file", "", "only hunks in this path")
	stagedOnly := fs.Bool("staged", false, "only hunks already in the index")
	unstagedOnly := fs.Bool("unstaged", false, "only hunks not yet in the index")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *stagedOnly && *unstagedOnly {
		return usageErrorf("--staged and --unstaged are mutually exclusive")
	}

	_, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}

	hunks := collectHunks(s, *file, *stagedOnly, *unstagedOnly)
	if *asJSON {
		return writeJSON(c.Stdout, hunks)
	}
	return writeHunkTable(c.Stdout, hunks, s)
}

// hunkJSON is the wire shape for a hunk.
type hunkJSON struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Staged  bool   `json:"staged"`
	Header  string `json:"header"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// Section is the enclosing context git prints after the @@ marker.
	Section string `json:"section,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// collectHunks flattens a session into addressable hunks, staged side first so
// each file reads index-then-worktree.
func collectHunks(s *app.Session, file string, stagedOnly, unstagedOnly bool) []hunkJSON {
	out := []hunkJSON{}
	for _, entry := range s.Files {
		if file != "" && entry.Path != file {
			continue
		}
		if entry.IsBinary() {
			out = append(out, hunkJSON{
				ID:     entry.Path,
				File:   entry.Path,
				Staged: entry.Staged != nil,
				Header: "binary file",
				Binary: true,
			})
			continue
		}

		sides := []struct {
			diff   *git.FileDiff
			staged bool
		}{
			{entry.Staged, true},
			{entry.Unstaged, false},
		}
		for _, side := range sides {
			if side.diff == nil {
				continue
			}
			if stagedOnly && !side.staged {
				continue
			}
			if unstagedOnly && side.staged {
				continue
			}
			for _, h := range side.diff.Hunks {
				added, removed := h.Stats()
				out = append(out, hunkJSON{
					ID:      side.diff.ID(h).String(),
					File:    entry.Path,
					Staged:  side.staged,
					Header:  h.Header(),
					Added:   added,
					Removed: removed,
					Section: h.Section,
				})
			}
		}
	}
	return out
}

func writeHunkTable(w io.Writer, hunks []hunkJSON, s *app.Session) error {
	if len(hunks) == 0 {
		fmt.Fprintf(w, "no changes in %s\n", s.Title)
		return nil
	}

	var lastFile string
	for _, h := range hunks {
		if h.File != lastFile {
			if lastFile != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, h.File)
			lastFile = h.File
		}
		state := "unstaged"
		if h.Staged {
			state = "staged"
		}
		if h.Binary {
			fmt.Fprintf(w, "  %-8s binary file — no diff to show\n", state)
			continue
		}
		fmt.Fprintf(w, "  %-8s %+d/-%d  %s\n", state, h.Added, h.Removed, h.ID)
		if h.Section != "" {
			fmt.Fprintf(w, "  %-8s        %s\n", "", strings.TrimSpace(h.Section))
		}
	}
	return nil
}
