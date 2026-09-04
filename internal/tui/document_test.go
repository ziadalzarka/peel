package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
	doc := Build(partStagedSession(t), nil, nil, LayoutUnified, WithSideFolds(map[string]bool{"alpha.go": false}))

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

// A file git holds in both places at once is drawn as two labelled halves, so
// the change already reviewed and the change still to read do not run together.
func TestBuildHeadsBothHalvesOfAPartStagedFile(t *testing.T) {
	doc := Build(partStagedSession(t), nil, nil, LayoutUnified, WithSideFolds(map[string]bool{"alpha.go": false}))

	if len(doc.Sides) != 2 {
		t.Fatalf("sides = %d, want the index's and the working tree's", len(doc.Sides))
	}
	if !doc.Sides[0].Staged || doc.Sides[1].Staged {
		t.Errorf("sides = %v, want the index's first", doc.Sides)
	}
	for i, side := range doc.Sides {
		if got := doc.Rows[side.Row].Kind; got != RowSide {
			t.Errorf("side %d heads row kind %v, want RowSide", i, got)
		}
		if len(side.Hunks) != 1 {
			t.Errorf("side %d has %d hunks, want 1", i, len(side.Hunks))
		}
	}
	if got := doc.RowOfSide("alpha.go", store.OriginIndex); got != doc.Sides[0].Row {
		t.Errorf("RowOfSide(index) = %d, want %d", got, doc.Sides[0].Row)
	}
}

// The half already in the index opens folded: it has been reviewed and put away,
// so what a part-staged file shows is what is left to read.
func TestBuildFoldsTheStagedHalfOfAPartStagedFile(t *testing.T) {
	doc := Build(partStagedSession(t), nil, nil, LayoutUnified)

	if !doc.Sides[0].Folded {
		t.Error("the index's half should open folded")
	}
	if doc.Sides[1].Folded {
		t.Error("the working tree's half should be open — it is what is left to review")
	}
	if len(doc.Hunks) != 1 || doc.Hunks[0].Staged {
		t.Fatalf("hunks = %d, want only the working tree's", len(doc.Hunks))
	}
	if got := doc.SideAt(doc.Sides[0].Row); got != 0 {
		t.Errorf("SideAt(heading) = %d, want 0", got)
	}
	if got := doc.SideAt(doc.RowOfHunk(0)); got != -1 {
		t.Errorf("SideAt(a hunk header) = %d, want -1", got)
	}
}

// A file whose changes are all in the index has one half like any other file,
// and a heading over it names nothing to tell it from.
func TestBuildDoesNotHeadSidesOfAFullyStagedFile(t *testing.T) {
	doc := Build(sessionOf([]git.FileEntry{fullyStagedFile(t)}), nil, nil, LayoutUnified)

	if len(doc.Sides) != 0 {
		t.Fatalf("sides = %v, want none where there is nothing to tell apart", doc.Sides)
	}
	if len(doc.Hunks) != 1 {
		t.Errorf("hunks = %d, want the staged one shown", len(doc.Hunks))
	}
}

// The fold of a part-staged file's index half is remembered by path, and staging
// the rest of the file leaves that memory behind. It must not survive as a
// heading: the file has one half now, and the heading would hide it behind a
// line saying "staged" over a file already marked staged twice over.
func TestBuildDropsAStaleSideFoldWhenTheFileIsFullyStaged(t *testing.T) {
	folds := map[string]bool{"alpha.go": true}
	doc := Build(sessionOf([]git.FileEntry{fullyStagedFile(t)}), nil, nil, LayoutUnified,
		WithSideFolds(folds))

	if len(doc.Sides) != 0 {
		t.Fatalf("sides = %v, want none — the file has one half", doc.Sides)
	}
	if len(doc.Hunks) != 1 {
		t.Errorf("hunks = %d, want the file's diff on screen rather than folded behind a heading", len(doc.Hunks))
	}
}

// fullyStagedFile gives alpha.go changes in the index and none in the working
// tree, which is what a file looks like once it has been staged whole.
func fullyStagedFile(t *testing.T) git.FileEntry {
	t.Helper()
	entries := parseFiles(t, twoFileDiff)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	return entries[0]
}

// An ordinary file — everything still in the working tree — is left as the plain
// run of hunks it has always been. A heading over every file says nothing.
func TestBuildDoesNotHeadSidesOfAnUnstagedFile(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	if len(doc.Sides) != 0 {
		t.Errorf("sides = %v, want none where there is nothing to tell apart", doc.Sides)
	}
}

// partStagedSession gives alpha.go changes in both places at once, which is the
// shape git can produce and peel has to read.
func partStagedSession(t *testing.T) *app.Session {
	t.Helper()
	entries := parseFiles(t, twoFileDiff)
	staged := *entries[0].Unstaged
	entries[0].Staged = &staged
	return sessionOf(entries[:1])
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

// A part-staged file puts two line 3s on screen — "three" in the index, and
// "inserted" on disk — and a note has to land on the one it was written on.
// Placing it by line number alone put every working-tree note on whatever the
// index happened to hold at that number, several lines and one file away from
// the code it was about.
func TestBuildPlacesANoteOnTheHalfItWasWrittenOn(t *testing.T) {
	comments := []store.Comment{
		{ID: "disk", File: "notes.txt", Line: 3, Side: store.SideNew, Origin: store.OriginWorktree,
			Body: "about the inserted line", Author: store.AuthorUser},
		{ID: "index", File: "notes.txt", Line: 3, Side: store.SideNew, Origin: store.OriginIndex,
			Body: "about what was staged", Author: store.AuthorUser},
	}
	doc := Build(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}), comments, nil, LayoutUnified,
		WithSideFolds(map[string]bool{"notes.txt": false}))

	text, staged := codeUnder(t, doc, "disk")
	if text != "inserted" || staged {
		t.Errorf("the working-tree note sits under %q (staged=%v), want \"inserted\" on disk", text, staged)
	}
	text, staged = codeUnder(t, doc, "index")
	if text != "three" || !staged {
		t.Errorf("the index note sits under %q (staged=%v), want \"three\" in the index", text, staged)
	}
}

// A note from an older peel, or from an agent that named no half, still lands:
// it goes where it always went, on the first line matching its number.
func TestBuildPlacesANoteThatNamesNoHalf(t *testing.T) {
	comments := []store.Comment{
		{ID: "old", File: "notes.txt", Line: 3, Side: store.SideNew, Body: "no origin", Author: store.AuthorUser},
	}
	doc := Build(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}), comments, nil, LayoutUnified,
		WithSideFolds(map[string]bool{"notes.txt": false}))

	if text, _ := codeUnder(t, doc, "old"); text != "three" {
		t.Errorf("a note naming no half sits under %q, want the first line 3 in the document", text)
	}
}

// The editor for a note being written opens where the note will end up, so it
// has to tell the two halves apart the same way.
func TestBuildOpensTheEditorOnTheHalfBeingCommentedOn(t *testing.T) {
	draft := Draft{
		anchor: anchor{path: "notes.txt", line: 3, side: store.SideNew, origin: store.OriginWorktree},
		Height: 3,
	}
	doc := Build(sessionOf([]git.FileEntry{partStagedFile(t, "notes.txt")}), nil, nil, LayoutUnified,
		WithDraft(draft), WithSideFolds(map[string]bool{"notes.txt": false}))

	if doc.DraftRow < 0 {
		t.Fatal("the editor was not placed")
	}
	prev := doc.Rows[doc.DraftRow-1]
	if prev.Kind != RowLine {
		t.Fatalf("the editor follows a %v, want the line it comments on", prev.Kind)
	}
	ref := doc.Hunks[prev.Hunk]
	if got := ref.Hunk.Lines[prev.Left].Text; got != "inserted" || ref.Staged {
		t.Errorf("the editor opened under %q (staged=%v), want \"inserted\" on disk", got, ref.Staged)
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

func TestBuildWrapsALongCommentLineToTheWidthItHas(t *testing.T) {
	body := "this review comment is far too long to sit on one row of a narrow pane, so it has to run on"
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Body: body, Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified, WithPaneWidth(46))

	head := doc.RowOfComment("c1")
	if head < 0 {
		t.Fatal("comment was not placed")
	}
	var rows []string
	for i := head; i < len(doc.Rows) && doc.Rows[i].Kind == RowComment; i++ {
		rows = append(rows, doc.Rows[i].Text)
	}
	if len(rows) < 3 {
		t.Fatalf("comment took %d rows, want it wrapped across several: %q", len(rows), rows)
	}
	// The marker, the indent and the bar come off the pane, then the tag naming
	// the author comes off what is left, and every row is wrapped to that so the
	// ones below line up under the first.
	width := halfNone.room(46) - ansi.StringWidth(commentTag(comments[0]))
	for i, row := range rows {
		if got := ansi.StringWidth(row); got > width {
			t.Errorf("row %d is %d wide, want no more than %d: %q", i, got, width, row)
		}
	}
	if got := strings.Join(rows, " "); got != body {
		t.Errorf("wrapped rows read as %q, want %q", got, body)
	}
	if !doc.Rows[head].Head || doc.Rows[head+1].Head {
		t.Error("only the first row of a wrapped comment is its head")
	}
}

// A comment is as likely to hold a list or a snippet as it is prose, so the
// line breaks it was written with are its own — wrapping only adds to them.
func TestBuildKeepsACommentsOwnLineBreaksWhenWrapping(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "alpha.go", Body: "short\n\n- one\n- two", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified, WithPaneWidth(46))

	head := doc.RowOfComment("c1")
	if head < 0 {
		t.Fatal("comment was not placed")
	}
	for i, want := range []string{"short", "", "- one", "- two"} {
		if got := doc.Rows[head+i].Text; got != want {
			t.Errorf("row %d = %q, want %q", i, got, want)
		}
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

// With nothing in the way a jump of ten lines is ten presses of the arrow, and
// the same jump back reaches the row it started on.
func TestLeapCountsCursorPositions(t *testing.T) {
	// One file, two hunks, and no copy of it to offer any hidden code from — so
	// the ten lines run through a hunk header with nothing to stop them.
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified)

	first := doc.FirstStop()
	got := doc.Leap(first, 10)

	want := first
	for range 10 {
		want = doc.NextStop(want)
	}
	if got != want {
		t.Fatalf("Leap(%d, 10) = %d, want %d — the row ten presses of down reaches", first, got, want)
	}
	if doc.endsLeap(got) {
		t.Fatalf("the jump landed on row %d, which would have stopped it anyway — "+
			"this test is not measuring a run of ten lines", got)
	}
	if back := doc.Leap(got, -10); back != first {
		t.Errorf("ten lines back up = %d, want the row it started on, %d", back, first)
	}
}

// A file header is as far as one press goes. A jump that would have run past it
// lands on it instead, in either direction, and the next press carries on — so
// the header of the file being left and the header of the one below are both
// somewhere the brackets stop rather than rows they cross.
func TestLeapStopsAtAFileHeader(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	// Two lines into the second file, where ten lines up would otherwise land
	// somewhere inside the first one.
	header := doc.RowOfFile(1)
	inside := doc.NextStop(doc.NextStop(header))
	if doc.FileAt(inside) != 1 {
		t.Fatalf("row %d is in file %d, want two stops into the second file", inside, doc.FileAt(inside))
	}
	if got := doc.Leap(inside, -10); got != header {
		t.Errorf("[ from row %d = %d, want its own file's header at %d", inside, got, header)
	}
	if got := doc.Leap(header, -10); doc.FileAt(got) != 0 {
		t.Errorf("[ from the header of file 1 = %d, in file %d — want it carried into the file above",
			got, doc.FileAt(got))
	}

	// The same going down: the count runs out of the first file's body and stops
	// on the second file's header rather than carrying on into its diff.
	from := doc.PrevStop(doc.PrevStop(header))
	if got := doc.Leap(from, 10); got != header {
		t.Errorf("] from row %d = %d, want the next file's header at %d", from, got, header)
	}
}

// A row standing where the diff left code out stops the jump too: it is the row
// that says the code the leap would have crossed was never shown.
func TestLeapStopsWhereTheDiffLeavesCodeOut(t *testing.T) {
	doc := Build(newSession(t, contextDiff), nil, nil, LayoutUnified, WithExpansion(expansionOf(nil)))

	// The run under the first hunk, and the first line of the body above it — the
	// row after the run the diff left out over the hunk's head.
	below := doc.Expands[1].Row
	body := doc.NextStop(doc.Expands[0].Row)
	if doc.Rows[body].Kind != RowLine || below-body <= 1 {
		t.Fatalf("row %d is a %v with the run at %d — want a hunk body between them", body, doc.Rows[body].Kind, below)
	}
	if got := doc.Leap(body, 10); got != below {
		t.Errorf("] from the first line of the hunk = %d, want the ▾ row under it at %d", got, below)
	}

	// And going up, from inside the second hunk, to the row over its head.
	above := doc.Expands[2].Row
	within := doc.NextStop(doc.NextStop(above))
	if got := doc.Leap(within, -10); got != above {
		t.Errorf("[ from row %d = %d, want the ▴ row at %d", within, got, above)
	}
}

// The heading over one half of a part-staged file stops a leap the way the file
// header does. Landing under it having crossed it is the failure the two halves
// exist to prevent: the same line number means a different line in each.
func TestLeapStopsAtTheHalfOfAFileItReaches(t *testing.T) {
	session := newSession(t, twoFileDiff)
	session.Files = append(session.Files, partStagedFile(t, "notes.txt"))
	doc := Build(session, nil, nil, LayoutUnified)

	// The working tree's half, which is the one left open.
	heading := doc.RowOfSide("notes.txt", store.OriginWorktree)
	if heading < 0 || doc.Rows[heading].Kind != RowSide {
		t.Fatalf("row %d is not the unstaged half's heading", heading)
	}

	last := doc.LastStop()
	if doc.FileAt(last) != doc.FileAt(heading) || last-heading <= 1 {
		t.Fatalf("row %d is not inside the part-staged file's body below %d", last, heading)
	}
	if got := doc.Leap(last, -10); got != heading {
		t.Errorf("[ from the last line of the file = %d, want its half's heading at %d", got, heading)
	}
	if got := doc.Leap(heading, 10); got <= heading {
		t.Errorf("] from the heading = %d, want it carried on down into the half's own hunks", got)
	}
}

// A walkthrough heading stops one too. It is the note explaining the group of
// files under it, and a jump that crossed it would put the reviewer inside a
// group whose explanation went by unread.
func TestLeapStopsAtAWalkthroughHeading(t *testing.T) {
	groups := Groups{Steps: []store.Step{
		{Title: "the change", Body: "why it is here", Files: []string{"alpha.go"}},
		{Title: "the rest", Body: "and the rest of it", Files: []string{"beta.txt", "gamma.md"}},
	}, Width: 40}
	doc := Build(newSession(t, threeFileDiff), nil, nil, LayoutUnified, WithGroups(groups))

	heading := doc.Steps[1].Row
	if doc.Rows[heading].Kind != RowStep {
		t.Fatalf("row %d is not the second group's heading", heading)
	}

	// Up from the header of the first file the group introduces — a file's own
	// header is the nearer boundary from anywhere inside it, so the heading above
	// it is what the next press reaches.
	below := doc.RowOfFile(1)
	if doc.Steps[1].Files[0] != 1 {
		t.Fatalf("file 1 is not the first the second group introduces: %v", doc.Steps[1].Files)
	}
	if got := doc.Leap(below, -10); got != heading {
		t.Errorf("[ from the file at row %d = %d, want the walkthrough heading at %d", below, got, heading)
	}
	// And down into it from the file the group before it ends on.
	above := doc.PrevStop(heading)
	if got := doc.Leap(above, 10); got != heading {
		t.Errorf("] from row %d = %d, want the walkthrough heading at %d", above, got, heading)
	}
}

// A note written on the diff stops one too. It is the one thing on the screen
// that is not the code, and a jump that crossed it would have read the lines it
// hangs off without what the last pass had to say about them.
func TestLeapStopsAtAComment(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "wide.go", Line: 3, Side: store.SideNew, Body: "why this line", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, contextDiff), comments, nil, LayoutUnified)

	note := doc.RowOfComment("c1")
	if note < 0 || doc.Rows[note].Kind != RowComment || !doc.Rows[note].Head {
		t.Fatalf("row %d is not the head of the note on line 3", note)
	}

	// The top of the file, far enough above the note that ten presses of the
	// arrow from it run out the other side.
	above := doc.FirstStop()
	stepped := above
	for range 10 {
		stepped = doc.NextStop(stepped)
	}
	if stepped <= note {
		t.Fatalf("ten stops down from row %d reach row %d, which is above the note at %d — "+
			"this test is not measuring a jump the note interrupts", above, stepped, note)
	}
	if got := doc.Leap(above, 10); got != note {
		t.Errorf("] from row %d = %d, want the note at %d", above, got, note)
	}

	// And up to it from the row those ten presses reached, below the note.
	if got := doc.Leap(stepped, -10); got != note {
		t.Errorf("[ from row %d = %d, want the note at %d", stepped, got, note)
	}
}

// A jump does not park on the code a note was written about with the note itself
// one row further on. Running out of count inside the run gives the count up and
// takes the note, which is what the run is barred for.
func TestLeapEndingOnANotedLineTakesTheNote(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "wide.go", Line: 3, Side: store.SideNew, Body: "why this line", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, contextDiff), comments, nil, LayoutUnified)

	note := doc.RowOfComment("c1")
	line := doc.PrevStop(note)
	if !doc.notedLine(line) {
		t.Fatalf("row %d is not the line the note was written about", line)
	}

	// The count that runs out exactly on that line, counted from the top.
	from, n := doc.FirstStop(), 0
	for at := from; at != line; n++ {
		next := doc.NextStop(at)
		if next == at {
			t.Fatalf("row %d is not below the top of the diff", line)
		}
		at = next
	}
	if got := doc.Leap(from, n); got != note {
		t.Errorf("] of %d from row %d = %d, want the note at %d rather than the line it is about at %d",
			n, from, got, note, line)
	}

	// Upwards the line is the reviewer's own business: a jump out of a note walks
	// into the run it was written about rather than being pulled back onto it.
	if got := doc.Leap(note, -1); got != line {
		t.Errorf("[ from the note at %d = %d, want the line it is about at %d", note, got, line)
	}
}

// Counting stops where the document does. A jump longer than what is left lands
// on the last row there is rather than off the end of the rows.
func TestLeapStopsAtTheEndsOfTheDocument(t *testing.T) {
	doc := Build(newSession(t, twoFileDiff), nil, nil, LayoutUnified)

	if got := doc.Leap(doc.LastStop(), 1); got != doc.LastStop() {
		t.Errorf("Leap one past the bottom = %d, want it left at %d", got, doc.LastStop())
	}
	if got := doc.Leap(doc.FirstStop(), -1); got != doc.FirstStop() {
		t.Errorf("Leap one past the top = %d, want it left at %d", got, doc.FirstStop())
	}
	if got := doc.Leap(doc.FirstStop(), 0); got != doc.FirstStop() {
		t.Errorf("Leap of nothing = %d, want it standing still at %d", got, doc.FirstStop())
	}
}

// Whatever the document holds — a walkthrough note, a comment several rows long,
// a file whose diff is folded away, a file git holds in both places at once, a
// run of code the diff left out — a jump of n lands on a row the arrows can
// reach, no further than n presses of one, and no further than the first file
// header or hidden run in between. Either layout: the split pairs a hunk's lines
// up differently but the rows that end a jump are the same rows.
func TestLeapGoesNoFurtherThanSteppingOrTheFirstThingInTheWay(t *testing.T) {
	comments := []store.Comment{
		{ID: "c1", File: "wide.go", Line: 3, Side: store.SideNew, Body: "first\nsecond\nthird", Author: store.AuthorUser},
		{ID: "c2", File: "beta.txt", Body: "a note on the file as a whole", Author: store.AuthorAgent},
		{ID: "c3", File: "wide.go", Line: 47, EndLine: 51, Side: store.SideNew,
			Body: "a note on a run of lines rather than one", Author: store.AuthorUser},
	}
	groups := Groups{Steps: []store.Step{
		{Title: "the change", Body: "why it is here", Files: []string{"wide.go"}},
		{Title: "the rest", Body: "and the rest of it", Files: []string{"beta.txt", "gamma.md"}},
	}, Width: 40}
	session := newSession(t, contextDiff+threeFileDiff)
	session.Files = append(session.Files, partStagedFile(t, "notes.txt"))

	for _, layout := range []Layout{LayoutUnified, LayoutSplit} {
		t.Run(layout.String(), func(t *testing.T) {
			doc := Build(session, comments, map[string]bool{"gamma.md": true},
				layout, WithGroups(groups), WithPaneWidth(46), WithExpansion(expansionOf(nil)))
			assertLeapNeverOvershoots(t, doc)
		})
	}
}

// assertLeapNeverOvershoots checks every jump the brackets can make from every
// row of a document, against what stepping one line at a time reaches.
func assertLeapNeverOvershoots(t *testing.T, doc Document) {
	t.Helper()
	// A document missing any of these would still pass every check below, and
	// pass it about a diff simpler than the one this is meant to be walking.
	held := map[RowKind]bool{}
	for _, r := range doc.Rows {
		held[r.Kind] = true
	}
	for _, kind := range []RowKind{RowFile, RowHunk, RowLine, RowComment, RowStep, RowSide, RowExpand, RowBlank} {
		if !held[kind] {
			t.Fatalf("the document holds no %v row, so this is not the diff the test means to walk", kind)
		}
	}
	run := 0
	for i := range doc.Rows {
		if doc.notedLine(i) {
			run++
		}
	}
	if run < 2 {
		t.Fatalf("the document holds %d lines a note was written about, so the runs a jump crosses to reach a note go unwalked", run)
	}

	for start := range doc.Len() {
		if !doc.IsStop(start) {
			continue
		}
		for n := range 20 {
			for _, dir := range []int{1, -1} {
				step := doc.NextStop
				if dir < 0 {
					step = doc.PrevStop
				}
				got := doc.Leap(start, dir*n)
				if !doc.IsStop(got) {
					t.Fatalf("Leap(%d, %d) = %d, a row the cursor cannot rest on", start, dir*n, got)
				}

				// Walk the same way one stop at a time to see what the jump crossed
				// on its way there: never more than n of them — except the lines of
				// a run it crossed to reach the note under them — and never a row
				// that should have ended it.
				moved := 0
				for at := start; at != got; moved++ {
					next := step(at)
					if next == at {
						t.Fatalf("Leap(%d, %d) = %d, which stepping that way never reaches", start, dir*n, got)
					}
					if moved >= n && !(dir > 0 && doc.notedLine(at)) {
						t.Fatalf("Leap(%d, %d) = %d, counted past row %d, which is not a line of a run it is on its way through",
							start, dir*n, got, at)
					}
					if at = next; at != got && doc.endsLeap(at) {
						t.Fatalf("Leap(%d, %d) = %d, past row %d, which should have ended it",
							start, dir*n, got, at)
					}
				}
				// Going further than n is only for arriving at a note.
				if moved > n && !(doc.Rows[got].Kind == RowComment && doc.Rows[got].Head) {
					t.Fatalf("Leap(%d, %d) = %d, %d past what it counts, and not on a note",
						start, dir*n, got, moved-n)
				}
				// Stopping short of n is only allowed where something stopped it.
				if moved < n && !doc.endsLeap(got) && step(got) != got {
					t.Fatalf("Leap(%d, %d) = %d, %d lines short with nothing there to stop it",
						start, dir*n, got, n-moved)
				}
			}
		}
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

// The file key acts on the whole file, so every row inside one has to resolve to
// it — the cursor is never asked to be moved to the header first.
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

func TestDocumentMeasuresTheWidestLineWithTabsExpanded(t *testing.T) {
	doc := Build(newSession(t, longLineDiff), nil, nil, LayoutUnified)
	if want := ansi.StringWidth(wideLine); doc.CodeWidth != want {
		t.Errorf("CodeWidth = %d, want %d", doc.CodeWidth, want)
	}

	// A tab is one byte and eight columns, and it is columns the offset it
	// bounds is counted in.
	tabbed := Build(newSession(t, tabDiff), nil, nil, LayoutUnified)
	if want := ansi.StringWidth(expandTabs("\t\t\tdeeper(i)")); tabbed.CodeWidth != want {
		t.Errorf("CodeWidth over tab-indented code = %d, want %d", tabbed.CodeWidth, want)
	}
}

func TestDocumentMeasuresTheWidestFileHeader(t *testing.T) {
	doc := Build(newSession(t, longPathDiff), nil, nil, LayoutUnified)
	if want := ansi.StringWidth(longPath + " modified +1 -1"); doc.HeadWidth != want {
		t.Errorf("HeadWidth = %d, want %d", doc.HeadWidth, want)
	}

	// The counts are part of the header, so a header is only read to its end
	// once they are on screen too.
	if doc.HeadWidth <= ansi.StringWidth(longPath) {
		t.Errorf("HeadWidth = %d, want more than the %d columns the path alone takes",
			doc.HeadWidth, ansi.StringWidth(longPath))
	}
}

// A note outlives the change it was written on when the file is committed, put
// back, or stashed: the session stops holding it, and before this the note was
// drawn nowhere while the store, `C` and the code host all still had it. It is
// the reviewer's note either way, so it keeps a place on screen.
func TestBuildDrawsNotesWhoseFileHasLeftTheDiff(t *testing.T) {
	comments := []store.Comment{
		{ID: "here", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "still in the change", Author: store.AuthorUser},
		{ID: "gone", File: "vanished.go", Line: 7, Side: store.SideNew, Body: "left behind", Author: store.AuthorUser},
		{ID: "also", File: "vanished.go", Line: 9, Side: store.SideNew, Body: "left behind too", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	row := doc.RowOfComment("gone")
	if row < 0 {
		t.Fatal("a note whose file left the diff was drawn nowhere")
	}
	if doc.RowOfComment("also") < 0 {
		t.Error("the second note on the same file was drawn nowhere")
	}

	fi := doc.Rows[row].File
	if fi != 2 {
		t.Errorf("the note's file is document file %d, want it after the two the diff holds", fi)
	}
	if len(doc.Files) != 3 {
		t.Fatalf("files = %d, want one header per file, the missing one included", len(doc.Files))
	}
	f := doc.Files[fi]
	if !f.Orphan || f.Entry.Path != "vanished.go" {
		t.Fatalf("the note's file = %+v, want vanished.go marked as having no diff", f)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("hunks under a file with no diff = %d, want none", len(f.Hunks))
	}

	head := doc.RowOfFile(fi)
	if doc.Rows[head].Kind != RowFile || doc.Rows[head+1].Kind != RowNote {
		t.Fatalf("rows at the header = %v, %v, want a header and the note saying why there is no diff",
			doc.Rows[head].Kind, doc.Rows[head+1].Kind)
	}
	if got := doc.Rows[head+1].Text; got != orphanNote(2) {
		t.Errorf("the note under the header = %q, want it to say where the two notes' changes went", got)
	}
	if got := doc.Hunks; len(got) != 2 {
		t.Errorf("hunks = %d, want the two the diff holds and no more", len(got))
	}
}

// A file the diff still holds keeps its notes where they were: only the ones
// nothing on screen can claim go under a header of their own.
func TestBuildLeavesNotesOnFilesTheDiffHolds(t *testing.T) {
	comments := []store.Comment{
		{ID: "here", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "on a line", Author: store.AuthorUser},
	}
	doc := Build(newSession(t, twoFileDiff), comments, nil, LayoutUnified)

	if len(doc.Files) != 2 {
		t.Fatalf("files = %d, want only the two the diff holds", len(doc.Files))
	}
	for _, f := range doc.Files {
		if f.Orphan {
			t.Errorf("%s was drawn as a file with no diff", f.Entry.Path)
		}
	}
	if _, staged := codeUnder(t, doc, "here"); staged {
		t.Error("the note left the line it was written on")
	}
}
