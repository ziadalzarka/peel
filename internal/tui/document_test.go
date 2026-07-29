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

	c1 := rowOfComment(doc, "c1")
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

	c3 := rowOfComment(doc, "c3")
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

	row := rowOfComment(doc, "stale")
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

func TestBuildSplitsMultiLineCommentBodiesIntoRows(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Body: "first\nsecond\nthird", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	head := rowOfComment(doc, "c1")
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

	hunk := doc.NextStop(first)
	if doc.Rows[hunk].Kind != RowHunk {
		t.Fatalf("second stop is a %v, want a hunk header", doc.Rows[hunk].Kind)
	}
	if back := doc.PrevStop(hunk); back != first {
		t.Errorf("PrevStop = %d, want %d", back, first)
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

	lineRow := -1
	for i, r := range doc.Rows {
		if r.Kind == RowLine {
			lineRow = i
			break
		}
	}
	if lineRow < 0 {
		t.Fatal("no line rows")
	}
	if got := doc.Nearest(lineRow); !doc.IsStop(got) {
		t.Errorf("Nearest(%d) = %d, which is not a stop", lineRow, got)
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
	if file.Staged {
		t.Error("an unstaged file should not report Staged")
	}

	hunk := doc.TargetAt(doc.RowOfHunk(0))
	if hunk.Kind != TargetHunk || hunk.Hunk != 0 || hunk.Path != "alpha.go" {
		t.Errorf("hunk header target = %+v", hunk)
	}
}

func TestTargetAtCommentRowActsOnItsFile(t *testing.T) {
	comments := []store.Comment{{ID: "c1", File: "beta.txt", Body: "note", Author: store.AuthorUser}}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	row := rowOfComment(doc, "c1")
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

func rowOfComment(d Document, id string) int {
	for i, r := range d.Rows {
		if r.Kind != RowComment || !r.Head {
			continue
		}
		if d.Comments[r.Comment].ID == id {
			return i
		}
	}
	return -1
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
