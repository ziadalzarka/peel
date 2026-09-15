package tui

import (
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func foldFixture(t *testing.T, resolved bool) (*fakeBackend, *Model) {
	t.Helper()
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Body: "first line\nsecond line\nthird line",
			Author: store.AuthorUser, Resolved: resolved},
	}
	return backend, newModel(t, backend)
}

func rowsOfComment(m *Model, id string) int {
	n := 0
	for _, r := range m.doc.Rows {
		if r.Kind == RowComment && m.doc.Comments[r.Comment].ID == id {
			n++
		}
	}
	return n
}

func TestResolvingACommentFoldsItToOneLine(t *testing.T) {
	backend, m := foldFixture(t, false)
	if got := rowsOfComment(m, "c1"); got != 3 {
		t.Fatalf("an open comment takes %d rows, want 3", got)
	}

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "x")

	if !backend.resolved["c1"] {
		t.Fatal("x did not resolve the comment")
	}
	if got := rowsOfComment(m, "c1"); got != 1 {
		t.Errorf("a resolved comment takes %d rows, want 1", got)
	}
	if got := m.doc.Rows[m.doc.RowOfComment("c1")].Text; got != "first line …" {
		t.Errorf("folded comment reads %q, want its first line and a mark that there is more", got)
	}

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, "x")
	if got := rowsOfComment(m, "c1"); got != 3 {
		t.Errorf("a reopened comment takes %d rows, want 3", got)
	}
}

func TestSpaceUnfoldsAResolvedCommentAndFoldsItAgain(t *testing.T) {
	_, m := foldFixture(t, true)
	if got := rowsOfComment(m, "c1"); got != 1 {
		t.Fatalf("a resolved comment opened taking %d rows, want 1", got)
	}

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, " ")
	if got := rowsOfComment(m, "c1"); got != 3 {
		t.Errorf("space left the resolved comment taking %d rows, want 3", got)
	}
	if m.collapsed["alpha.go"] {
		t.Error("space on a comment folded the file it is in")
	}
	if c, ok := m.doc.CommentAt(m.cursor); !ok || c.ID != "c1" {
		t.Errorf("cursor at row %d, want it still on the comment", m.cursor)
	}

	m.moveTo(m.doc.RowOfComment("c1") + 2)
	press(t, m, " ")
	if got := rowsOfComment(m, "c1"); got != 1 {
		t.Errorf("space from the comment's last line left it taking %d rows, want 1", got)
	}
	if m.collapsed["alpha.go"] {
		t.Error("space on a comment folded the file it is in")
	}
	if want := m.doc.RowOfComment("c1"); m.cursor != want {
		t.Errorf("cursor at row %d, want it on the folded comment at %d", m.cursor, want)
	}
}

func TestSpaceFoldsAnOpenCommentAndResolvingItKeepsItFolded(t *testing.T) {
	_, m := foldFixture(t, false)

	m.moveTo(m.doc.RowOfComment("c1"))
	press(t, m, " ")
	if got := rowsOfComment(m, "c1"); got != 1 {
		t.Fatalf("space left the open comment taking %d rows, want 1", got)
	}

	press(t, m, "x")
	if got := rowsOfComment(m, "c1"); got != 1 {
		t.Errorf("resolving a folded comment left it taking %d rows, want 1", got)
	}
}

func TestSpaceOnAFileHeaderStillFoldsTheFile(t *testing.T) {
	_, m := foldFixture(t, true)

	m.moveTo(m.doc.RowOfFile(m.fileIndex("alpha.go")))
	press(t, m, " ")
	if !m.collapsed["alpha.go"] {
		t.Error("space on the file header did not fold the file")
	}
}
