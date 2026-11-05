package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

const casesDiff = `diff --git a/cases_test.go b/cases_test.go
index 1111111..2222222 100644
--- a/cases_test.go
+++ b/cases_test.go
@@ -1,3 +1,10 @@
 package cases
 
-old
+should reject empty
+	bodyA1
+	bodyA2
+end
+
+should reject duplicate
+	bodyB1
+end
`

const bodiesLabel = "test bodies"

func bodiesRun() store.HiddenRun {
	return store.HiddenRun{ID: "a", File: "cases_test.go", Line: 4, EndLine: 5, Label: bodiesLabel, Author: "claude"}
}

func otherBodyRun() store.HiddenRun {
	return store.HiddenRun{ID: "b", File: "cases_test.go", Line: 9, Author: "claude"}
}

func casesModel(t *testing.T, runs ...store.HiddenRun) (*fakeBackend, *Model) {
	t.Helper()
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = runs
	return backend, newModel(t, backend)
}

func hiddenRowText(t *testing.T, m *Model, n int) string {
	t.Helper()
	if n >= len(m.doc.HiddenRows) {
		t.Fatalf("drew %d hidden rows, want at least %d", len(m.doc.HiddenRows), n+1)
	}
	return ansi.Strip(m.renderer.Row(m.doc, m.doc.HiddenRows[n].Row, RowState{}))
}

func wantShown(t *testing.T, m *Model, words ...string) {
	t.Helper()
	got := ansi.Strip(body(m))
	for _, w := range words {
		if !strings.Contains(got, w) {
			t.Errorf("%q is hidden and should show:\n%s", w, got)
		}
	}
}

func wantHidden(t *testing.T, m *Model, words ...string) {
	t.Helper()
	got := ansi.Strip(body(m))
	for _, w := range words {
		if strings.Contains(got, w) {
			t.Errorf("%q shows and should be hidden:\n%s", w, got)
		}
	}
}

func TestARunCollapsesToOneRowInsideItsHunk(t *testing.T) {
	_, m := casesModel(t, bodiesRun())

	wantHidden(t, m, "bodyA1", "bodyA2")
	wantShown(t, m, "should reject empty", "end", "should reject duplicate", "bodyB1")

	text := hiddenRowText(t, m, 0)
	for _, want := range []string{"▸", "2 lines hidden"} {
		if !strings.Contains(text, want) {
			t.Errorf("row = %q, want it to mention %q", text, want)
		}
	}

	row := m.doc.HiddenRows[0].Row
	ref, ok := m.doc.HunkTargetAt(row)
	if !ok || ref.Path != "cases_test.go" {
		t.Errorf("the row is not part of its hunk: %+v, %v", ref, ok)
	}
	if got := ansi.Strip(m.renderer.Row(m.doc, row-1, RowState{})); !strings.Contains(got, "should reject empty") {
		t.Errorf("the line above the row is %q", got)
	}
	if m.doc.HiddenLines != 2 {
		t.Errorf("HiddenLines = %d, want 2", m.doc.HiddenLines)
	}
}

func TestTheHunkHeaderAndTheScreenSayHowMuchIsHidden(t *testing.T) {
	_, m := casesModel(t, bodiesRun())

	var header string
	for i, r := range m.doc.Rows {
		if r.Kind == RowHunk {
			header = ansi.Strip(m.renderer.Row(m.doc, i, RowState{}))
		}
	}
	if !strings.Contains(header, "2 of 11 lines hidden") {
		t.Errorf("hunk header = %q", header)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "2 lines hidden") || !strings.Contains(view, "A review mode") {
		t.Errorf("the screen does not say what is hidden or how to see it:\n%s", view)
	}
}

func TestARunWithNoAuthorOrLabelSaysOnlyTheCount(t *testing.T) {
	_, m := casesModel(t, store.HiddenRun{ID: "a", File: "cases_test.go", Line: 4, Author: store.AuthorUnknown})

	text := ansi.Strip(m.renderer.Row(m.doc, m.doc.HiddenRows[0].Row, RowState{Cursor: true}))
	if !strings.Contains(text, "1 line hidden") || strings.Contains(text, "unknown") || strings.Contains(text, "—") {
		t.Errorf("row = %q", text)
	}
}

func TestSpaceOpensOneRunAndLeavesTheOthersClosed(t *testing.T) {
	_, m := casesModel(t, bodiesRun(), otherBodyRun())
	wantHidden(t, m, "bodyA1", "bodyB1")

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "space")

	wantShown(t, m, "bodyA1", "bodyA2")
	wantHidden(t, m, "bodyB1")
	if text := hiddenRowText(t, m, 0); !strings.Contains(text, "▾") || strings.Contains(text, "hidden") {
		t.Errorf("an open run's row = %q", text)
	}
	if m.cursor != m.doc.HiddenRows[0].Row {
		t.Errorf("the cursor left the row it opened: %d, want %d", m.cursor, m.doc.HiddenRows[0].Row)
	}

	press(t, m, "space")
	wantHidden(t, m, "bodyA1", "bodyB1")

	press(t, m, "z")
	wantShown(t, m, "bodyA1")
	wantHidden(t, m, "bodyB1")
}

func TestACommentedLineIsNeverHidden(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "cases_test.go", Line: 3, EndLine: 6, Author: "claude"}}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 4, Side: store.SideNew,
		Body: "why here", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantShown(t, m, "bodyA1", "why here")
	wantHidden(t, m, "bodyA2", "should reject empty")
	if len(m.doc.HiddenRows) != 2 {
		t.Fatalf("drew %d rows, want one on each side of the commented line", len(m.doc.HiddenRows))
	}
	if first, second := hiddenRowText(t, m, 0), hiddenRowText(t, m, 1); !strings.Contains(first, "2 lines hidden") ||
		!strings.Contains(second, "2 lines hidden") {
		t.Errorf("rows = %q and %q", first, second)
	}
	if m.doc.HiddenRows[0].Row >= m.doc.RowOfLine(0, 4) || m.doc.HiddenRows[1].Row <= m.doc.RowOfLine(0, 4) {
		t.Errorf("the rows are not either side of the commented line: %+v", m.doc.HiddenRows)
	}

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "space")
	wantShown(t, m, "should reject empty", "bodyA2")
}

func TestEveryLineOfACommentedRangeStaysVisible(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "cases_test.go", Line: 3, EndLine: 6, Author: "claude"}}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 4, EndLine: 5, Side: store.SideNew,
		Body: "why here", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantShown(t, m, "bodyA1", "bodyA2")
	wantHidden(t, m, "should reject empty")
}

func TestAnOutdatedNoteStillKeepsItsNearestLineVisible(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{bodiesRun()}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 40, NearestLine: 4, Outdated: true,
		Side: store.SideNew, Body: "why here", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantShown(t, m, "bodyA1")
	wantHidden(t, m, "bodyA2")
}

func TestALineBeingCommentedOnIsNotHiddenByARunThatArrivesLater(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	m := newModel(t, backend)

	m.moveTo(m.doc.RowOfLine(0, 4))
	press(t, m, "c")
	if m.mode != modeComment {
		t.Fatal("c did not open the editor")
	}

	m.hiddenRuns = []store.HiddenRun{bodiesRun()}
	m.rebuild()

	wantShown(t, m, "bodyA1")
	wantHidden(t, m, "bodyA2")
}

func TestTheAgentKeySwitchesBetweenReviewModeAndTheFullDiff(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{bodiesRun()}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 9, Side: store.SideNew,
		Body: "look here", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantHidden(t, m, "bodyA1")
	wantShown(t, m, "look here")

	press(t, m, "A")
	wantShown(t, m, "bodyA1", "bodyA2")
	wantHidden(t, m, "look here", "lines hidden")
	if !strings.Contains(m.status, "full diff") {
		t.Errorf("status = %q", m.status)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "full diff") {
		t.Errorf("the header does not say the diff is complete:\n%s", view)
	}
	if !backend.othersHidden {
		t.Error("the choice was not saved")
	}

	press(t, m, "A")
	wantHidden(t, m, "bodyA1")
	wantShown(t, m, "look here")
	if !strings.Contains(m.status, "review mode") || !strings.Contains(m.status, "2 lines collapsed") {
		t.Errorf("status = %q", m.status)
	}
}

func TestTheAgentKeyWorksWhenThereAreOnlyRuns(t *testing.T) {
	_, m := casesModel(t, bodiesRun())

	press(t, m, "A")
	wantShown(t, m, "bodyA1")
	press(t, m, "A")
	wantHidden(t, m, "bodyA1")
}

func TestReviewModeOffIsRememberedWhenThereAreOnlyRuns(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{bodiesRun()}
	backend.othersHidden = true
	m := newModel(t, backend)

	wantShown(t, m, "bodyA1")
	if !m.othersHidden {
		t.Error("the saved choice was not restored")
	}
}

func TestStagingAHunkFromTheRowTakesTheHiddenLinesToo(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{bodiesRun()}
	m := newModel(t, backend, WithStageMode(app.StageModeHunk))

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "s")

	if len(backend.stagedHunks) != 1 && len(backend.stagedFiles) != 1 {
		t.Fatalf("nothing was staged: hunks %v, files %v", backend.stagedHunks, backend.stagedFiles)
	}
	if !strings.Contains(m.status, "including 2 hidden lines") {
		t.Errorf("status = %q, want it to mention the hidden lines", m.status)
	}
}

func TestStagingOneHunkLeavesTheOtherAlone(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	backend.runs = []store.HiddenRun{{ID: "a", File: "list.txt", Line: 2, EndLine: 3, Author: "claude"}}
	m := newModel(t, backend, WithStageMode(app.StageModeHunk))

	m.moveTo(m.doc.HiddenRows[0].Row)
	if got := m.doc.Rows[m.cursor].Hunk; got != 0 {
		t.Fatalf("the row is in hunk %d, want 0", got)
	}
	press(t, m, "s")

	if len(backend.stagedHunks) != 1 || len(backend.stagedFiles) != 0 {
		t.Errorf("hunks %v, files %v, want only the hunk under the cursor", backend.stagedHunks, backend.stagedFiles)
	}
}

func TestAWholeFileRunCollapsesTheFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "beta.txt", Label: "generated", Author: "claude"}}
	m := newModel(t, backend)

	wantHidden(t, m, "new")
	wantShown(t, m, "func One")
	if text := hiddenRowText(t, m, 0); !strings.Contains(text, "3 lines hidden") {
		t.Errorf("row = %q", text)
	}

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "space")
	wantShown(t, m, "new")
	press(t, m, "space")
	wantHidden(t, m, "new")
}

func TestStagingFromAWholeFileRowStagesTheFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "beta.txt", Author: "claude"}}
	m := newModel(t, backend, WithStageMode(app.StageModeHunk))

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "s")

	if len(backend.stagedFiles) != 1 || backend.stagedFiles[0] != "beta.txt" {
		t.Errorf("staged files = %v, want beta.txt", backend.stagedFiles)
	}
	if !strings.Contains(m.status, "including 3 hidden lines") {
		t.Errorf("status = %q", m.status)
	}
}

func TestAWholeFileRunGivesWayToALineNote(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "cases_test.go", Author: "claude"}}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 9, Side: store.SideNew,
		Body: "look here", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantShown(t, m, "bodyB1", "look here")
	wantHidden(t, m, "bodyA1", "should reject duplicate")
	if len(m.doc.HiddenRows) != 2 {
		t.Errorf("drew %d rows, want one on each side of the note", len(m.doc.HiddenRows))
	}
}

func TestAWholeFileRunWithANoteOnTheFileStillCollapses(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "cases_test.go", Author: "claude"}}
	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Body: "about all of it", Author: store.AuthorAgent}}
	m := newModel(t, backend)

	wantShown(t, m, "about all of it")
	wantHidden(t, m, "bodyA1")
}

func TestARunInSplitLayoutHidesBothSides(t *testing.T) {
	backend := newFakeBackend(newSession(t, casesDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "cases_test.go", Line: 3, EndLine: 4, Author: "claude"}}
	m := newModel(t, backend, WithLayout(LayoutSplit))

	wantHidden(t, m, "old", "should reject empty", "bodyA1")
	wantShown(t, m, "bodyA2")
	if text := hiddenRowText(t, m, 0); !strings.Contains(text, "3 lines hidden") {
		t.Errorf("row = %q, want the removed line counted too", text)
	}
}

func TestARemovedLineGoesWithTheNewLineAfterIt(t *testing.T) {
	_, m := casesModel(t, store.HiddenRun{ID: "a", File: "cases_test.go", Line: 3, Author: "claude"})
	wantHidden(t, m, "old", "should reject empty")
	if text := hiddenRowText(t, m, 0); !strings.Contains(text, "2 lines hidden") {
		t.Errorf("row = %q", text)
	}
}

func TestARemovedLineWithNothingAfterItStaysVisible(t *testing.T) {
	const diff = `diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1,3 +1,2 @@
 keep one
 keep two
-gone for good
`
	backend := newFakeBackend(newSession(t, diff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "a.txt", Line: 1, EndLine: 2, Author: "claude"}}
	m := newModel(t, backend)

	wantHidden(t, m, "keep one", "keep two")
	wantShown(t, m, "gone for good")
}

func TestAMergeConflictIsNeverHidden(t *testing.T) {
	entries := parseFiles(t, casesDiff)
	entries[0].Conflicted = true
	for name, run := range map[string]store.HiddenRun{
		"lines": bodiesRun(),
		"file":  {ID: "a", File: "cases_test.go", Author: "claude"},
	} {
		backend := newFakeBackend(sessionOf(entries))
		backend.runs = []store.HiddenRun{run}
		m := newModel(t, backend)
		if len(m.doc.HiddenRows) != 0 {
			t.Errorf("%s: a conflict was hidden", name)
		}
		wantShown(t, m, "bodyA1", "bodyA2")
	}
}

func TestAnOutdatedRunHidesNothing(t *testing.T) {
	run := bodiesRun()
	run.Outdated = true
	_, m := casesModel(t, run)

	wantShown(t, m, "bodyA1", "bodyA2")
	if view := ansi.Strip(m.View()); strings.Contains(view, "review mode") || strings.Contains(view, "lines hidden") {
		t.Errorf("the screen talks about hidden lines with nothing hidden:\n%s", view)
	}
}

func TestARunThatCoversPartOfAPairLeavesItVisible(t *testing.T) {
	const diff = `diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,3 @@
 keep
-before
+after one
+after two
`
	backend := newFakeBackend(newSession(t, diff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "a.txt", Line: 3, Author: "claude"}}
	m := newModel(t, backend, WithLayout(LayoutSplit))

	wantShown(t, m, "before", "after one")
	wantHidden(t, m, "after two")
}

func TestTheCursorStopsOnARow(t *testing.T) {
	_, m := casesModel(t, bodiesRun())
	row := m.doc.HiddenRows[0].Row

	m.moveTo(row - 1)
	press(t, m, "down")
	if m.cursor != row {
		t.Errorf("down went to %d, want the row at %d", m.cursor, row)
	}
	press(t, m, "down")
	if m.cursor != row+1 {
		t.Errorf("down from the row went to %d, want %d", m.cursor, row+1)
	}

	if !m.doc.endsLeap(row) {
		t.Error("a long move should stop on the row rather than step over hidden lines")
	}
}

func TestTheAgentKeyKeepsTheCursorOnTheSameCode(t *testing.T) {
	_, m := casesModel(t, bodiesRun())
	row := m.doc.HiddenRows[0].Row
	m.moveTo(row)

	press(t, m, "A")
	if want := m.doc.RowOfLine(0, 4); m.cursor != want {
		t.Errorf("in the full diff the cursor is on %d, want the first line the row hid at %d", m.cursor, want)
	}

	press(t, m, "A")
	if m.cursor != row {
		t.Errorf("back in review mode the cursor is on %d, want the row at %d", m.cursor, row)
	}
}

func TestACursorOnARowStaysOnItsCodeWhenACommentSplitsIt(t *testing.T) {
	backend, m := casesModel(t, store.HiddenRun{ID: "a", File: "cases_test.go", Line: 4, EndLine: 6, Author: "claude"})
	m.moveTo(m.doc.HiddenRows[0].Row)

	backend.comments = []store.Comment{{ID: "c1", File: "cases_test.go", Line: 4, Side: store.SideNew,
		Body: "why here", Author: store.AuthorAgent}}
	press(t, m, "r")

	if want := m.doc.RowOfLine(0, 4); m.cursor != want {
		t.Errorf("cursor = %d, want the commented line at %d, which is where the row started", m.cursor, want)
	}
}

func TestARowCannotBeCommentedOnDirectly(t *testing.T) {
	_, m := casesModel(t, bodiesRun())

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "c")

	if m.mode == modeComment {
		t.Error("c opened the editor with no line to hang the note from")
	}
	if !strings.Contains(m.status, "space opens") {
		t.Errorf("status = %q", m.status)
	}
}

func TestACursorOnALineThatGetsHiddenMovesToTheRow(t *testing.T) {
	backend, m := casesModel(t)
	m.moveTo(m.doc.RowOfLine(0, 5))

	backend.runs = []store.HiddenRun{bodiesRun()}
	press(t, m, "r")

	if len(m.doc.HiddenRows) != 1 {
		t.Fatalf("the reload drew %d rows, want 1", len(m.doc.HiddenRows))
	}
	if m.cursor != m.doc.HiddenRows[0].Row {
		t.Errorf("cursor = %d, want the row at %d", m.cursor, m.doc.HiddenRows[0].Row)
	}
}

func TestATableSplitByARunStillDrawsItsEdges(t *testing.T) {
	backend := newFakeBackend(newSession(t, tableRunDiff))
	backend.runs = []store.HiddenRun{{ID: "a", File: "notes.md", Line: 3, EndLine: 4, Author: "claude"}}
	m := newModel(t, backend)

	if len(m.doc.HiddenRows) != 1 {
		t.Fatalf("drew %d rows, want 1", len(m.doc.HiddenRows))
	}
	wantHidden(t, m, "| a |", "|---|")
	wantShown(t, m, "after the table", "intro line")
	for i := range m.doc.Rows {
		_ = m.renderer.Row(m.doc, i, RowState{})
	}
}

func TestAPollPicksUpANewRunAndARewriteUnhidesIt(t *testing.T) {
	repo, m := followModel(t)
	wantShown(t, m, "println(1)")

	addHiddenRun(t, repo, store.HiddenRun{File: "main.go", Line: 3, Label: "the call", Author: "claude"})
	poll(t, m)

	wantHidden(t, m, "println(1)")
	wantShown(t, m, "▸")

	repo.Write("main.go", "package main\n\nfunc main() { println(2) }\n")
	poll(t, m)

	wantShown(t, m, "println(2)")
	wantHidden(t, m, "▸")
}

func TestAPollFollowsARunWhenLinesAreAddedAbove(t *testing.T) {
	repo, m := followModel(t)
	addHiddenRun(t, repo, store.HiddenRun{File: "main.go", Line: 3, Author: "claude"})
	poll(t, m)

	repo.Write("main.go", "// top\npackage main\n\nfunc main() { println(1) }\n")
	poll(t, m)

	wantShown(t, m, "// top")
	wantHidden(t, m, "println(1)")
}

func contextDoc(t *testing.T, open map[string]bool, runs ...store.HiddenRun) Document {
	t.Helper()
	return Build(newSession(t, contextDiff), nil, nil, LayoutUnified,
		WithExpansion(expansionOf(nil)), WithHiddenRuns(runs, open))
}

func wideRun(id string, line, end int) store.HiddenRun {
	return store.HiddenRun{ID: id, File: "wide.go", Line: line, EndLine: end, Label: "test code", Author: "claude"}
}

func rowKinds(doc Document) []RowKind {
	kinds := make([]RowKind, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

func wantKinds(t *testing.T, doc Document, want ...RowKind) {
	t.Helper()
	if got := rowKinds(doc); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func drawnRow(doc Document, row int, st RowState) string {
	return strings.TrimSpace(ansi.Strip(plainRenderer(70).Row(doc, row, st)))
}

func TestOneRowStandsBetweenVisibleLinesAcrossHunksAndGaps(t *testing.T) {
	doc := contextDoc(t, nil, wideRun("a", 2, 8), wideRun("b", 47, 49), wideRun("c", 51, 53))

	wantKinds(t, doc, RowFile, RowHidden, RowLine, RowLine, RowHidden, RowBlank)
	if got, want := drawnRow(doc, 1, RowState{}), "▸ 11 lines hidden"; got != want {
		t.Errorf("first row = %q, want %q", got, want)
	}
	if got, want := drawnRow(doc, 4, RowState{}), "▸ 3 lines hidden"; got != want {
		t.Errorf("last row = %q, want %q", got, want)
	}
	if doc.HiddenLines != 14 {
		t.Errorf("HiddenLines = %d, want 14", doc.HiddenLines)
	}
}

func TestARowSaysOnlyTheCountUntilTheCursorIsOnIt(t *testing.T) {
	doc := contextDoc(t, nil, wideRun("a", 2, 8), wideRun("b", 47, 49))

	if got, want := drawnRow(doc, 1, RowState{}), "▸ 11 lines hidden"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
	if got := drawnRow(doc, 1, RowState{Cursor: true}); !strings.Contains(got, "by claude — test code") {
		t.Errorf("the cursor row = %q, want who hid the lines and why", got)
	}
}

func TestARowOverRunsThatDisagreeNamesNeither(t *testing.T) {
	other := wideRun("b", 47, 49)
	other.Label = "fixtures"
	doc := contextDoc(t, nil, wideRun("a", 2, 8), other)

	if got, want := drawnRow(doc, 1, RowState{Cursor: true}), "▸ 11 lines hidden"; !strings.Contains(got, want) ||
		strings.Contains(got, "claude") || strings.Contains(got, "fixtures") {
		t.Errorf("the cursor row = %q, want only %q", got, want)
	}
}

func TestAHiddenHunkLeavesTheGapRowsOfVisibleCodeAlone(t *testing.T) {
	first := contextDoc(t, nil, wideRun("a", 2, 8))
	wantKinds(t, first, RowFile, RowHidden, RowHunk, RowExpand,
		RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowExpand, RowBlank)

	second := contextDoc(t, nil, wideRun("b", 47, 53))
	wantKinds(t, second, RowFile, RowHunk, RowExpand,
		RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowHidden, RowBlank)
}

func TestAnOpenRunDrawsItsHunkAsUsual(t *testing.T) {
	doc := contextDoc(t, map[string]bool{"a": true}, wideRun("a", 2, 8))

	wantKinds(t, doc, RowFile, RowHunk, RowExpand, RowHidden,
		RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowExpand,
		RowHunk, RowExpand,
		RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowExpand, RowBlank)
}

func TestAHeaderThatWasNotDrawnDoesNotTakeTheNameOfTheNextOne(t *testing.T) {
	const diff = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,1 +1,1 @@ func One
-a
+b
@@ -9,1 +9,1 @@ func One
-c
+d
`
	runs := []store.HiddenRun{{ID: "a", File: "a.go", Line: 1, Author: "claude"}}
	doc := Build(newSession(t, diff), nil, nil, LayoutUnified, WithHiddenRuns(runs, nil))

	row := doc.RowOfHunk(1)
	if doc.Rows[row].Kind != RowHunk {
		t.Fatalf("the second hunk has no header: %v", rowKinds(doc))
	}
	if got := drawnRow(doc, row, RowState{}); !strings.Contains(got, "func One") {
		t.Errorf("header = %q, want the name no earlier header gave", got)
	}
}

func TestAHunkWithNoHeaderIsFoundAtItsRow(t *testing.T) {
	doc := contextDoc(t, nil, wideRun("a", 2, 8), wideRun("b", 47, 53))

	if doc.RowOfHunk(0) != 1 || doc.RowOfHunk(1) != 1 {
		t.Errorf("RowOfHunk = %d and %d, want the shared row at 1", doc.RowOfHunk(0), doc.RowOfHunk(1))
	}
}

func TestAWholeFileRowIsNotTakenForAHunk(t *testing.T) {
	runs := []store.HiddenRun{{ID: "a", File: "wide.go", Author: "claude"}}
	doc := contextDoc(t, nil, runs...)

	if got := doc.RowOfHunk(-1); got != -1 {
		t.Errorf("RowOfHunk(-1) = %d, want -1", got)
	}
}

func TestSpaceOpensEverythingOnARowAndUndoClosesIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	backend.runs = []store.HiddenRun{
		{ID: "a", File: "list.txt", Line: 3, EndLine: 5, Author: "claude"},
		{ID: "b", File: "list.txt", Line: 8, EndLine: 9, Author: "claude"},
	}
	m := newModel(t, backend)

	if got := m.doc.HiddenRows[0].Row; got != m.doc.HiddenRows[1].Row {
		t.Fatalf("the runs drew rows %d and %d, want one row", got, m.doc.HiddenRows[1].Row)
	}
	wantShown(t, m, "5 lines hidden")
	wantHidden(t, m, "three", "eight")

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "space")
	wantShown(t, m, "three", "eight", "nine")
	if !strings.Contains(m.status, "opening 5 lines") {
		t.Errorf("status = %q", m.status)
	}
	if m.cursor != m.doc.HiddenRows[0].Row {
		t.Errorf("cursor = %d, want the first open row at %d", m.cursor, m.doc.HiddenRows[0].Row)
	}

	press(t, m, "z")
	wantShown(t, m, "5 lines hidden")
	wantHidden(t, m, "three", "eight")
}

func TestClosingARunJoinsItToTheRowBesideIt(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	backend.runs = []store.HiddenRun{
		{ID: "a", File: "list.txt", Line: 3, EndLine: 5, Author: "claude"},
		{ID: "b", File: "list.txt", Line: 8, EndLine: 9, Author: "claude"},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "space")
	wantShown(t, m, "three", "eight")

	press(t, m, "space")
	wantHidden(t, m, "three")
	wantShown(t, m, "eight")
	if !strings.Contains(m.status, "hiding 3 lines again") {
		t.Errorf("status = %q", m.status)
	}

	m.moveTo(m.doc.HiddenRows[1].Row)
	press(t, m, "space")
	wantShown(t, m, "5 lines hidden")
	if m.cursor != m.doc.HiddenRows[1].Row {
		t.Errorf("cursor = %d, want the joined row at %d", m.cursor, m.doc.HiddenRows[1].Row)
	}
}

func TestJStopsOnARowThatTookTheHeadersPlace(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	backend.runs = []store.HiddenRun{{ID: "b", File: "list.txt", Line: 8, EndLine: 9, Author: "claude"}}
	m := newModel(t, backend)

	row := m.doc.HiddenRows[0].Row
	m.moveTo(m.doc.RowOfHunk(0))
	press(t, m, "j")
	if m.cursor != row {
		t.Errorf("j went to %d, want the row at %d", m.cursor, row)
	}
}

func TestJStepsOverARowInTheMiddleOfAHunk(t *testing.T) {
	_, m := casesModel(t, bodiesRun())

	if m.doc.IsMark(m.doc.HiddenRows[0].Row) {
		t.Error("a row with its hunk's header above it should not be a stop for j")
	}
}

func TestStagingFromARowOverSeveralHunksTakesTheFirst(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeHunkFile))
	backend.runs = []store.HiddenRun{
		{ID: "a", File: "list.txt", Line: 1, EndLine: 5, Author: "claude"},
		{ID: "b", File: "list.txt", Line: 8, EndLine: 11, Author: "claude"},
	}
	m := newModel(t, backend, WithStageMode(app.StageModeHunk))
	first := m.doc.Hunks[0].ID

	m.moveTo(m.doc.HiddenRows[0].Row)
	press(t, m, "s")

	if len(backend.stagedHunks) != 1 || backend.stagedHunks[0] != first {
		t.Fatalf("staged %v, want only the first hunk %v", backend.stagedHunks, first)
	}
	if !strings.Contains(m.status, "including 6 hidden lines") {
		t.Errorf("status = %q", m.status)
	}
	if m.doc.Rows[m.cursor].Kind != RowHidden {
		t.Errorf("the cursor left the rest of the row: it is on a %v", m.doc.Rows[m.cursor].Kind)
	}
}

func addHiddenRun(t *testing.T, repo *gittest.Repo, run store.HiddenRun) {
	t.Helper()
	ctx := context.Background()
	a, err := app.Open(ctx, repo.Dir, app.WithAIRegistry(ai.NewRegistry()), app.WithForgeRegistry(forge.NewRegistry()))
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	if _, err := a.AddHiddenRun(ctx, s, run); err != nil {
		t.Fatalf("AddHiddenRun: %v", err)
	}
}
