package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

func TestBuildFlattensFilesHunksAndLines(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	if len(doc.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(doc.Files))
	}
	if len(doc.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(doc.Hunks))
	}
	if got := doc.Files[0].Entry.Path; got != "alpha.go" {
		t.Errorf("first file = %q, want alpha.go", got)
	}
	if got := doc.Hunks[0].ID.String(); got != "alpha.go:@-1,4+1,5" {
		t.Errorf("first hunk id = %q", got)
	}

	// The first file's rows are the header, the hunk header, then one row per
	// diff line, then a blank separator.
	want := []RowKind{RowFile, RowHunk, RowLine, RowLine, RowLine, RowLine, RowLine, RowLine, RowBlank}
	var got []RowKind
	for _, r := range doc.Rows {
		if r.File != 0 {
			break
		}
		got = append(got, r.Kind)
	}
	if len(got) != len(want) {
		t.Fatalf("first file rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestBuildPutsAStepsNoteAboveItsFiles(t *testing.T) {
	steps := []store.Step{{
		Number: 1,
		Title:  "The fixture, first",
		Files:  []string{"beta.txt"},
		Body:   "It is read by the test the code change is for.",
	}}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified,
		WithGroups(Groups{Steps: steps, Width: 60}))

	// The note, its explanation and the blank that closes it come before the
	// file header, and the file's own rows are untouched.
	want := []RowKind{RowStep, RowStepText, RowStepText, RowFile, RowHunk, RowLine, RowLine, RowLine, RowBlank}
	var got []RowKind
	for _, r := range doc.Rows[:len(want)] {
		got = append(got, r.Kind)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
	if len(doc.Steps) != 2 {
		t.Fatalf("groups = %d, want the step plus the leftover", len(doc.Steps))
	}
	if got := doc.Steps[0]; got.Row != 0 || len(got.Files) != 1 || doc.Files[got.Files[0]].Entry.Path != "beta.txt" {
		t.Errorf("first group = %+v, want beta.txt at row 0", got)
	}
	if got := doc.Rows[1].Text; !strings.Contains(got, "read by the test") {
		t.Errorf("explanation row = %q", got)
	}
}

func TestBuildShowsAFileNamedTwiceOnce(t *testing.T) {
	steps := []store.Step{
		{Number: 1, Title: "Both", Files: []string{"beta.txt", "alpha.go"}, Body: "One reason."},
		{Number: 2, Title: "Alpha again", Files: []string{"alpha.go"}, Body: "Another reason."},
	}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified,
		WithGroups(Groups{Steps: steps, Width: 60}))

	if len(doc.Files) != 2 {
		t.Fatalf("files = %d, want the changeset's 2", len(doc.Files))
	}
	if got := []string{doc.Files[0].Entry.Path, doc.Files[1].Entry.Path}; got[0] != "beta.txt" || got[1] != "alpha.go" {
		t.Errorf("files are in %v, want the narrative's order", got)
	}
	if got := doc.Steps[1].Files; len(got) != 0 {
		t.Errorf("the second group covers %v, want nothing left for it to show", got)
	}
	// The second group keeps its heading and explanation: it is a note the
	// narrative wrote, and only the duplicated file is dropped.
	if got := doc.Steps[1].Title; got != "Alpha again" {
		t.Errorf("second group = %q, want it kept", got)
	}
}

func TestBuildOrdersStagedSideBeforeWorkingTree(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	// Give alpha.go changes on both sides, as a partially staged file has.
	staged := *entries[0].Unstaged
	entries[0].Staged = &staged

	doc := Build(&app.Session{Files: entries[:1], Stageable: true}, nil, nil, LayoutUnified)

	if len(doc.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(doc.Hunks))
	}
	if !doc.Hunks[0].Staged {
		t.Error("first hunk should be the staged side")
	}
	if doc.Hunks[1].Staged {
		t.Error("second hunk should be the working-tree side")
	}
}

func TestBuildCollapsedFileHidesItsBody(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, map[string]bool{"alpha.go": true}, LayoutUnified)

	if !doc.Files[0].Collapsed {
		t.Error("alpha.go should be marked collapsed")
	}
	if len(doc.Files[0].Hunks) != 0 {
		t.Errorf("collapsed file has %d hunks, want 0", len(doc.Files[0].Hunks))
	}
	if len(doc.Hunks) != 1 {
		t.Errorf("document has %d hunks, want only beta.txt's", len(doc.Hunks))
	}
	for _, r := range doc.Rows {
		if r.File == 0 && r.Kind == RowLine {
			t.Fatal("collapsed file still produced line rows")
		}
	}
}

func TestBuildBinaryFileGetsANoteInsteadOfHunks(t *testing.T) {
	const binary = `diff --git a/logo.png b/logo.png
index 1111111..2222222 100644
Binary files a/logo.png and b/logo.png differ
`
	doc := Build(newSession(t, binary), nil, nil, LayoutUnified)

	if len(doc.Hunks) != 0 {
		t.Fatalf("hunks = %d, want 0", len(doc.Hunks))
	}
	if !hasNote(doc, "binary") {
		t.Errorf("rows = %v, want a note mentioning binary", kinds(doc))
	}
}

func TestBuildNilSessionIsEmpty(t *testing.T) {
	doc := Build(nil, nil, nil, LayoutUnified)
	if doc.Len() != 0 {
		t.Errorf("rows = %d, want 0", doc.Len())
	}
	if doc.FirstStop() != 0 || doc.LastStop() != 0 {
		t.Error("an empty document should report stop 0")
	}
	if doc.TargetAt(0).Kind != TargetNone {
		t.Error("an empty document should have nothing to target")
	}
}

func TestPairLinesUnifiedEmitsEveryLineOnce(t *testing.T) {
	lines := hunkLines()
	pairs := pairLines(lines, LayoutUnified)

	if len(pairs) != len(lines) {
		t.Fatalf("pairs = %d, want %d", len(pairs), len(lines))
	}
	for i, p := range pairs {
		if p.left != i || p.right != -1 {
			t.Fatalf("pair %d = %+v, want {left:%d right:-1}", i, p, i)
		}
	}
}

func TestPairLinesSplitAlignsReplacementsAndPadsTheShorterRun(t *testing.T) {
	// context, two removals, one addition, context.
	lines := []git.Line{
		{Kind: git.LineContext, Text: "a", OldLine: 1, NewLine: 1},
		{Kind: git.LineRemoved, Text: "b", OldLine: 2},
		{Kind: git.LineRemoved, Text: "c", OldLine: 3},
		{Kind: git.LineAdded, Text: "B", NewLine: 2},
		{Kind: git.LineContext, Text: "d", OldLine: 4, NewLine: 3},
	}

	got := pairLines(lines, LayoutSplit)
	want := []linePair{{0, 0}, {1, 3}, {2, -1}, {4, 4}}

	if len(got) != len(want) {
		t.Fatalf("pairs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pair %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPairLinesSplitPutsNoNewlineOnTheSideItBelongsTo(t *testing.T) {
	lines := []git.Line{
		{Kind: git.LineRemoved, Text: "old", OldLine: 1},
		{Kind: git.LineNoNewline, Text: " No newline at end of file"},
		{Kind: git.LineAdded, Text: "new", NewLine: 1},
		{Kind: git.LineNoNewline, Text: " No newline at end of file"},
	}

	got := pairLines(lines, LayoutSplit)

	// The marker after a removal is old-side only; after an addition, new-side
	// only. Runs are collected before the marker interrupts them, so the
	// removal and the addition do not pair up.
	want := []linePair{{0, -1}, {1, -1}, {-1, 2}, {-1, 3}}
	if len(got) != len(want) {
		t.Fatalf("pairs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pair %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBuildAnchorsCommentsToTheirLines(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew, Body: "on the new line", Author: store.AuthorUser},
		{ID: "c2", File: "alpha.go", Body: "about the whole file", Author: store.AuthorAgent},
		{ID: "c3", File: "beta.txt", Line: 2, Side: store.SideOld, Body: "about the deletion", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	fileRow := doc.RowOfFile(0)
	if got, ok := doc.CommentAt(fileRow + 1); !ok || got.ID != "c2" {
		t.Errorf("row after the file header = %v (ok=%v), want c2", got.ID, ok)
	}

	c1 := doc.RowOfComment("c1")
	if c1 < 0 {
		t.Fatal("c1 was not placed")
	}
	prev := doc.Rows[c1-1]
	if prev.Kind != RowLine {
		t.Fatalf("c1 follows a %v, want a line", prev.Kind)
	}
	if line := doc.Hunks[prev.Hunk].Hunk.Lines[prev.Left]; line.NewLine != 4 {
		t.Errorf("c1 anchored to new line %d, want 4", line.NewLine)
	}

	c3 := doc.RowOfComment("c3")
	if c3 < 0 {
		t.Fatal("c3 was not placed")
	}
	if line := doc.Hunks[doc.Rows[c3-1].Hunk].Hunk.Lines[doc.Rows[c3-1].Left]; line.OldLine != 2 || line.Kind != git.LineRemoved {
		t.Errorf("c3 anchored to %+v, want the removed old line 2", line)
	}
}

func TestBuildKeepsCommentsWhoseLineIsNoLongerInTheDiff(t *testing.T) {
	comments := []store.Comment{
		{ID: "stale", File: "alpha.go", Line: 900, Side: store.SideNew, Body: "moved away", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	row := doc.RowOfComment("stale")
	if row < 0 {
		t.Fatal("a comment whose line left the diff was dropped")
	}
	if doc.Rows[row].File != 0 {
		t.Errorf("stale comment landed on file %d, want alpha.go", doc.Rows[row].File)
	}
}

func TestBuildPlacesEachCommentOnceAcrossBothSides(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	staged := *entries[0].Unstaged
	entries[0].Staged = &staged

	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 4, Side: store.SideNew, Body: "once", Author: store.AuthorUser},
	}
	doc := Build(&app.Session{Files: entries[:1], Stageable: true}, comments, nil, LayoutUnified)

	seen := 0
	for _, r := range doc.Rows {
		if r.Kind == RowComment {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("comment rows = %d, want 1 even though the line appears on both sides", seen)
	}
}

func TestBuildReservesRowsForTheCommentBeingWritten(t *testing.T) {
	draft := Draft{anchor: anchor{path: "alpha.go", line: 4, side: store.SideNew}, Height: 3}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified, WithDraft(draft))

	if doc.DraftRow < 0 {
		t.Fatal("the editor was not placed")
	}
	prev := doc.Rows[doc.DraftRow-1]
	if prev.Kind != RowLine {
		t.Fatalf("the editor follows a %v, want the line it comments on", prev.Kind)
	}
	if line := doc.Hunks[prev.Hunk].Hunk.Lines[prev.Left]; line.NewLine != 4 {
		t.Errorf("the editor sits under new line %d, want 4", line.NewLine)
	}
	for i := range draft.Height {
		if got := doc.Rows[doc.DraftRow+i].Kind; got != RowDraft {
			t.Errorf("row %d of the editor is a %v, want a draft row", i, got)
		}
	}
	if doc.IsStop(doc.DraftRow) {
		t.Error("the cursor can rest on the editor, which has the keyboard already")
	}
}

func TestBuildPutsAFileLevelDraftUnderItsFileHeader(t *testing.T) {
	draft := Draft{anchor: anchor{path: "beta.txt", side: store.SideNew}, Height: 2}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified, WithDraft(draft))

	if doc.DraftRow < 0 {
		t.Fatal("the editor was not placed")
	}
	if prev := doc.Rows[doc.DraftRow-1]; prev.Kind != RowFile || doc.Files[prev.File].Entry.Path != "beta.txt" {
		t.Errorf("the editor follows a %v, want beta.txt's header", prev.Kind)
	}
}

func TestBuildKeepsADraftWhoseLineIsNoLongerInTheDiff(t *testing.T) {
	draft := Draft{anchor: anchor{path: "alpha.go", line: 900, side: store.SideNew}, Height: 2}
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified, WithDraft(draft))

	if doc.DraftRow < 0 {
		t.Fatal("a draft whose line left the diff was dropped")
	}
	if doc.Rows[doc.DraftRow].File != 0 {
		t.Errorf("the editor landed on file %d, want alpha.go", doc.Rows[doc.DraftRow].File)
	}
}

func TestBuildPlacesTheDraftOnceAcrossBothSides(t *testing.T) {
	entries := parseFiles(t, twoFileDiff)
	staged := *entries[0].Unstaged
	entries[0].Staged = &staged

	draft := Draft{anchor: anchor{path: "alpha.go", line: 4, side: store.SideNew}, Height: 2}
	doc := Build(&app.Session{Files: entries[:1], Stageable: true}, nil, nil, LayoutUnified, WithDraft(draft))

	rows := 0
	for _, r := range doc.Rows {
		if r.Kind == RowDraft {
			rows++
		}
	}
	if rows != draft.Height {
		t.Errorf("draft rows = %d, want %d even though the line appears on both sides", rows, draft.Height)
	}
}

func TestBuildSplitsMultiLineCommentBodiesIntoRows(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Body: "first\nsecond\nthird", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	head := doc.RowOfComment("c1")
	if head < 0 {
		t.Fatal("comment was not placed")
	}
	for i, want := range []string{"first", "second", "third"} {
		row := doc.Rows[head+i]
		if row.Kind != RowComment || row.Text != want {
			t.Fatalf("row %d = %+v, want comment text %q", head+i, row, want)
		}
		if wantHead := i == 0; row.Head != wantHead {
			t.Errorf("row %d Head = %v, want %v", head+i, row.Head, wantHead)
		}
	}
	// Only the first line is a cursor stop, so j does not walk a long comment
	// line by line.
	if !doc.IsStop(head) || doc.IsStop(head+1) {
		t.Error("only the first row of a comment should be a stop")
	}
}

func TestNavigationStepsBetweenHunksAndFiles(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	first := doc.FirstStop()
	if doc.Rows[first].Kind != RowFile {
		t.Fatalf("first stop is a %v, want a file header", doc.Rows[first].Kind)
	}

	hunk := doc.NextMark(first)
	if doc.Rows[hunk].Kind != RowHunk {
		t.Fatalf("second mark is a %v, want a hunk header", doc.Rows[hunk].Kind)
	}
	if back := doc.PrevMark(hunk); back != first {
		t.Errorf("PrevMark = %d, want %d", back, first)
	}

	// From inside the first file, K goes to the top of the current file, and only
	// then to the previous one.
	if got := doc.PrevFile(hunk); got != first {
		t.Errorf("PrevFile from a hunk = %d, want the file header %d", got, first)
	}

	second := doc.NextFile(hunk)
	if doc.Rows[second].Kind != RowFile || doc.Files[doc.Rows[second].File].Entry.Path != "beta.txt" {
		t.Fatalf("NextFile landed on row %d (%v)", second, doc.Rows[second].Kind)
	}
	if got := doc.PrevFile(second); got != first {
		t.Errorf("PrevFile from beta.txt header = %d, want alpha.go header %d", got, first)
	}
}

// The cursor rests on diff lines, so stepping walks into a hunk body while j and
// k jump over it to the next thing worth landing on.
func TestStopsIncludeDiffLinesButMarksDoNot(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	for i, r := range doc.Rows {
		switch r.Kind {
		case RowLine, RowNote:
			if !doc.IsStop(i) || doc.IsMark(i) {
				t.Errorf("row %d (%v) is stop=%v mark=%v, want a stop that is not a mark",
					i, r.Kind, doc.IsStop(i), doc.IsMark(i))
			}
		case RowBlank:
			if doc.IsStop(i) {
				t.Errorf("row %d is the blank between files and should not hold the cursor", i)
			}
		}
	}

	hunk := doc.RowOfHunk(0)
	if got := doc.NextStop(hunk); doc.Rows[got].Kind != RowLine {
		t.Errorf("stepping off a hunk header lands on a %v, want the first line of its body", doc.Rows[got].Kind)
	}
	if got := doc.NextMark(hunk); doc.Rows[got].Kind == RowLine {
		t.Error("j landed inside a hunk body")
	}
}

func TestNavigationAtTheEdgesStaysPut(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	if got := doc.PrevStop(doc.FirstStop()); got != doc.FirstStop() {
		t.Errorf("PrevStop at the top = %d, want %d", got, doc.FirstStop())
	}
	last := doc.LastStop()
	if got := doc.NextStop(last); got != last {
		t.Errorf("NextStop at the bottom = %d, want %d", got, last)
	}
	if got := doc.NextFile(last); got != last {
		t.Errorf("NextFile at the bottom = %d, want %d", got, last)
	}
}

func TestNearestSnapsOntoAStop(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	blank := -1
	for i, r := range doc.Rows {
		if r.Kind == RowBlank {
			blank = i
			break
		}
	}
	if blank < 0 {
		t.Fatal("no blank rows")
	}
	if got := doc.Nearest(blank); !doc.IsStop(got) {
		t.Errorf("Nearest(%d) = %d, which is not a stop", blank, got)
	}
	if got := doc.Nearest(doc.Len() + 50); !doc.IsStop(got) {
		t.Errorf("Nearest past the end = %d, which is not a stop", got)
	}
}

func TestTargetAtDistinguishesFilesFromHunks(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	file := doc.TargetAt(doc.RowOfFile(0))
	if file.Kind != TargetFile || file.Path != "alpha.go" {
		t.Errorf("file header target = %+v", file)
	}

	hunk := doc.TargetAt(doc.RowOfHunk(0))
	if hunk.Kind != TargetHunk || hunk.Hunk != 0 || hunk.Path != "alpha.go" {
		t.Errorf("hunk header target = %+v", hunk)
	}
}

// Staging is whole-file, so every row inside a file has to resolve to it.
func TestFileTargetAtResolvesEveryRowOfAFileToTheFile(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	rows := map[string]int{
		"file header":  doc.RowOfFile(0),
		"hunk header":  doc.RowOfHunk(0),
		"changed line": doc.RowOfLine(0, 2),
		"context line": doc.RowOfLine(0, 0),
	}
	for name, row := range rows {
		file, ok := doc.FileTargetAt(row)
		if !ok || file.Entry.Path != "alpha.go" {
			t.Errorf("FileTargetAt(%s) = %q (ok=%v), want alpha.go", name, file.Entry.Path, ok)
		}
	}

	if _, ok := doc.FileTargetAt(-1); ok {
		t.Error("a row outside the document reports a file to stage")
	}
}

func TestTargetAtADiffLineAddressesThatLine(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	// Line 2 of the first hunk is a removal, line 0 is context.
	change := doc.TargetAt(doc.RowOfLine(0, 2))
	if change.Kind != TargetLine || change.Hunk != 0 || change.Path != "alpha.go" {
		t.Errorf("changed line target = %+v", change)
	}
	context := doc.RowOfLine(0, 0)
	if got := doc.TargetAt(context); got.Kind != TargetLine {
		t.Errorf("context line target = %+v, want the line itself", got)
	}
	if _, index, ok := doc.LineAt(context); !ok || index != 0 {
		t.Errorf("LineAt a context row = %d (ok=%v), want line 0 — a comment can still land there", index, ok)
	}
}

// A split row can hold a removal and the addition that replaced it. A comment on
// it takes the new side alone.
func TestSplitRowAddressesTheNewSideForComments(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutSplit)

	row := doc.RowOfLine(0, 2)
	if got := doc.Rows[row].Right; got != 3 {
		t.Fatalf("the row holding line 2 has Right = %d, want the addition at 3", got)
	}
	if _, index, ok := doc.LineAt(row); !ok || index != 3 {
		t.Errorf("LineAt = %d (ok=%v), want the new side at 3", index, ok)
	}
}

func TestTargetAtCommentRowActsOnItsFile(t *testing.T) {
	comments := []store.Comment{{ID: "c1", File: "beta.txt", Body: "note", Author: store.AuthorUser}}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	row := doc.RowOfComment("c1")
	target := doc.TargetAt(row)
	if target.Kind != TargetFile || target.Path != "beta.txt" {
		t.Errorf("comment row target = %+v, want beta.txt as a file", target)
	}
}

func TestRowOfLineFindsBothSidesInSplitLayout(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutSplit)

	lines := doc.Hunks[0].Hunk.Lines
	for i, l := range lines {
		if !l.IsChange() {
			continue
		}
		if doc.RowOfLine(0, i) < 0 {
			t.Errorf("line %d (%s) has no row in split layout", i, l.Render())
		}
	}
}

func TestLayoutToggles(t *testing.T) {
	if got := LayoutUnified.Toggle(); got != LayoutSplit {
		t.Errorf("unified toggles to %v", got)
	}
	if got := LayoutSplit.Toggle(); got != LayoutUnified {
		t.Errorf("split toggles to %v", got)
	}
	if LayoutUnified.String() != "unified" || LayoutSplit.String() != "split" {
		t.Error("layout names are wrong")
	}
}

func TestEmptyNoteExplainsWhyAFileHasNoHunks(t *testing.T) {
	const modeOnly = `diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`
	doc := Build(newSession(t, modeOnly), nil, nil, LayoutUnified)
	if !hasNote(doc, "100755") {
		t.Errorf("rows = %v, want a note naming the new mode", noteTexts(doc))
	}
}

func hasNote(d Document, substr string) bool {
	for _, text := range noteTexts(d) {
		if strings.Contains(text, substr) {
			return true
		}
	}
	return false
}

func noteTexts(d Document) []string {
	var out []string
	for _, r := range d.Rows {
		if r.Kind == RowNote {
			out = append(out, r.Text)
		}
	}
	return out
}

func kinds(d Document) []RowKind {
	out := make([]RowKind, 0, len(d.Rows))
	for _, r := range d.Rows {
		out = append(out, r.Kind)
	}
	return out
}

func hunkLines() []git.Line {
	return []git.Line{
		{Kind: git.LineContext, Text: "a", OldLine: 1, NewLine: 1},
		{Kind: git.LineRemoved, Text: "b", OldLine: 2},
		{Kind: git.LineAdded, Text: "B", NewLine: 2},
		{Kind: git.LineContext, Text: "c", OldLine: 3, NewLine: 3},
	}
}
