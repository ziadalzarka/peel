package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func TestACommentFromThePullRequestIsNotPostedOrEditable(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "github-1", File: "alpha.go", Line: 3, Body: "from the pull request", Author: "ziadalzarka", Remote: "github"},
		{ID: "u1", File: "alpha.go", Line: 4, Body: "written here", Author: "ziadalzarka"},
	}
	m := newModel(t, backend, WithAuthor("ziadalzarka"))

	if inline, _ := m.pendingReview(); inline != 1 {
		t.Errorf("pending inline comments = %d, want only the one written here", inline)
	}

	m.moveTo(m.doc.RowOfComment("github-1"))
	press(t, m, "e")
	if m.mode != modeBrowse || !strings.Contains(m.status, "github") {
		t.Errorf("mode = %v, status = %q, want the editor shut and where the note came from", m.mode, m.status)
	}
}
