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

// handoffHeader says what the paste is and what to do with it, in one line above
// the notes.
//
// How to read an anchor is left to the anchor: what a line number counts lines
// in is said in brackets on the note that needs it, and "old side" would mean
// nothing to a reader who has not been told what the two sides are.
const handoffHeader = "Review comments copied from peel. Review them one by one."

// commentHandoff renders review notes as text to hand an agent.
//
// The notes are grouped by file, in the order the files are first commented on
// and by line within a file, so the agent reads a file's notes together instead
// of being sent back and forth in the order they happened to be written.
func commentHandoff(comments []store.Comment) string {
	var b strings.Builder
	b.WriteString(handoffHeader)
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
// prints paths in, saying what the number counts lines in when that is not the
// file the agent is about to open.
func handoffAnchor(c store.Comment) string {
	if c.Line <= 0 {
		return c.File
	}
	anchor := c.Location()
	if note := lineNumberNote(c); note != "" {
		return anchor + " (" + note + ")"
	}
	return anchor
}

// lineNumberNote explains a line number the agent cannot take at face value.
//
// A note on a removed line is numbered against the file before the change, and a
// note on the staged half of a part-staged file is numbered against the copy in
// the index — neither of which is what the agent will read off disk. Neither is
// a line whose code has since been rewritten: that one names nothing at all now,
// and an agent sent to it would edit whatever took its place.
func lineNumberNote(c store.Comment) string {
	if c.Outdated {
		return "the code this was written on has since changed; the line number is where it was"
	}
	old, staged := c.Side == store.SideOld, c.Origin == store.OriginIndex
	switch {
	case old && staged:
		return "line number from the committed file, before anything was staged"
	case old:
		return "line number from the file before this change"
	case staged:
		return "line number from the staged copy, not the file on disk"
	}
	return ""
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
