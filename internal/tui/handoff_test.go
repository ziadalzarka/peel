package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

// The handoff is one block per note: where it was left, then the note indented
// under it. Notes on the same file are grouped and ordered by line, however they
// were written, so the agent reads a file once.
func TestHandoffGroupsTheNotesByFile(t *testing.T) {
	got := commentHandoff([]store.Comment{
		{File: "beta.txt", Line: 2, Side: store.SideNew, Body: "wrong fixture"},
		{File: "alpha.go", Line: 9, Side: store.SideNew, Body: "this leaks the tx"},
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "name it"},
	})

	want := "beta.txt:2\n" +
		"  wrong fixture\n" +
		"\n" +
		"alpha.go:3\n" +
		"  name it\n" +
		"\n" +
		"alpha.go:9\n" +
		"  this leaks the tx\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
	if firstLineOf(got) != "Review comments copied from peel. Review them one by one." {
		t.Errorf("handoff opens with %q, want it to say where the notes came from and what to do with them", firstLineOf(got))
	}
}

// A line number the agent cannot read off disk is explained where it is used.
// The ordinary note — the new side of the working tree's change — is the file
// the agent is about to open, and says nothing extra.
func TestHandoffAnchorsSayWhichSideAndWhichLine(t *testing.T) {
	cases := []struct {
		name    string
		comment store.Comment
		want    string
	}{
		{"new side", store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew}, "alpha.go:9"},
		{
			"working tree",
			store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew, Origin: store.OriginWorktree},
			"alpha.go:9",
		},
		{
			"old side",
			store.Comment{File: "alpha.go", Line: 9, Side: store.SideOld},
			"alpha.go:9 (line number from the file before this change)",
		},
		{
			"staged half",
			store.Comment{File: "alpha.go", Line: 9, Side: store.SideNew, Origin: store.OriginIndex},
			"alpha.go:9 (line number from the staged copy, not the file on disk)",
		},
		{
			"old side of the staged half",
			store.Comment{File: "alpha.go", Line: 9, Side: store.SideOld, Origin: store.OriginIndex},
			"alpha.go:9 (line number from the committed file, before anything was staged)",
		},
		{"whole file", store.Comment{File: "alpha.go", Side: store.SideNew}, "alpha.go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := handoffAnchor(c.comment); got != c.want {
				t.Errorf("handoffAnchor = %q, want %q", got, c.want)
			}
		})
	}
}

// Every line of a multi-line note is indented under its anchor, so the block a
// note occupies is unambiguous even when it has a blank line in it.
func TestHandoffIndentsEveryLineOfANote(t *testing.T) {
	got := commentHandoff([]store.Comment{
		{File: "alpha.go", Line: 3, Side: store.SideNew, Body: "this leaks the tx\n\nand the retry hides it\n"},
	})

	want := "alpha.go:3\n" +
		"  this leaks the tx\n" +
		"\n" +
		"  and the retry hides it\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("handoff =\n%s\nwant it to end with\n%s", got, want)
	}
}

// The store's own ids and timestamps mean nothing outside peel, so they are not
// pasted into a conversation that cannot look them up.
func TestHandoffLeavesPeelsOwnBookkeepingOut(t *testing.T) {
	got := commentHandoff([]store.Comment{
		{ID: "cmt_abc123", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "name it"},
	})

	if strings.Contains(got, "cmt_abc123") {
		t.Errorf("handoff carries the comment id:\n%s", got)
	}
}

func TestStillOpenLeavesTheResolvedNotesOutAndCountsThem(t *testing.T) {
	open, resolved := stillOpen([]store.Comment{
		{ID: "c1", File: "alpha.go", Body: "one"},
		{ID: "c2", File: "alpha.go", Body: "two", Resolved: true},
		{ID: "c3", File: "beta.txt", Body: "three"},
	})

	if len(open) != 2 || open[0].ID != "c1" || open[1].ID != "c3" {
		t.Errorf("open = %+v, want c1 and c3", open)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1", resolved)
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
