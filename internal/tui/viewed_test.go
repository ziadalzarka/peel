package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

const growingHunkFile = `diff --git a/list.txt b/list.txt
index 1111111..2222222 100644
--- a/list.txt
+++ b/list.txt
@@ -1,3 +1,4 @@
 one
+one and a half
 two
 three
@@ -8,3 +9,3 @@
 eight
-nine
+NINE
 ten
`

const markdownBody = "## Why\r\n\r\nThe **key** was never read.\r\n<!-- template: delete me -->\r\n\r\n\r\n" +
	"| a | b |\r\n|---|---|\r\n| 1 | 2 |\r\n\r\n```go\r\nx := 1\r\n```\r\n"

func pullRequestOf(t *testing.T, diff, body string) *app.Session {
	t.Helper()
	s := newSession(t, diff)
	s.Title = "#412 Drop the document key"
	s.Target = "github:cli/cli#412"
	s.PR = &forge.PullRequest{
		Ref:     forge.Ref{Owner: "cli", Repo: "cli", Number: 412},
		Title:   "Drop the document key",
		Body:    body,
		BaseRef: "main",
		HeadRef: "drop-key",
	}
	return s
}

func withFiles(s *app.Session, files []git.FileEntry) *app.Session {
	out := *s
	out.Files = files
	return &out
}

func paneShows(view, mark, path string) bool {
	return strings.Contains(view, mark+" "+path)
}

func TestSInAPullRequestMarksTheFileViewed(t *testing.T) {
	session := pullRequestOf(t, twoFileDiff, "")
	backend := newFakeBackend(session)
	backend.nextSession = withFiles(session, []git.FileEntry{app.FileViewed(session.Files[0], true), session.Files[1]})
	m := newModel(t, backend)

	press(t, m, "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("marked %v, want [alpha.go]", got)
	}
	if !m.doc.Files[0].Collapsed {
		t.Error("the viewed file did not fold away")
	}
	if m.doc.FileAt(m.cursor) != 1 {
		t.Errorf("cursor on file %d, want it carried on to beta.txt", m.doc.FileAt(m.cursor))
	}
	if !strings.Contains(m.status, "viewed alpha.go") {
		t.Errorf("status = %q, want it to say the file was viewed", m.status)
	}
	view := m.View()
	if !paneShows(view, "✓", "alpha.go") || paneShows(view, "✓", "beta.txt") {
		t.Errorf("the file tree does not tick the viewed file alone:\n%s", view)
	}
	if !strings.Contains(view, "1/2 viewed") {
		t.Errorf("the header does not count what is viewed:\n%s", view)
	}
	if strings.Contains(view, "read-only") {
		t.Errorf("a pull request that can be marked viewed still says read-only:\n%s", view)
	}
}

func TestSpaceFoldsAPullRequestFileWithoutMarkingItViewed(t *testing.T) {
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, ""))
	m := newModel(t, backend)

	press(t, m, "space")

	if len(backend.stagedFiles) != 0 {
		t.Fatalf("folding marked %v viewed", backend.stagedFiles)
	}
	if !m.doc.Files[0].Collapsed {
		t.Fatal("space did not fold the file")
	}
	view := m.View()
	if paneShows(view, "✓", "alpha.go") || !strings.Contains(view, "0/2 viewed") {
		t.Errorf("a folded file reads as viewed:\n%s", view)
	}

	press(t, m, "alt+up", "s")
	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Errorf("s on the folded file marked %v, want [alpha.go]", got)
	}
}

func TestSByHunkInAPullRequestMarksOneHunkViewedWithItsNumbersKept(t *testing.T) {
	session := pullRequestOf(t, growingHunkFile, "")
	backend := newFakeBackend(session)
	m := hunkModel(t, backend)
	atHunk(t, m, 0)
	id := m.doc.Hunks[0].ID
	part, _ := app.HunkViewed(session.Files[0], id)
	backend.nextSession = withFiles(session, []git.FileEntry{part})

	press(t, m, "s")

	if len(backend.stagedHunks) != 1 || backend.stagedHunks[0] != id {
		t.Fatalf("marked %v, want [%v]", backend.stagedHunks, id)
	}
	if !strings.Contains(m.status, "viewed one hunk of list.txt") {
		t.Errorf("status = %q", m.status)
	}
	view := m.View()
	for _, want := range []string{"▸ viewed", "not viewed yet", "viewed +1 -0", "unviewed +1 -1"} {
		if !strings.Contains(view, want) {
			t.Errorf("the part-viewed file does not say %q:\n%s", want, view)
		}
	}
	if !paneShows(view, "●", "list.txt") {
		t.Errorf("the file tree does not mark the part-viewed file:\n%s", view)
	}
}

func TestAViewedHunkIsDrawnWithItsNumbersKept(t *testing.T) {
	session := pullRequestOf(t, growingHunkFile, "")
	d := session.Files[0].Unstaged
	id := d.ID(d.Hunks[0])

	viewed, entry, ok := restagedHunk(session, id)
	if !ok || entry.State() != git.StatePartial {
		t.Fatalf("restagedHunk = %v, %v", entry.State(), ok)
	}
	if left := viewed.Files[0].Unstaged.Hunks[0]; left.OldStart != 8 || left.NewStart != 9 {
		t.Errorf("the hunk left to view reads -%d +%d, want -8 +9 — both halves are the same diff", left.OldStart, left.NewStart)
	}

	session.PR = nil
	staged, _, _ := restagedHunk(session, id)
	if left := staged.Files[0].Unstaged.Hunks[0]; left.OldStart != 9 {
		t.Errorf("staging in a working tree read -%d, want the index's renumbering", left.OldStart)
	}
}

func TestPullRequestKeysAreWordedAsViewing(t *testing.T) {
	m := newModel(t, newFakeBackend(pullRequestOf(t, twoFileDiff, "")))
	if hints := m.hints(); !strings.Contains(hints, "s mark viewed") || !strings.Contains(hints, "u unmark") {
		t.Errorf("hints = %q, want them to talk about viewing", hints)
	}
	press(t, m, "S")
	if !strings.Contains(m.status, "marks the hunk the cursor is in viewed") {
		t.Errorf("status = %q", m.status)
	}
	press(t, m, "?")
	if view := m.View(); !strings.Contains(view, "mark the hunk the cursor is in viewed") {
		t.Errorf("help does not talk about viewing:\n%s", view)
	}
}

func TestUndoTakesAViewedMarkBack(t *testing.T) {
	session := pullRequestOf(t, twoFileDiff, "")
	backend := newFakeBackend(session)
	m := newModel(t, backend)

	press(t, m, "s", "z")

	if len(backend.restored) != 1 {
		t.Fatalf("restored %v, want the list put back once", backend.restored)
	}
	if got := m.session.Files[0].State(); got != git.StateUnstaged {
		t.Errorf("alpha.go = %v after undo, want not viewed", got)
	}
}

func TestAPullRequestOpensWithItsViewedFilesFolded(t *testing.T) {
	files := parseFiles(t, twoFileDiff+growingHunkFile)
	files[0] = app.FileViewed(files[0], true)
	list := files[2].Unstaged
	files[2], _ = app.HunkViewed(files[2], list.ID(list.Hunks[0]))
	backend := newFakeBackend(withFiles(pullRequestOf(t, twoFileDiff, ""), files))
	backend.folded = []string{"beta.txt", "list.txt"}
	m := newModel(t, backend)

	if !m.collapsed["alpha.go"] {
		t.Error("a viewed file opened unfolded")
	}
	if !m.collapsed["beta.txt"] {
		t.Error("a file folded by hand opened again")
	}
	if m.collapsed["list.txt"] {
		t.Error("a folded file with hunks still to view stayed folded")
	}
}

func TestACommentStaysOnAHunkOnceItIsViewed(t *testing.T) {
	session := pullRequestOf(t, growingHunkFile, "")
	session.Files[0] = app.FileViewed(session.Files[0], true)
	comments := []store.Comment{{
		ID: "c1", File: "list.txt", Line: 10, Side: store.SideNew, Origin: store.OriginWorktree,
		Body: "why upper case", Author: store.AuthorUser,
	}}

	doc := Build(session, comments, nil, LayoutUnified)

	if text, _ := codeUnder(t, doc, "c1"); text != "NINE" {
		t.Errorf("the note sits under %q, want the line it was written on", text)
	}
}

func TestThePullRequestDescriptionOpensTheReviewAsMarkdown(t *testing.T) {
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, markdownBody))
	m := newModel(t, backend)

	if m.doc.Description == nil || m.cursor != m.doc.Description.Row {
		t.Fatalf("cursor on row %d, want the description heading", m.cursor)
	}
	if m.doc.Description.Row > m.doc.Files[0].Row {
		t.Error("the description is under the diff")
	}
	view := m.View()
	for _, want := range []string{"description", "main ← drop-key", "## Why", "The **key** was never read.", "│ 1 │ 2 │", "x := 1"} {
		if !strings.Contains(view, want) {
			t.Errorf("the description does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "template") {
		t.Errorf("an HTML comment in the description was drawn:\n%s", view)
	}
	if strings.Contains(view, "\r") {
		t.Error("the description kept its carriage returns")
	}
}

func TestTheDescriptionWrapsItsProseToThePane(t *testing.T) {
	long := strings.Repeat("word ", 60)
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, long))
	m := newModel(t, backend)

	rows := 0
	for _, r := range m.doc.Rows {
		if r.Kind == RowDescriptionText {
			rows++
		}
	}
	if rows < 3 {
		t.Errorf("a 300 column paragraph took %d rows, want it wrapped", rows)
	}
}

func TestAWrappedListItemCarriesOnUnderItsText(t *testing.T) {
	long := strings.Repeat("word ", 40)
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, "- [x] "+long+"\n> "+long))
	m := newModel(t, backend)

	var texts []string
	for _, r := range m.doc.Rows {
		if r.Kind == RowDescriptionText {
			texts = append(texts, r.Text)
		}
	}
	if len(texts) < 4 {
		t.Fatalf("got %d rows, want both lines wrapped: %q", len(texts), texts)
	}
	if !strings.HasPrefix(texts[0], "- [x] word") || !strings.HasPrefix(texts[1], "      word") {
		t.Errorf("the list item wrapped as %q, want its second row under its text", texts[:2])
	}
	last := texts[len(texts)-1]
	if !strings.HasPrefix(last, "> word") {
		t.Errorf("the quote wrapped to %q, want each row still quoted", last)
	}
	for _, text := range texts {
		if width := ansi.StringWidth(text); width > m.doc.pane-1-descriptionIndent {
			t.Errorf("%q is %d columns, wider than the pane", text, width)
		}
	}
}

func TestSpaceFoldsTheDescriptionAndItStaysFolded(t *testing.T) {
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, markdownBody))
	m := newModel(t, backend)

	press(t, m, "space")

	if !m.doc.Description.Folded || !backend.descriptionFolded {
		t.Fatal("space did not fold the description and keep it folded")
	}
	if strings.Contains(m.View(), "## Why") {
		t.Error("the folded description is still drawn")
	}
	if again := newModel(t, backend); !again.doc.Description.Folded {
		t.Error("the description opened again the next time the pull request was read")
	}

	press(t, m, "z")
	if m.doc.Description.Folded || backend.descriptionFolded {
		t.Error("undo did not open the description again")
	}
}

func TestTheDescriptionIsNotSomethingToMarkOrCommentOn(t *testing.T) {
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, markdownBody))
	m := newModel(t, backend)
	press(t, m, "down")

	press(t, m, "s")
	if len(backend.stagedFiles) != 0 || !strings.Contains(m.status, "nothing to mark viewed here") {
		t.Errorf("s on the description marked %v, status %q", backend.stagedFiles, m.status)
	}
	press(t, m, "c")
	if m.mode != modeBrowse {
		t.Errorf("c on the description opened mode %v", m.mode)
	}

	press(t, m, "j")
	if row := m.doc.Rows[m.cursor]; row.Kind != RowFile || row.File != 0 {
		t.Errorf("j from the description landed on %+v, want the first file", row)
	}
}

func TestAPullRequestWithNoDescriptionOpensOnItsFirstFile(t *testing.T) {
	backend := newFakeBackend(pullRequestOf(t, twoFileDiff, "<!-- only the template -->\r\n"))
	m := newModel(t, backend)

	if m.doc.Description != nil {
		t.Error("an empty description was drawn")
	}
	if row := m.doc.Rows[m.cursor]; row.Kind != RowFile {
		t.Errorf("cursor on %+v, want the first file", row)
	}
}
