package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ziadalzarka/peel/internal/store"
)

// This is the paste path to an agent, for the times the store is not one.
//
// An agent working in the repository reads `peel comment list --json` and needs
// nothing from here. An agent in a browser tab, or one on another machine, has
// no store to read — so `C` renders the same notes as text meant to be pasted
// into a conversation: what the paste is, where each note was left, and the note
// itself. No IDs, no timestamps, nothing that only means something inside peel.

// handoffPreamble tells the agent how to read the anchors that follow. It is
// written once, above the notes, rather than annotated onto each one.
const handoffPreamble = `Address each one. The path and line say where the note was left: a note with no
line is about the file as a whole, and "old side" means the line number is from
the file before the change.`

// commentHandoff renders review notes as text to hand an agent.
//
// The notes are grouped by file, in the order the files are first commented on
// and by line within a file, so the agent reads a file's notes together instead
// of being sent back and forth in the order they happened to be written.
func commentHandoff(comments []store.Comment) string {
	var b strings.Builder
	b.WriteString("Review comments copied from peel.\n\n")
	b.WriteString(handoffPreamble)
	b.WriteString("\n")
	for _, c := range inReadingOrder(comments) {
		fmt.Fprintf(&b, "\n%s\n", handoffAnchor(c))
		for _, line := range strings.Split(strings.TrimSpace(c.Body), "\n") {
			if strings.TrimSpace(line) == "" {
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(&b, "  %s\n", strings.TrimRight(line, " \t"))
		}
	}
	return b.String()
}

// handoffAnchor names where a note was left, in the file:line form every tool
// prints paths in — the side only when it is the one that needs saying.
func handoffAnchor(c store.Comment) string {
	if c.Line <= 0 {
		return c.File
	}
	if c.Side == store.SideOld {
		return fmt.Sprintf("%s:%d (old side)", c.File, c.Line)
	}
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}

// inReadingOrder groups the notes by file and orders each file's by line,
// keeping the files in the order they were first commented on.
func inReadingOrder(comments []store.Comment) []store.Comment {
	first := map[string]int{}
	for _, c := range comments {
		if _, seen := first[c.File]; !seen {
			first[c.File] = len(first)
		}
	}
	out := append([]store.Comment(nil), comments...)
	sort.SliceStable(out, func(i, j int) bool {
		if first[out[i].File] != first[out[j].File] {
			return first[out[i].File] < first[out[j].File]
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// stillOpen splits the notes into the ones left to deal with and a count of the
// ones already resolved.
//
// A resolved note has been dealt with, so handing it to an agent asked to
// address the review would only send it after work already done. The count comes
// back so the footer can say those notes were left out rather than dropping them
// silently.
func stillOpen(comments []store.Comment) (open []store.Comment, resolved int) {
	for _, c := range comments {
		if c.Resolved {
			resolved++
			continue
		}
		open = append(open, c)
	}
	return open, resolved
}
