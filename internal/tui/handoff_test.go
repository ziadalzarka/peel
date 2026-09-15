package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func handoffOf(comments []store.Comment) string {
	threads, _ := reviewThreads(comments)
	return commentHandoff(threads, nil)
}

// The handoff is one block per thread: where it was left, then its notes
// indented under it. Threads on the same file are grouped and ordered by line,
// however they were written, so the agent reads a file once.
func TestHandoffGroupsTheNotesByFile(t *testing.T) {
	got := handoffOf([]store.Comment{
		{File: "beta.txt", Line: 2, Side: store.SideNew, Body: "wrong fixture"},
		{File: "alpha.go", Line: 9, Side: store.SideNew, Body: "this leaks the tx"},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "name it"},
	})

	want := "beta.txt:2\n" +
		"  user: wrong fixture\n" +
		"\n" +
		"alpha.go:3\n" +
		"  user: name it\n" +
		"\n" +
		"alpha.go:9\n" +
		"  user: this leaks the tx\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
	if firstLineOf(got) != "Review comments copied from peel. Review them one by one." {
		t.Errorf("handoff opens with %q, want it to say where the notes came from and what to do with them", firstLineOf(got))
	}
}

func TestHandoffPutsTheNotesOnOneLineUnderOneAnchor(t *testing.T) {
	got := handoffOf([]store.Comment{
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "this leaks the tx", Author: store.AuthorUser},
		{File: "beta.txt", Line: 2, Side: store.SideNew, Body: "wrong fixture", Author: store.AuthorUser},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "closed it in the defer", Author: store.AuthorAgent},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "the retry still hides it", Author: store.AuthorUser},
	})

	want := "alpha.go:3\n" +
		"  user: this leaks the tx\n" +
		"  agent: closed it in the defer\n" +
		"  user: the retry still hides it\n" +
		"\n" +
		"beta.txt:2\n" +
		"  user: wrong fixture\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
	if n := strings.Count(got, "alpha.go:3"); n != 1 {
		t.Errorf("alpha.go:3 is named %d times, want once:\n%s", n, got)
	}
}

func TestHandoffCarriesTheResolvedNotesOfAThreadStillOpen(t *testing.T) {
	got := handoffOf([]store.Comment{
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "name it", Author: store.AuthorUser, Resolved: true},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "renamed", Author: store.AuthorAgent, Resolved: true},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "and the test too", Author: store.AuthorUser},
	})

	want := "alpha.go:3\n" +
		"  user (resolved): name it\n" +
		"  agent (resolved): renamed\n" +
		"  user: and the test too\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
}

func TestReviewThreadsLeaveOutAThreadWithNoOpenNoteOfTheReviewers(t *testing.T) {
	threads, resolved := reviewThreads([]store.Comment{
		{ID: "a1", File: "alpha.go", Line: 3, Body: "this drops the error", Author: store.AuthorAgent},
		{ID: "u1", File: "alpha.go", Line: 5, Body: "dealt with", Author: store.AuthorUser, Resolved: true},
		{ID: "a2", File: "alpha.go", Line: 5, Body: "still open on my side", Author: store.AuthorAgent},
		{ID: "u2", File: "beta.txt", Line: 2, Body: "wrong fixture", Author: store.AuthorUser},
	})

	if len(threads) != 1 || len(threads[0].notes) != 1 || threads[0].notes[0].ID != "u2" {
		t.Errorf("threads = %+v, want only the one holding u2", threads)
	}
	if resolved != 1 {
		t.Errorf("resolved left out = %d, want 1", resolved)
	}
}

func TestHandoffThreadsANoteOnARunWithTheNotesUnderItsLastLine(t *testing.T) {
	got := handoffOf([]store.Comment{
		{File: "alpha.go", Line: 4, Side: store.SideNew, Body: "why here", Author: store.AuthorAgent},
		{File: "alpha.go", Line: 2, EndLine: 4, Side: store.SideNew, Body: "these three", Author: store.AuthorUser},
		{File: "alpha.go", Line: 2, Side: store.SideNew, Body: "just this one", Author: store.AuthorUser},
	})

	want := "alpha.go:2\n" +
		"  user: just this one\n" +
		"\n" +
		"alpha.go:2-4\n" +
		"  agent: why here\n" +
		"  user: these three\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
}

func TestHandoffKeepsTheTwoSidesOfALineApart(t *testing.T) {
	threads, _ := reviewThreads([]store.Comment{
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "new", Author: store.AuthorUser},
		{File: "alpha.go", Line: 3, Side: store.SideOld, Body: "old", Author: store.AuthorUser},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Origin: store.OriginIndex, Body: "staged", Author: store.AuthorUser},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Origin: store.OriginWorktree, Body: "on disk", Author: store.AuthorUser},
	})

	if len(threads) != 3 {
		t.Fatalf("got %d threads, want new, old and staged apart: %+v", len(threads), threads)
	}
	if len(threads[0].notes) != 2 {
		t.Errorf("the note with no origin and the one on disk were split: %+v", threads[0].notes)
	}
}

// A line number the agent cannot read off disk is explained where it is used.
// The ordinary note — the new side of the working tree's change — is the file
// the agent is about to open, and says nothing extra.
func TestHandoffAnchorsSayWhichSideAndWhichLine(t *testing.T) {
	cases := []struct {
		name    string
		comment store.Comment
		gone    bool
		want    string
	}{
		{name: "new side", comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew}, want: "alpha.go:9"},
		{
			name:    "working tree",
			comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew, Origin: store.OriginWorktree},
			want:    "alpha.go:9",
		},
		{
			name:    "old side",
			comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideOld},
			want:    "alpha.go:9 (line number from the file before this change)",
		},
		{
			name:    "staged half",
			comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew, Origin: store.OriginIndex},
			want:    "alpha.go:9 (line number from the staged copy, not the file on disk)",
		},
		{
			name:    "old side of the staged half",
			comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideOld, Origin: store.OriginIndex},
			want:    "alpha.go:9 (line number from the committed file, before anything was staged)",
		},
		{name: "whole file", comment: store.Comment{File: "alpha.go", Side: store.SideNew}, want: "alpha.go"},
		{
			name:    "a file the change no longer touches",
			comment: store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew},
			gone:    true,
			want:    "alpha.go:9 (" + goneNote + ")",
		},
		{
			name:    "a whole-file note on one the change no longer touches",
			comment: store.Comment{File: "alpha.go", Side: store.SideNew},
			gone:    true,
			want:    "alpha.go (" + goneNote + ")",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := handoffAnchor(c.comment, c.gone); got != c.want {
				t.Errorf("handoffAnchor = %q, want %q", got, c.want)
			}
		})
	}
}

// Every line of a multi-line note is indented under its anchor, so the block a
// note occupies is unambiguous even when it has a blank line in it.
func TestHandoffIndentsEveryLineOfANote(t *testing.T) {
	got := handoffOf([]store.Comment{
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "this leaks the tx\n\nand the retry hides it\n"},
	})

	want := "alpha.go:3\n" +
		"  user: this leaks the tx\n" +
		"\n" +
		"    and the retry hides it\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
}

// The store's own ids and timestamps mean nothing outside peel, so they are not
// pasted into a conversation that cannot look them up.
func TestHandoffLeavesPeelsOwnBookkeepingOut(t *testing.T) {
	got := handoffOf([]store.Comment{
		{ID: "cmt_abc123", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "name it"},
	})

	if strings.Contains(got, "cmt_abc123") {
		t.Errorf("handoff carries the comment id:\n%s", got)
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
