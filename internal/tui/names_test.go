package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func reviewedByMany(t *testing.T) *fakeBackend {
	t.Helper()
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "mine", File: "alpha.go", Line: 3, Body: "mine, by name", Author: "ziadalzarka"},
		{ID: "old", File: "alpha.go", Line: 4, Body: "mine, from before names", Author: store.AuthorUser},
		{ID: "claude", File: "alpha.go", Line: 4, Body: "claude's", Author: "claude"},
		{ID: "unknown", File: "beta.txt", Line: 2, Body: "nobody said", Author: store.AuthorUnknown},
		{ID: "octocat", File: "beta.txt", Line: 2, Body: "from the pull request", Author: "octocat"},
	}
	return backend
}

func TestANoteWrittenInTheReviewIsSignedWithTheReviewersName(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend, WithAuthor("ziadalzarka"))

	press(t, m, "j", "c")
	typeText(t, m, "needs a test")
	press(t, m, "enter")

	if len(backend.added) != 1 || backend.added[0].Author != "ziadalzarka" {
		t.Fatalf("added = %+v, want one note signed ziadalzarka", backend.added)
	}
	if !strings.Contains(m.View(), "ziadalzarka: needs a test") {
		t.Errorf("view does not show the note under the reviewer's name:\n%s", m.View())
	}
}

func TestAHidesEveryoneElsesNotesAndLeavesTheReviewersOwn(t *testing.T) {
	backend := reviewedByMany(t)
	m := newModel(t, backend, WithAuthor("ziadalzarka"))

	press(t, m, "A")

	for _, id := range []string{"claude", "unknown", "octocat"} {
		if row := m.doc.RowOfComment(id); row >= 0 {
			t.Errorf("%s's note is still on row %d", id, row)
		}
	}
	for _, id := range []string{"mine", "old"} {
		if m.doc.RowOfComment(id) < 0 {
			t.Errorf("the reviewer's note %s was hidden with the rest", id)
		}
	}
	if !strings.Contains(m.status, "3 comments by others hidden") {
		t.Errorf("status = %q", m.status)
	}
}

func TestXDeletesEveryoneElsesNotesAndNoneOfTheReviewers(t *testing.T) {
	backend := reviewedByMany(t)
	m := newModel(t, backend, WithAuthor("ziadalzarka"))

	press(t, m, "X", "y")

	if got := strings.Join(backend.removed, " "); got != "claude unknown octocat" {
		t.Errorf("removed %q, want every note not signed by the reviewer", got)
	}
}

func TestSomeoneElsesNoteIsNotEditableAndSaysWhose(t *testing.T) {
	backend := reviewedByMany(t)
	m := newModel(t, backend, WithAuthor("ziadalzarka"))

	m.moveTo(m.doc.RowOfComment("octocat"))
	press(t, m, "e")
	if m.mode != modeBrowse || !strings.Contains(m.status, "octocat's") {
		t.Errorf("mode = %v, status = %q, want the editor shut and whose note it is", m.mode, m.status)
	}

	m.moveTo(m.doc.RowOfComment("mine"))
	press(t, m, "e")
	if m.mode == modeBrowse {
		t.Errorf("status = %q, want the editor open on the reviewer's own note", m.status)
	}
}

func TestReviewThreadsCountTheReviewersNameAsTheirs(t *testing.T) {
	threads, _ := reviewThreads([]store.Comment{
		{File: "alpha.go", Line: 3, Body: "by name", Author: "ziadalzarka"},
		{File: "alpha.go", Line: 5, Body: "claude only", Author: "claude"},
	}, "ziadalzarka")

	if len(threads) != 1 || threads[0].notes[0].Body != "by name" {
		t.Errorf("threads = %+v, want only the thread the reviewer wrote in", threads)
	}
}
