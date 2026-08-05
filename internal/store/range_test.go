package store

import (
	"os"
	"strings"
	"testing"
)

func TestANoteOnARunOfLinesKeepsBothEnds(t *testing.T) {
	s := newTestStore(t)

	added, err := s.Add(Comment{
		File: "svc.go", Line: 10, EndLine: 14, Side: SideNew,
		Origin: OriginWorktree, Body: "these five belong together", Author: AuthorUser,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.Get(added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EndLine != 14 {
		t.Errorf("end line = %d, want 14 kept with the note", got.EndLine)
	}
	if want := "svc.go:10-14"; got.Location() != want {
		t.Errorf("location = %q, want %q", got.Location(), want)
	}

	// A note on one line has no range, and writing a zero into every stored note
	// would date every file peel has already written.
	single, err := s.Add(Comment{File: "svc.go", Line: 3, Body: "one line", Author: AuthorUser})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if single.Location() != "svc.go:3" {
		t.Errorf("location = %q, want svc.go:3", single.Location())
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Count(string(raw), "endLine") != 1 {
		t.Errorf("stored file mentions endLine %d times, want only the note that has one:\n%s",
			strings.Count(string(raw), "endLine"), raw)
	}
}

// A range is a claim about the lines between its two numbers, so the two numbers
// have to be able to have lines between them.
func TestARangeThatNamesNoLinesIsRefused(t *testing.T) {
	for _, c := range []Comment{
		{File: "svc.go", Line: 14, EndLine: 10, Body: "backwards", Author: AuthorUser},
		{File: "svc.go", EndLine: 10, Body: "no line to run from", Author: AuthorUser},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("comment on %d-%d was accepted", c.Line, c.EndLine)
		}
	}
}
