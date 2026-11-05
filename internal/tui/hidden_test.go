package tui

import (
	"context"
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
	for _, want := range []string{"▸", "2 lines hidden", "by claude", bodiesLabel} {
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

	text := hiddenRowText(t, m, 0)
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
	text := hiddenRowText(t, m, 0)
	if !strings.Contains(text, "3 lines hidden") || !strings.Contains(text, "generated") {
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
	wantShown(t, m, "the call")

	repo.Write("main.go", "package main\n\nfunc main() { println(2) }\n")
	poll(t, m)

	wantShown(t, m, "println(2)")
	wantHidden(t, m, "the call")
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
