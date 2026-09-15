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
// into a conversation: what the paste is, where each thread was left, and the
// notes in it. No IDs, no timestamps, nothing that only means something inside
// peel.

// handoffHeader says what the paste is and what to do with it, in one line above
// the notes.
//
// How to read an anchor is left to the anchor: what a line number counts lines
// in is said in brackets on the note that needs it, and "old side" would mean
// nothing to a reader who has not been told what the two sides are.
const handoffHeader = "Review comments copied from peel. Review them one by one."

type thread struct {
	key   threadKey
	notes []store.Comment
}

type threadKey struct {
	file     string
	line     int
	old      bool
	staged   bool
	outdated bool
}

func threadKeyOf(c store.Comment) threadKey {
	return threadKey{
		file:     c.File,
		line:     hangsOn(c.Line, c.EndLine),
		old:      c.Side == store.SideOld,
		staged:   c.Origin == store.OriginIndex,
		outdated: c.Outdated,
	}
}

func reviewThreads(comments []store.Comment, me store.Author) (copied []thread, resolvedLeftOut int) {
	var all []thread
	at := map[threadKey]int{}
	for _, c := range comments {
		key := threadKeyOf(c)
		i, seen := at[key]
		if !seen {
			i = len(all)
			at[key] = i
			all = append(all, thread{key: key})
		}
		all[i].notes = append(all[i].notes, c)
	}
	for _, t := range all {
		if t.hasOpenNoteOf(me) {
			copied = append(copied, t)
			continue
		}
		for _, c := range t.notes {
			if c.Author.Mine(me) && c.Resolved {
				resolvedLeftOut++
			}
		}
	}
	return inReadingOrder(copied), resolvedLeftOut
}

func (t thread) hasOpenNoteOf(me store.Author) bool {
	for _, c := range t.notes {
		if c.Author.Mine(me) && !c.Resolved {
			return true
		}
	}
	return false
}

func (t thread) span() store.Comment {
	span := t.notes[0]
	for _, c := range t.notes[1:] {
		span.Line = min(span.Line, c.Line)
	}
	span.EndLine = 0
	if t.key.line > span.Line {
		span.EndLine = t.key.line
	}
	return span
}

func noteCount(threads []thread) int {
	n := 0
	for _, t := range threads {
		n += len(t.notes)
	}
	return n
}

// commentHandoff renders review threads as text to hand an agent: one anchor per
// thread, with every note in it under the anchor in the order it was written.
//
// gone names the files the change no longer touches, which the reader has to be
// told about: the agent is being sent to a path on disk, and a note left on a
// change that has since gone is the one case where that path holds nothing the
// review was ever about.
func commentHandoff(threads []thread, gone map[string]bool) string {
	var b strings.Builder
	b.WriteString(handoffHeader)
	b.WriteString("\n")
	for _, t := range threads {
		fmt.Fprintf(&b, "\n%s\n", handoffAnchor(t.span(), gone[t.key.file]))
		for _, c := range t.notes {
			writeHandoffNote(&b, c)
		}
	}
	return b.String()
}

func writeHandoffNote(b *strings.Builder, c store.Comment) {
	lines := strings.Split(strings.TrimSpace(c.Body), "\n")
	fmt.Fprintf(b, "  %s: %s\n", handoffAuthor(c), strings.TrimRight(lines[0], " \t"))
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(b, "    %s\n", strings.TrimRight(line, " \t"))
	}
}

func handoffAuthor(c store.Comment) string {
	author := string(c.Author)
	if author == "" {
		author = string(store.AuthorUser)
	}
	if c.Resolved {
		author += " (resolved)"
	}
	return author
}

// handoffAnchor names where a note was left, in the file:line form every tool
// prints paths in, saying what the number counts lines in when that is not the
// file the agent is about to open.
func handoffAnchor(c store.Comment, gone bool) string {
	if c.Line <= 0 {
		if gone {
			return c.File + " (" + goneNote + ")"
		}
		return c.File
	}
	anchor := c.Location()
	if gone {
		return anchor + " (" + goneNote + ")"
	}
	if note := lineNumberNote(c); note != "" {
		return anchor + " (" + note + ")"
	}
	return anchor
}

// goneNote warns that the change a note was written on is not in the diff any
// more — committed, stashed, or put back.
//
// Which of those it was decides whether the line number still names the same
// code, and peel cannot tell them apart from here. So the note says the one
// thing that is true of all three: what the number meant was measured against a
// change the reader will not find by reading the file.
const goneNote = "this file is not part of the change under review any more; " +
	"the line number is where the note was written"

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

// inReadingOrder groups the threads by file and orders each file's by line,
// keeping the files in the order they were first commented on.
func inReadingOrder(threads []thread) []thread {
	first := map[string]int{}
	for _, t := range threads {
		if _, seen := first[t.key.file]; !seen {
			first[t.key.file] = len(first)
		}
	}
	sort.SliceStable(threads, func(i, j int) bool {
		if first[threads[i].key.file] != first[threads[j].key.file] {
			return first[threads[i].key.file] < first[threads[j].key.file]
		}
		return threads[i].key.line < threads[j].key.line
	})
	return threads
}
