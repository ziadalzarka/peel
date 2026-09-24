package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
)

// conflictDiff is what peel reads for an unmerged path: the merge git left on
// disk, measured against the last commit, markers and all.
const conflictDiff = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,4 +1,8 @@
 a
 b
+<<<<<<< HEAD
 OURS
+=======
+THEIRS
+>>>>>>> side
 d
`

func conflictSession(t *testing.T) *app.Session {
	t.Helper()
	entries := parseFiles(t, conflictDiff)
	entries[0].Conflicted = true
	return sessionOf(entries)
}

func TestConflictedFileSaysWhatItIsAboveTheDiff(t *testing.T) {
	doc := Build(conflictSession(t), nil, nil, LayoutUnified)

	var notes []string
	for _, row := range doc.Rows {
		if row.Kind == RowNote {
			notes = append(notes, row.Text)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "merge conflict") {
		t.Fatalf("notes = %v, want one naming the merge conflict", notes)
	}

	// It has to come before the code it explains, or it explains nothing.
	noteAt, lineAt := -1, -1
	for i, row := range doc.Rows {
		if row.Kind == RowNote && noteAt < 0 {
			noteAt = i
		}
		if row.Kind == RowLine && lineAt < 0 {
			lineAt = i
		}
	}
	if noteAt < 0 || lineAt < 0 || noteAt > lineAt {
		t.Errorf("note at row %d, first diff line at row %d — want the note first", noteAt, lineAt)
	}
}

func TestConflictedFileHeaderSaysConflicted(t *testing.T) {
	session := conflictSession(t)
	doc := Build(session, nil, nil, LayoutUnified)
	r := plainRenderer(80)

	var header string
	for i, row := range doc.Rows {
		if row.Kind == RowFile {
			header = r.Row(doc, i, RowState{})
			break
		}
	}
	if !strings.Contains(header, "conflicted") {
		t.Errorf("file header = %q, want it to say conflicted", header)
	}
}

func TestConflictedFileWithNothingToDiffStillSaysWhy(t *testing.T) {
	// Ours kept, theirs deleted: no diff at all, and the note is the only thing
	// telling the reviewer the file is waiting on a decision.
	session := sessionOf([]git.FileEntry{{Path: "f.txt", Conflicted: true}})
	doc := Build(session, nil, nil, LayoutUnified)

	var notes []string
	for _, row := range doc.Rows {
		if row.Kind == RowNote {
			notes = append(notes, row.Text)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "merge conflict") {
		t.Fatalf("notes = %v, want one naming the merge conflict", notes)
	}
}

func TestStagingAConflictClearsIt(t *testing.T) {
	// The screen is drawn ahead of the write, and a staged conflict is a
	// resolved one — so the guess must not go on calling it conflicted.
	session := conflictSession(t)
	out := restaged(session, true, func(path string) bool { return path == "f.txt" })

	if out.Files[0].Conflicted {
		t.Error("file still reads as conflicted after being staged")
	}
	if out.Files[0].State() != git.StateStaged {
		t.Errorf("State() = %v, want staged", out.Files[0].State())
	}
}

func TestStagingWorksWhileAnotherPathIsConflicted(t *testing.T) {
	// `git write-tree` refuses an index with any unmerged path in it, and that
	// is the call staging makes to record what a press can be taken back to.
	// Mid-merge it fails for every file, conflicted or not, so the press has to
	// go through without an undo rather than not go through at all.
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.indexTreeErr = errors.New("not a fully merged index")
	m := newModel(t, backend)
	path := m.doc.Files[0].Entry.Path

	press(t, m, "s")
	if got := backend.stagedFiles; len(got) != 1 || got[0] != path {
		t.Fatalf("staged %v, want %s staged even with no tree to record", got, path)
	}
	if m.err != nil {
		t.Errorf("staging reported %v, want it to go through quietly", m.err)
	}

	press(t, m, "z")
	if m.status != "nothing to undo" {
		t.Errorf("status = %q, want the press to be one there is no undo for", m.status)
	}
}

// paneDiff is four files over three directories, for the order the pane puts
// them in.
const paneDiff = `diff --git a/internal/tui/model.go b/internal/tui/model.go
--- a/internal/tui/model.go
+++ b/internal/tui/model.go
@@ -1 +1,2 @@
 a
+b
diff --git a/internal/tui/view.go b/internal/tui/view.go
--- a/internal/tui/view.go
+++ b/internal/tui/view.go
@@ -1 +1,2 @@
 a
+b
diff --git a/internal/git/parse.go b/internal/git/parse.go
--- a/internal/git/parse.go
+++ b/internal/git/parse.go
@@ -1 +1,2 @@
 a
+b
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1 +1,2 @@
 a
+b
`

// paneOf builds the file pane for paneDiff with the named paths conflicted.
func paneOf(t *testing.T, conflicted ...string) []paneRow {
	t.Helper()
	entries := parseFiles(t, paneDiff)
	for i := range entries {
		for _, path := range conflicted {
			if entries[i].Path == path {
				entries[i].Conflicted = true
			}
		}
	}
	return fileTree(Build(sessionOf(entries), nil, nil, LayoutUnified).Files)
}

// paneFiles lists the pane's file rows in the order it draws them.
func paneFiles(rows []paneRow) []string {
	var out []string
	for _, row := range rows {
		if row.File >= 0 {
			out = append(out, row.Path)
		}
	}
	return out
}

func TestPaneKeepsOneTreeWithNoConflictInIt(t *testing.T) {
	rows := paneOf(t)
	for _, row := range rows {
		if row.Heading {
			t.Fatalf("a tree with nothing conflicted has a %q heading over it", row.Name)
		}
	}
	want := []string{"internal/tui/model.go", "internal/tui/view.go", "internal/git/parse.go", "README.md"}
	if got := paneFiles(rows); !equalPaths(got, want) {
		t.Errorf("files = %v, want the document's order %v", got, want)
	}
}

func TestDiffPutsConflictsFirstInThePanesOrder(t *testing.T) {
	entries := parseFiles(t, paneDiff)
	entries[1].Conflicted = true
	entries[3].Conflicted = true
	doc := Build(sessionOf(entries), nil, nil, LayoutUnified)

	var diff []string
	for _, row := range doc.Rows {
		if row.Kind == RowFile {
			diff = append(diff, doc.Files[row.File].Entry.Path)
		}
	}
	want := []string{"internal/tui/view.go", "README.md", "internal/tui/model.go", "internal/git/parse.go"}
	if !equalPaths(diff, want) {
		t.Errorf("diff files = %v, want %v", diff, want)
	}
	if pane := paneFiles(fileTree(doc.Files)); !equalPaths(pane, diff) {
		t.Errorf("pane files = %v, want the diff's order %v", pane, diff)
	}
}

func TestPaneLiftsConflictsToTheTopUnderTheirOwnHeading(t *testing.T) {
	rows := paneOf(t, "internal/tui/view.go", "README.md")

	if len(rows) == 0 || !rows[0].Heading || rows[0].Name != conflictHeading {
		t.Fatalf("first row = %+v, want the %q heading", rows[0], conflictHeading)
	}

	// The conflicts come first, each still under the directory it lives in, and
	// everything else follows under the second heading.
	want := []string{"internal/tui/view.go", "README.md", "internal/tui/model.go", "internal/git/parse.go"}
	if got := paneFiles(rows); !equalPaths(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}

	var headings []string
	var restAt int
	for i, row := range rows {
		if row.Heading {
			headings = append(headings, row.Name)
			if row.Name == restHeading {
				restAt = i
			}
		}
	}
	if !equalPaths(headings, []string{conflictHeading, restHeading}) {
		t.Errorf("headings = %v, want one over each group", headings)
	}
	for _, row := range rows[:restAt] {
		if row.File >= 0 && !strings.HasSuffix(row.Path, "view.go") && !strings.HasSuffix(row.Path, "README.md") {
			t.Errorf("%s is above the %q heading", row.Path, restHeading)
		}
	}
}

func TestPaneHeadsAReviewThatIsAllConflictOnce(t *testing.T) {
	rows := paneOf(t, "internal/tui/model.go", "internal/tui/view.go",
		"internal/git/parse.go", "README.md")

	var headings []string
	for _, row := range rows {
		if row.Heading {
			headings = append(headings, row.Name)
		}
	}
	if !equalPaths(headings, []string{conflictHeading}) {
		t.Errorf("headings = %v, want just the one — there is no rest to head", headings)
	}
}

func TestPaneHeadingAnswersToNoFile(t *testing.T) {
	// The pane scrolls to the row a file sits on, so a heading must not be
	// mistaken for one.
	entries := parseFiles(t, paneDiff)
	entries[1].Conflicted = true
	m := newModel(t, newFakeBackend(sessionOf(entries)))

	for i, row := range m.fileRows {
		if !row.Heading {
			continue
		}
		if row.File >= 0 || row.Path != "" {
			t.Errorf("heading row %d = %+v, want it to name nothing in the tree", i, row)
		}
	}
	for file := range m.doc.Files {
		at := m.paneRowOf(file)
		if at < 0 || m.fileRows[at].Heading {
			t.Errorf("file %d lands on pane row %d, want a row of its own", file, at)
		}
	}
}

func TestPaneHeadingDrawsAsABreak(t *testing.T) {
	entries := parseFiles(t, paneDiff)
	entries[1].Conflicted = true
	m := newModel(t, newFakeBackend(sessionOf(entries)))
	width := m.filePaneWidth()

	line := m.paneLine(m.fileRows[0], "", width)
	if !strings.Contains(line, conflictHeading) {
		t.Errorf("heading row = %q, want it to say %q", line, conflictHeading)
	}
	if !strings.Contains(line, "─") {
		t.Errorf("heading row = %q, want a rule out to the edge", line)
	}
	for _, row := range m.fileRows {
		if got := ansi.StringWidth(m.paneLine(row, "", width)); got != width {
			t.Errorf("pane row %q is %d wide, want %d", row.Name, got, width)
		}
	}
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
